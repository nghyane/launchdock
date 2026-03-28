package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type AuthType string

const (
	AuthOAuth  AuthType = "oauth"
	AuthAPIKey AuthType = "apikey"
)

type Credential struct {
	ID       string
	Provider string // "anthropic" | "openai"
	AuthType AuthType
	Label    string
	Source   string // "keychain:claude-code" | "file:~/.codex/auth.json" | "env:ANTHROPIC_API_KEY"
	Managed  bool

	// OAuth fields
	AccessToken  string
	RefreshToken string
	AccountID    string // chatgpt-account-id for OpenAI OAuth
	Email        string
	ExpiresAt    time.Time

	// API key field
	APIKey string

	// Runtime state
	CooldownUntil time.Time
}

// Token returns the bearer token for this credential.
func (c *Credential) Token() string {
	if c.AuthType == AuthAPIKey {
		return c.APIKey
	}
	return c.AccessToken
}

// IsExpired checks if the credential's access token has expired.
func (c *Credential) IsExpired() bool {
	if c.AuthType == AuthAPIKey {
		return false
	}
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt.Add(-5 * time.Minute)) // 5 min buffer
}

// --- Load from macOS Keychain (Claude OAuth) ---

func LoadFromKeychain() ([]Credential, error) {
	// Claude Code stores OAuth tokens in macOS Keychain:
	//   "Claude Code-credentials"          — primary account
	//   "Claude Code-credentials-<hex>"    — additional accounts
	services := listClaudeKeychainServices()

	var creds []Credential
	for _, service := range services {
		cred, err := loadKeychainEntry(service)
		if err != nil {
			slog.Debug("keychain entry not found", "service", service, "error", err)
			continue
		}
		creds = append(creds, *cred)
	}

	// Fallback: ~/.claude/.credentials.json
	if len(creds) == 0 {
		home, _ := os.UserHomeDir()
		if home != "" {
			credFile := filepath.Join(home, ".claude", ".credentials.json")
			if data, err := os.ReadFile(credFile); err == nil {
				if cred := parseClaudeCredentialJSON(data, "file:"+credFile); cred != nil {
					creds = append(creds, *cred)
				}
			}
		}
	}

	return creds, nil
}

// listClaudeKeychainServices scans keychain for all Claude Code credential entries.
func listClaudeKeychainServices() []string {
	cmd := exec.Command("security", "dump-keychain")
	out, err := cmd.Output()
	if err != nil {
		return []string{"Claude Code-credentials"}
	}

	re := regexp.MustCompile(`"(Claude Code-credentials(?:-[0-9a-f]+)?)"`)
	matches := re.FindAllStringSubmatch(string(out), -1)

	seen := map[string]bool{}
	var services []string

	// Primary first
	const primary = "Claude Code-credentials"
	for _, m := range matches {
		svc := m[1]
		if !seen[svc] {
			seen[svc] = true
			if svc == primary {
				services = append([]string{primary}, services...)
			} else {
				services = append(services, svc)
			}
		}
	}

	if len(services) == 0 {
		return []string{primary}
	}
	return services
}

