package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// OAuthFlow implements the OAuth 2.0 Authorization Code + PKCE flow
// for adding Claude accounts to launchdock.

const (
	claudeAuthorizeURL = "https://claude.com/cai/oauth/authorize"
	openAIAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	openAIDefaultPort  = 1455
)

// RunOAuthFlow opens a browser for the user to authorize, captures the callback,
// exchanges the code for tokens, and saves the credential.
func RunOAuthFlow(label string) (*Credential, error) {
	// Generate PKCE
	verifier := generateCodeVerifier()
	challenge := generateCodeChallenge(verifier)
	state := generateState()

	// Start local callback server
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)
	scopes := claudeDefaultScopes

	authURL, err := url.Parse(claudeAuthorizeURL)
	if err != nil {
		return nil, fmt.Errorf("parse authorize url: %w", err)
	}
	params := authURL.Query()
	params.Set("code", "true")
	params.Set("client_id", claudeClientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", scopes)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", state)
	authURL.RawQuery = params.Encode()
	authorizeURL := authURL.String()

	// Channel to receive the auth code
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch")
			writeAuthError(w, http.StatusBadRequest, "Claude", "Sign-in could not be completed", "The callback state did not match this login request.", "state mismatch")
			return
		}
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			errCh <- fmt.Errorf("auth error: %s - %s", errMsg, r.URL.Query().Get("error_description"))
			writeAuthError(w, http.StatusUnauthorized, "Claude", "Authentication failed", "Launchdock could not finish connecting your Claude account.", errMsg)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			writeAuthError(w, http.StatusBadRequest, "Claude", "Authorization code missing", "Claude did not return an authorization code.", "missing code")
			return
		}
		codeCh <- code
		writeAuthSuccess(w, "Claude", "Connected to Launchdock", "Your Claude account is ready. Return to the terminal to continue.")
	})

	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()

	// Open browser
	fmt.Fprintf(os.Stderr, "\nOpening browser to authenticate...\n")
	fmt.Fprintf(os.Stderr, "If the browser doesn't open, visit:\n%s\n\n", authorizeURL)
	openBrowser(authorizeURL)

	// Wait for callback
	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("authentication timed out after 5 minutes")
	}

	slog.Info("received auth code, exchanging for tokens...")

	// Exchange code for tokens
	cred, err := exchangeCodeForTokens(code, verifier, state, redirectURI, label)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	// Save to config
	if err := SaveCredentialToConfig(cred); err != nil {
		slog.Warn("failed to save credential to config", "error", err)
	}

	return cred, nil
}

func RunOpenAIOAuthFlow(label string) (*Credential, error) {
	verifier := generateCodeVerifier()
	challenge := generateCodeChallenge(verifier)
	state := generateState()

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", openAIDefaultPort))
	if err != nil {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("start callback server: %w", err)
		}
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", port)

	authorizeURL := fmt.Sprintf(
		"%s?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&code_challenge=%s&code_challenge_method=S256&id_token_add_organizations=true&codex_cli_simplified_flow=true&state=%s",
		openAIAuthorizeURL,
		openAIClientID,
		url.QueryEscape(redirectURI),
		url.QueryEscape("openid profile email offline_access"),
		url.QueryEscape(challenge),
		url.QueryEscape(state),
	)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch")
			writeAuthError(w, http.StatusBadRequest, "OpenAI", "Sign-in could not be completed", "The callback state did not match this login request.", "state mismatch")
			return
		}
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			errCh <- fmt.Errorf("auth error: %s - %s", errMsg, r.URL.Query().Get("error_description"))
			writeAuthError(w, http.StatusUnauthorized, "OpenAI", "Authentication failed", "Launchdock could not finish connecting your OpenAI account.", errMsg)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			writeAuthError(w, http.StatusBadRequest, "OpenAI", "Authorization code missing", "OpenAI did not return an authorization code.", "missing code")
			return
		}
		codeCh <- code
		http.Redirect(w, r, "/success", http.StatusFound)
	})
	mux.HandleFunc("/success", func(w http.ResponseWriter, r *http.Request) {
		writeAuthSuccess(w, "OpenAI", "Connected to Launchdock", "Your OpenAI account is ready. Return to the terminal to continue.")
	})

	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()

	fmt.Fprintf(os.Stderr, "\nOpening browser to authenticate with OpenAI...\n")
	fmt.Fprintf(os.Stderr, "If the browser doesn't open, visit:\n%s\n\n", authorizeURL)
	openBrowser(authorizeURL)

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("authentication timed out after 5 minutes")
	}

	cred, err := exchangeOpenAICodeForTokens(code, verifier, redirectURI, label)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	if err := SaveCredentialToConfig(cred); err != nil {
		slog.Warn("failed to save credential to config", "error", err)
	}
	return cred, nil
}

