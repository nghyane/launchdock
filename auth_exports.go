package launchdock

import (
	"time"

	authpkg "github.com/nghiahoang/launchdock/internal/auth"
)

type AuthType = authpkg.AuthType

const (
	AuthOAuth  = authpkg.AuthOAuth
	AuthAPIKey = authpkg.AuthAPIKey
)

type Credential = authpkg.Credential
type ClaudeProfile = authpkg.ClaudeProfile
type Config = authpkg.Config
type ConfigCredential = authpkg.ConfigCredential

func LoadAllCredentials() []Credential               { return authpkg.LoadAllCredentials() }
func LoadFromFile(path string) ([]Credential, error) { return authpkg.LoadFromFile(path) }
func LoadFromKeychain() ([]Credential, error)        { return authpkg.LoadFromKeychain() }
func RefreshClaudeOAuth(refreshToken string) (string, string, time.Time, error) {
	return authpkg.RefreshClaudeOAuth(refreshToken)
}
func RefreshOpenAIOAuth(refreshToken string) (string, string, time.Time, error) {
	return authpkg.RefreshOpenAIOAuth(refreshToken)
}
func LoadClaudeProfile() ClaudeProfile                     { return authpkg.LoadClaudeProfile() }
func RunOAuthFlow(label string) (*Credential, error)       { return authpkg.RunOAuthFlow(label) }
func RunOpenAIOAuthFlow(label string) (*Credential, error) { return authpkg.RunOpenAIOAuthFlow(label) }
func configPath() string                                   { return authpkg.ConfigPath() }
func loadConfig() *Config                                  { return authpkg.LoadConfig() }
func saveConfig(cfg *Config) error                         { return authpkg.SaveConfig(cfg) }
func saveCredentialToConfig(cred *Credential) error        { return authpkg.SaveCredentialToConfig(cred) }
func saveAPIKeyToConfig(provider, label, apiKey string) error {
	return authpkg.SaveAPIKeyToConfig(provider, label, apiKey)
}
func generateCredentialID() string           { return authpkg.GenerateCredentialID() }
func removeConfigCredential(id string) error { return authpkg.RemoveConfigCredential(id) }
func toggleConfigCredentialDisabled(id string) error {
	return authpkg.ToggleConfigCredentialDisabled(id)
}
func persistManagedCredentialState(id, refreshToken, accountID, email string) error {
	return authpkg.PersistManagedCredentialState(id, refreshToken, accountID, email)
}
func RefreshViaCLI(prompt string) error { return authpkg.RefreshViaCLI(prompt) }