func loadKeychainEntry(service string) (*Credential, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", service,
		"-w", // password only
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed for %s: %w", service, err)
	}

	raw := strings.TrimSpace(string(out))

	// Parse the JSON credential — may be nested under "claudeAiOauth"
	var wrapper struct {
		ClaudeAiOauth *struct {
			AccessToken  string      `json:"accessToken"`
			RefreshToken string      `json:"refreshToken"`
			ExpiresAt    json.Number `json:"expiresAt"`
		} `json:"claudeAiOauth"`
		AccessToken  string      `json:"accessToken"`
		RefreshToken string      `json:"refreshToken"`
		ExpiresAt    json.Number `json:"expiresAt"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return nil, fmt.Errorf("parse keychain JSON: %w", err)
	}

	accessToken := wrapper.AccessToken
	refreshToken := wrapper.RefreshToken
	expiresAtRaw := wrapper.ExpiresAt.String()
	if wrapper.ClaudeAiOauth != nil {
		accessToken = wrapper.ClaudeAiOauth.AccessToken
		refreshToken = wrapper.ClaudeAiOauth.RefreshToken
		expiresAtRaw = wrapper.ClaudeAiOauth.ExpiresAt.String()
	}

	if accessToken == "" {
		return nil, fmt.Errorf("no accessToken in keychain entry %s", service)
	}

	var expiresAt time.Time
	if expiresAtRaw != "" {
		if ms, err := strconv.ParseInt(expiresAtRaw, 10, 64); err == nil {
			expiresAt = time.UnixMilli(ms)
		} else if t, err := time.Parse(time.RFC3339, expiresAtRaw); err == nil {
			expiresAt = t
		}
	}

	return &Credential{
		Provider:     "anthropic",
		AuthType:     AuthOAuth,
		Label:        "Claude Keychain",
		Source:       "keychain:" + service,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// --- Load from file (Codex OAuth: ~/.codex/auth.json) ---

func LoadFromFile(path string) ([]Credential, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var auth struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			IDToken      string `json:"id_token"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
		LastRefresh string `json:"last_refresh"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if auth.Tokens.AccessToken == "" {
		return nil, fmt.Errorf("no access_token in %s", path)
	}

	// Decode JWT to get expiry
	expiresAt := extractJWTExpiry(auth.Tokens.AccessToken)

	return []Credential{{
		Provider:     "openai",
		AuthType:     AuthOAuth,
		Label:        "Codex OAuth (" + auth.AuthMode + ")",
		Source:       "file:" + path,
		AccessToken:  auth.Tokens.AccessToken,
		RefreshToken: auth.Tokens.RefreshToken,
		AccountID:    auth.Tokens.AccountID,
		Email:        extractOpenAIEmail(auth.Tokens.IDToken),
		ExpiresAt:    expiresAt,
	}}, nil
}

// --- Load from environment variable ---

func LoadFromEnv(envKey, provider string) (*Credential, error) {
	val := os.Getenv(envKey)
	if val == "" {
		return nil, fmt.Errorf("env %s not set", envKey)
	}
	return &Credential{
		Provider: provider,
		AuthType: AuthAPIKey,
		Label:    envKey,
		Source:   "env:" + envKey,
		APIKey:   val,
	}, nil
}

// --- Refresh ---

const (
	// OpenAI / Codex OAuth
	openAIOAuthEndpoint = "https://auth.openai.com/oauth/token"
	openAIClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"

	// Claude OAuth (from Claude Code binary)
	claudeOAuthEndpoint = "https://platform.claude.com/v1/oauth/token"
	claudeClientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeDefaultScopes = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

// RefreshClaudeOAuth refreshes a Claude OAuth token using the discovered endpoint.
// Claude uses JSON body (not form-encoded) and requires a scope field.
func RefreshClaudeOAuth(refreshToken string) (accessToken, newRefresh string, expiresAt time.Time, err error) {
	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeClientID,
		"scope":         claudeDefaultScopes,
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", claudeOAuthEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := APIClient.Do(req)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", time.Time{}, fmt.Errorf("refresh failed: status %d body=%s", resp.StatusCode, string(respBody))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", time.Time{}, fmt.Errorf("parse refresh response: %w", err)
	}

	exp := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	nr := result.RefreshToken
	if nr == "" {
		nr = refreshToken
	}
	return result.AccessToken, nr, exp, nil
}

// RefreshOpenAIOAuth refreshes an OpenAI/Codex OAuth token (form-encoded).
func RefreshOpenAIOAuth(refreshToken string) (accessToken, newRefresh string, expiresAt time.Time, err error) {
	resp, err := http.PostForm(openAIOAuthEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {openAIClientID},
	})
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", time.Time{}, fmt.Errorf("refresh failed: status %d body=%s", resp.StatusCode, string(respBody))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", time.Time{}, fmt.Errorf("parse refresh response: %w", err)
	}

	exp := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	nr := result.RefreshToken
	if nr == "" {
		nr = refreshToken
	}
	return result.AccessToken, nr, exp, nil
}

func RefreshViaCLI(command string) error {
	parts := strings.Fields(command)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- Helpers ---

// parseClaudeCredentialJSON parses a ~/.claude/.credentials.json file.
func parseClaudeCredentialJSON(data []byte, source string) *Credential {
	var wrapper struct {
		ClaudeAiOauth *struct {
			AccessToken  string      `json:"accessToken"`
			RefreshToken string      `json:"refreshToken"`
			ExpiresAt    json.Number `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil || wrapper.ClaudeAiOauth == nil {
		return nil
	}
	oauth := wrapper.ClaudeAiOauth
	if oauth.AccessToken == "" {
		return nil
	}
	var expiresAt time.Time
	if ms, err := strconv.ParseInt(oauth.ExpiresAt.String(), 10, 64); err == nil {
		expiresAt = time.UnixMilli(ms)
	}
	return &Credential{
		Provider:     "anthropic",
		AuthType:     AuthOAuth,
		Label:        "Claude Credentials File",
		Source:       source,
		AccessToken:  oauth.AccessToken,
		RefreshToken: oauth.RefreshToken,
		ExpiresAt:    expiresAt,
	}
}

func extractJWTExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp json.Number `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(claims.Exp.String(), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// LoadAllCredentials discovers all available credentials from known sources.
func LoadAllCredentials() []Credential {
	var all []Credential

	// 1. Claude OAuth from macOS Keychain
	if creds, err := LoadFromKeychain(); err == nil {
		all = append(all, creds...)
	}

	// 2. Codex OAuth from ~/.codex/auth.json
	home, _ := os.UserHomeDir()
	if home != "" {
		codexAuth := filepath.Join(home, ".codex", "auth.json")
		if creds, err := LoadFromFile(codexAuth); err == nil {
			all = append(all, creds...)
		}
	}

	// 3. Config file credentials (multi-account)
	configCreds := LoadFromConfig()
	all = append(all, configCreds...)

	// 4. Environment variables
	envSources := []struct {
		key      string
		provider string
	}{
		{"ANTHROPIC_API_KEY", "anthropic"},
		{"OPENAI_API_KEY", "openai"},
		{"GEMINI_API_KEY", "gemini"},
	}
	for _, src := range envSources {
		if cred, err := LoadFromEnv(src.key, src.provider); err == nil {
			all = append(all, *cred)
		}
	}

	return all
}