func exchangeCodeForTokens(code, verifier, state, redirectURI, label string) (*Credential, error) {
	body := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"client_id":     claudeClientID,
		"code_verifier": verifier,
		"state":         state,
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", claudeOAuthEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := APIClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		Account      struct {
			UUID         string `json:"uuid"`
			EmailAddress string `json:"email_address"`
		} `json:"account"`
		Organization struct {
			UUID string `json:"uuid"`
		} `json:"organization"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if label == "" || label == "Claude Account" {
		if result.Account.EmailAddress != "" {
			label = result.Account.EmailAddress
		}
	}

	return &Credential{
		Provider:     "anthropic",
		AuthType:     AuthOAuth,
		Label:        label,
		Source:       "oauth:launchdock",
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		AccountID:    result.Account.UUID,
		Email:        result.Account.EmailAddress,
		ExpiresAt:    time.Now().Add(time.Duration(result.ExpiresIn) * time.Second),
	}, nil
}

func exchangeOpenAICodeForTokens(code, verifier, redirectURI, label string) (*Credential, error) {
	body := fmt.Sprintf(
		"grant_type=authorization_code&code=%s&redirect_uri=%s&client_id=%s&code_verifier=%s",
		url.QueryEscape(code),
		url.QueryEscape(redirectURI),
		url.QueryEscape(openAIClientID),
		url.QueryEscape(verifier),
	)
	req, err := http.NewRequest("POST", openAIOAuthEndpoint, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := APIClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	accountID := extractOpenAIAccountID(result.IDToken)
	email := extractOpenAIEmail(result.IDToken)
	return &Credential{
		Provider:     "openai",
		AuthType:     AuthOAuth,
		Label:        label,
		Source:       "oauth:launchdock",
		Kind:         "codex_chatgpt",
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		AccountID:    accountID,
		Email:        email,
		ExpiresAt:    time.Now().Add(time.Duration(result.ExpiresIn) * time.Second),
	}, nil
}

// --- Config file for multi-account persistence ---

type Config struct {
	Credentials  []ConfigCredential `json:"credentials"`
	AutoDiscover bool               `json:"auto_discover"`
}

type ConfigCredential struct {
	ID           string `json:"id,omitempty"`
	Label        string `json:"label"`
	Provider     string `json:"provider"`
	Kind         string `json:"kind,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Email        string `json:"email,omitempty"`
	Disabled     bool   `json:"disabled,omitempty"`
}

func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "launchdock", "config.json")
}

func LoadConfig() *Config {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return &Config{AutoDiscover: true}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{AutoDiscover: true}
	}
	for i := range cfg.Credentials {
		if cfg.Credentials[i].ID == "" {
			cfg.Credentials[i].ID = GenerateCredentialID()
		}
	}
	return &cfg
}

func SaveConfig(cfg *Config) error {
	dir := filepath.Dir(ConfigPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0600)
}

