package main

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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// OAuthFlow implements the OAuth 2.0 Authorization Code + PKCE flow
// for adding Claude accounts to llm-mux.

const (
	claudeAuthorizeURL = "https://claude.ai/oauth/authorize"
)

// RunOAuthFlow opens a browser for the user to authorize, captures the callback,
// exchanges the code for tokens, and saves the credential.
func RunOAuthFlow(label string) (*Credential, error) {
	// Generate PKCE
	verifier := generateCodeVerifier()
	challenge := generateCodeChallenge(verifier)
	state := generateState()

	// Start local callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)
	scopes := claudeDefaultScopes

	// Build authorize URL
	authorizeURL := fmt.Sprintf(
		"%s?code=true&client_id=%s&response_type=code&redirect_uri=%s&scope=%s&code_challenge=%s&code_challenge_method=S256&state=%s",
		claudeAuthorizeURL,
		claudeClientID,
		redirectURI,
		scopes,
		challenge,
		state,
	)

	// Channel to receive the auth code
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			errCh <- fmt.Errorf("state mismatch")
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			errCh <- fmt.Errorf("auth error: %s - %s", errMsg, r.URL.Query().Get("error_description"))
			fmt.Fprintf(w, "<html><body><h1>Authentication failed</h1><p>%s</p><p>You can close this tab.</p></body></html>", errMsg)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			http.Error(w, "No code", http.StatusBadRequest)
			return
		}
		codeCh <- code
		fmt.Fprint(w, "<html><body><h1>Authenticated!</h1><p>You can close this tab and return to the terminal.</p></body></html>")
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
	if err := saveCredentialToConfig(cred); err != nil {
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

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &Credential{
		Provider:     "anthropic",
		AuthType:     AuthOAuth,
		Label:        label,
		Source:       "oauth:llm-mux",
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(result.ExpiresIn) * time.Second),
	}, nil
}

// --- Config file for multi-account persistence ---

type Config struct {
	Credentials  []ConfigCredential `json:"credentials"`
	AutoDiscover bool               `json:"auto_discover"`
}

type ConfigCredential struct {
	Label        string `json:"label"`
	Provider     string `json:"provider"`
	RefreshToken string `json:"refresh_token,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "llm-mux", "config.json")
}

func loadConfig() *Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return &Config{AutoDiscover: true}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{AutoDiscover: true}
	}
	return &cfg
}

func saveConfig(cfg *Config) error {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0600)
}

func saveCredentialToConfig(cred *Credential) error {
	cfg := loadConfig()
	cfg.Credentials = append(cfg.Credentials, ConfigCredential{
		Label:        cred.Label,
		Provider:     cred.Provider,
		RefreshToken: cred.RefreshToken,
	})
	return saveConfig(cfg)
}

// LoadFromConfig loads credentials from ~/.config/llm-mux/config.json
func LoadFromConfig() []Credential {
	cfg := loadConfig()
	var creds []Credential
	for _, cc := range cfg.Credentials {
		if cc.APIKey != "" {
			creds = append(creds, Credential{
				Provider: cc.Provider,
				AuthType: AuthAPIKey,
				Label:    cc.Label,
				Source:   "config:" + configPath(),
				APIKey:   cc.APIKey,
			})
		} else if cc.RefreshToken != "" {
			// Try refresh immediately to get access token
			var at, rt string
			var exp time.Time
			var err error

			if cc.Provider == "anthropic" {
				at, rt, exp, err = RefreshClaudeOAuth(cc.RefreshToken)
			} else if cc.Provider == "openai" {
				at, rt, exp, err = RefreshOpenAIOAuth(cc.RefreshToken)
			}

			if err != nil {
				slog.Warn("config credential refresh failed", "label", cc.Label, "error", err)
				// Store with refresh token anyway — will retry later
				creds = append(creds, Credential{
					Provider:     cc.Provider,
					AuthType:     AuthOAuth,
					Label:        cc.Label,
					Source:       "config:" + configPath(),
					RefreshToken: cc.RefreshToken,
				})
			} else {
				creds = append(creds, Credential{
					Provider:     cc.Provider,
					AuthType:     AuthOAuth,
					Label:        cc.Label,
					Source:       "config:" + configPath(),
					AccessToken:  at,
					RefreshToken: rt,
					ExpiresAt:    exp,
				})
			}
		}
	}
	return creds
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
	b := make([]byte, 16)
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
