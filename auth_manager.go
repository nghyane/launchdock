package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type CredentialView struct {
	ID              string
	Label           string
	Provider        string
	AuthType        AuthType
	Source          string
	SourceKind      string
	Managed         bool
	Disabled        bool
	Status          string
	StatusMessage   string
	AccountID       string
	CompatibleTools []string
}

func LoadCredentialViews() []CredentialView {
	var views []CredentialView

	if creds, err := LoadFromKeychain(); err == nil {
		for _, cred := range creds {
			views = append(views, externalCredentialView(cred, "claude"))
		}
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		if creds, err := LoadFromFile(filepath.Join(home, ".codex", "auth.json")); err == nil {
			for _, cred := range creds {
				views = append(views, externalCredentialView(cred, "codex"))
			}
		}
	}

	for _, cc := range loadConfig().Credentials {
		views = append(views, managedCredentialView(cc))
	}

	for _, src := range []struct {
		key      string
		provider string
	}{
		{"ANTHROPIC_API_KEY", "anthropic"},
		{"OPENAI_API_KEY", "openai"},
		{"GEMINI_API_KEY", "gemini"},
	} {
		if cred, err := LoadFromEnv(src.key, src.provider); err == nil {
			views = append(views, externalCredentialView(*cred, "env"))
		}
	}

	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Managed != views[j].Managed {
			return views[i].Managed
		}
		if views[i].Provider != views[j].Provider {
			return views[i].Provider < views[j].Provider
		}
		return views[i].Label < views[j].Label
	})

	return views
}

func externalCredentialView(cred Credential, sourceKind string) CredentialView {
	status := "healthy"
	message := "available"
	if cred.IsExpired() {
		status = "expired"
		message = "access token expired"
	}
	if cred.AuthType == AuthAPIKey {
		message = "api key available"
	}
	return CredentialView{
		ID:              cred.Source,
		Label:           cred.Label,
		Provider:        cred.Provider,
		AuthType:        cred.AuthType,
		Source:          cred.Source,
		SourceKind:      sourceKind,
		Status:          status,
		StatusMessage:   message,
		AccountID:       cred.AccountID,
		CompatibleTools: compatibleToolsForProvider(cred.Provider),
	}
}

func managedCredentialView(cc ConfigCredential) CredentialView {
	v := CredentialView{
		ID:              cc.ID,
		Label:           cc.Label,
		Provider:        cc.Provider,
		Source:          "config:" + configPath(),
		SourceKind:      "managed",
		Managed:         true,
		Disabled:        cc.Disabled,
		AccountID:       cc.AccountID,
		CompatibleTools: compatibleToolsForProvider(cc.Provider),
	}
	if cc.APIKey != "" {
		v.AuthType = AuthAPIKey
		if cc.Disabled {
			v.Status = "disabled"
			v.StatusMessage = "disabled in launchdock"
		} else {
			v.Status = "healthy"
			v.StatusMessage = "managed api key"
		}
		return v
	}
	v.AuthType = AuthOAuth
	if cc.Disabled {
		v.Status = "disabled"
		v.StatusMessage = "disabled in launchdock"
		return v
	}
	if cc.RefreshToken == "" {
		v.Status = "invalid"
		v.StatusMessage = "missing refresh token"
		return v
	}
	var err error
	if cc.Provider == "anthropic" {
		_, _, _, err = RefreshClaudeOAuth(cc.RefreshToken)
	} else if cc.Provider == "openai" {
		_, _, _, err = RefreshOpenAIOAuth(cc.RefreshToken)
	}
	if err != nil {
		v.Status = "stale"
		v.StatusMessage = err.Error()
		return v
	}
	v.Status = "healthy"
	v.StatusMessage = "refreshable"
	return v
}

func compatibleToolsForProvider(provider string) []string {
	switch provider {
	case "anthropic":
		return []string{"claude-code", "opencode", "droid", "pi"}
	case "openai":
		return []string{"codex", "opencode", "droid", "pi"}
	default:
		return []string{"opencode", "droid", "pi"}
	}
}

func authStatusLabel(v CredentialView) string {
	switch {
	case v.Disabled:
		return "disabled"
	case v.Status == "healthy":
		return "ready"
	case v.Status == "stale":
		return "relogin"
	case v.Status == "expired":
		return "expired"
	case v.Status == "invalid":
		return "invalid"
	default:
		return v.Status
	}
}

func authProviderLabel(provider string) string {
	return providerDisplayName(provider)
}

func authListLine(i int, v CredentialView) string {
	line := fmt.Sprintf("%d. [%s] %s (%s/%s) — %s", i+1, authStatusLabel(v), v.Label, v.Provider, v.AuthType, v.Source)
	if v.Managed {
		line += fmt.Sprintf(" [id: %s]", v.ID)
	}
	return line
}