func SaveCredentialToConfig(cred *Credential) error {
	cfg := LoadConfig()
	provider := normalizeProvider(cred.Provider)
	match := -1
	for i := range cfg.Credentials {
		cc := cfg.Credentials[i]
		if normalizeProvider(cc.Provider) != provider {
			continue
		}
		if cred.AccountID != "" && cc.AccountID != "" && cc.AccountID == cred.AccountID {
			match = i
			break
		}
		if match == -1 && cred.Email != "" && cc.Email != "" && strings.EqualFold(cc.Email, cred.Email) {
			match = i
		}
	}
	if match >= 0 {
		cfg.Credentials[match].Label = cred.Label
		cfg.Credentials[match].Provider = provider
		cfg.Credentials[match].Kind = cred.Kind
		cfg.Credentials[match].AccessToken = cred.AccessToken
		if !cred.ExpiresAt.IsZero() {
			cfg.Credentials[match].ExpiresAt = cred.ExpiresAt.Format(time.RFC3339)
		}
		cfg.Credentials[match].RefreshToken = cred.RefreshToken
		cfg.Credentials[match].APIKey = cred.APIKey
		cfg.Credentials[match].AccountID = cred.AccountID
		cfg.Credentials[match].Email = cred.Email
		cfg.Credentials[match].Disabled = false
		if cfg.Credentials[match].ID == "" {
			cfg.Credentials[match].ID = GenerateCredentialID()
		}
	} else {
		cfg.Credentials = append(cfg.Credentials, ConfigCredential{
			ID:           GenerateCredentialID(),
			Label:        cred.Label,
			Provider:     provider,
			Kind:         cred.Kind,
			AccessToken:  cred.AccessToken,
			ExpiresAt:    FormatExpiresAt(cred.ExpiresAt),
			RefreshToken: cred.RefreshToken,
			APIKey:       cred.APIKey,
			AccountID:    cred.AccountID,
			Email:        cred.Email,
		})
	}
	return SaveConfig(cfg)
}

func SaveAPIKeyToConfig(provider, label, apiKey string) error {
	provider = normalizeProvider(provider)
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("api key is empty")
	}
	if label == "" {
		label = strings.ToUpper(provider) + " API key"
	}
	cfg := LoadConfig()
	cfg.Credentials = append(cfg.Credentials, ConfigCredential{
		ID:       GenerateCredentialID(),
		Label:    label,
		Provider: provider,
		APIKey:   apiKey,
	})
	return SaveConfig(cfg)
}

func GenerateCredentialID() string {
	return "cred_" + generateState()
}

func RemoveConfigCredential(id string) error {
	cfg := LoadConfig()
	var filtered []ConfigCredential
	removed := false
	for _, cc := range cfg.Credentials {
		if cc.ID == id {
			removed = true
			continue
		}
		filtered = append(filtered, cc)
	}
	if !removed {
		return fmt.Errorf("credential not found")
	}
	cfg.Credentials = filtered
	return SaveConfig(cfg)
}

func ToggleConfigCredentialDisabled(id string) error {
	cfg := LoadConfig()
	for i := range cfg.Credentials {
		if cfg.Credentials[i].ID == id {
			cfg.Credentials[i].Disabled = !cfg.Credentials[i].Disabled
			return SaveConfig(cfg)
		}
	}
	return fmt.Errorf("credential not found")
}

func SetConfigCredentialDisabled(id string, disabled bool) error {
	cfg := LoadConfig()
	for i := range cfg.Credentials {
		if cfg.Credentials[i].ID == id {
			cfg.Credentials[i].Disabled = disabled
			return SaveConfig(cfg)
		}
	}
	return fmt.Errorf("credential not found")
}

func PersistManagedCredentialState(id, refreshToken, accountID, email string) error {
	cfg := LoadConfig()
	for i := range cfg.Credentials {
		if cfg.Credentials[i].ID != id {
			continue
		}
		if refreshToken != "" {
			cfg.Credentials[i].RefreshToken = refreshToken
		}
		if accountID != "" {
			cfg.Credentials[i].AccountID = accountID
		}
		if email != "" {
			cfg.Credentials[i].Email = email
		}
		return SaveConfig(cfg)
	}
	return fmt.Errorf("managed credential not found")
}

