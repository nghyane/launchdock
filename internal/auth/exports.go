package auth

func ConfigPath() string                            { return configPath() }
func LoadConfig() *Config                           { return loadConfig() }
func SaveConfig(cfg *Config) error                  { return saveConfig(cfg) }
func SaveCredentialToConfig(cred *Credential) error { return saveCredentialToConfig(cred) }
func SaveAPIKeyToConfig(provider, label, apiKey string) error {
	return saveAPIKeyToConfig(provider, label, apiKey)
}
func GenerateCredentialID() string                   { return generateCredentialID() }
func RemoveConfigCredential(id string) error         { return removeConfigCredential(id) }
func ToggleConfigCredentialDisabled(id string) error { return toggleConfigCredentialDisabled(id) }
func PersistManagedCredentialState(id, refreshToken, accountID, email string) error {
	return persistManagedCredentialState(id, refreshToken, accountID, email)
}