// LoadFromConfig loads credentials from ~/.config/launchdock/config.json
func LoadFromConfig() []Credential {
	cfg := LoadConfig()
	var creds []Credential
	for _, cc := range cfg.Credentials {
		if cc.Disabled {
			continue
		}
		provider := normalizeProvider(cc.Provider)
		if cc.APIKey != "" {
			if provider == "anthropic" {
				continue
			}
			creds = append(creds, Credential{
				ID:       cc.ID,
				Provider: provider,
				AuthType: AuthAPIKey,
				Label:    cc.Label,
				Source:   "config:" + ConfigPath(),
				Kind:     cc.Kind,
				Managed:  true,
				Email:    cc.Email,
				APIKey:   cc.APIKey,
			})
		} else if cc.RefreshToken != "" {
			if cc.AccessToken != "" {
				exp := parseConfigExpiresAt(cc.ExpiresAt)
				cred := Credential{
					ID:           cc.ID,
					Provider:     provider,
					AuthType:     AuthOAuth,
					Label:        cc.Label,
					Source:       "config:" + ConfigPath(),
					Kind:         cc.Kind,
					Managed:      true,
					AccessToken:  cc.AccessToken,
					RefreshToken: cc.RefreshToken,
					AccountID:    cc.AccountID,
					Email:        cc.Email,
					ExpiresAt:    exp,
				}
				if !cred.IsExpired() {
					creds = append(creds, cred)
					continue
				}
				// Token is expired or near expiry; fall through to refresh path.
			}
			// Refresh only when needed.
			var at, rt string
			var exp time.Time
			var err error

			if provider == "anthropic" {
				at, rt, exp, err = RefreshClaudeOAuth(cc.RefreshToken)
			} else if provider == "openai" {
				at, rt, exp, err = RefreshOpenAIOAuth(cc.RefreshToken)
			}

			if err != nil {
				slog.Warn("config credential refresh failed", "label", cc.Label, "error", err)
				if provider == "openai" && cc.ID != "" && IsTerminalOpenAIRefreshError(err) {
					if derr := SetConfigCredentialDisabled(cc.ID, true); derr != nil {
						slog.Warn("disable stale OpenAI credential failed", "label", cc.Label, "id", cc.ID, "error", derr)
					} else {
						slog.Warn("disabled stale OpenAI credential", "label", cc.Label, "id", cc.ID)
					}
					continue
				}
				// Store with refresh token anyway — will retry later
				creds = append(creds, Credential{
					ID:           cc.ID,
					Provider:     provider,
					AuthType:     AuthOAuth,
					Label:        cc.Label,
					Source:       "config:" + ConfigPath(),
					Kind:         cc.Kind,
					Managed:      true,
					RefreshToken: cc.RefreshToken,
					AccountID:    cc.AccountID,
					Email:        cc.Email,
				})
			} else {
				creds = append(creds, Credential{
					ID:           cc.ID,
					Provider:     provider,
					AuthType:     AuthOAuth,
					Label:        cc.Label,
					Source:       "config:" + ConfigPath(),
					Kind:         cc.Kind,
					Managed:      true,
					AccessToken:  at,
					RefreshToken: rt,
					AccountID:    cc.AccountID,
					Email:        cc.Email,
					ExpiresAt:    exp,
				})
			}
		}
	}
	return creds
}

func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude", "anthropic":
		return "anthropic"
	case "codex", "openai":
		return "openai"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func IsTerminalOpenAIRefreshError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "refresh_token_reused") ||
		strings.Contains(msg, "already been used to generate a new access token")
}

func parseConfigExpiresAt(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

func FormatExpiresAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// --- PKCE helpers ---

func generateCodeVerifier() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func generateState() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		cmd = exec.Command("open", url)
	}
	cmd.Start()
}

func extractOpenAIAccountID(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth map[string]any `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	if v, ok := claims.Auth["chatgpt_account_id"].(string); ok {
		return v
	}
	if v, ok := claims.Auth["account_id"].(string); ok {
		return v
	}
	return ""
}

func extractOpenAIEmail(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	return claims.Email
}
