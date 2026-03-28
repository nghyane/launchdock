package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type CredentialView struct {
	ID              string
	Label           string
	Email           string
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

	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Managed != views[j].Managed {
			return views[i].Managed
		}
		if views[i].Provider != views[j].Provider {
			return views[i].Provider < views[j].Provider
		}
		return views[i].Label < views[j].Label
	})

	enrichViewEmails(views)

	return views
}

func enrichViewEmails(views []CredentialView) {
	byAccount := map[string]string{}
	for _, v := range views {
		if v.AccountID != "" && v.Email != "" {
			byAccount[v.Provider+":"+v.AccountID] = v.Email
		}
	}
	for i := range views {
		if views[i].Email != "" || views[i].AccountID == "" {
			continue
		}
		if email := byAccount[views[i].Provider+":"+views[i].AccountID]; email != "" {
			views[i].Email = email
		}
	}
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
	if sourceKind == "claude" {
		profile := LoadClaudeProfile()
		if cred.Email == "" {
			cred.Email = profile.Email
		}
		if cred.Label == "Claude Keychain" && profile.DisplayName != "" {
			cred.Label = profile.DisplayName
		} else if cred.Label == "Claude Keychain" && profile.SubscriptionType != "" {
			cred.Label = humanizeClaudeSubscription(profile.SubscriptionType)
		}
	}
	return CredentialView{
		ID:              cred.Source,
		Label:           cred.Label,
		Email:           cred.Email,
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

func humanizeClaudeSubscription(raw string) string {
	raw = strings.ToLower(raw)
	switch {
	case strings.Contains(raw, "max"):
		return "Claude Max"
	case strings.Contains(raw, "pro"):
		return "Claude Pro"
	case strings.Contains(raw, "team"):
		return "Claude Team"
	default:
		return "Claude"
	}
}

func managedCredentialView(cc ConfigCredential) CredentialView {
	v := CredentialView{
		ID:              cc.ID,
		Label:           cc.Label,
		Email:           cc.Email,
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
	switch provider {
	case "anthropic":
		return "Claude"
	case "openai":
		return "OpenAI"
	default:
		return providerDisplayName(provider)
	}
}

func authListLine(i int, v CredentialView) string {
	line := fmt.Sprintf("%d. %-24s %-9s %-8s %s", i+1, truncateAuth(authDisplayName(v), 24), authStatusLabel(v), authProviderLabel(v.Provider), authSourceLabel(v))
	if v.Managed {
		line += fmt.Sprintf(" [id: %s]", v.ID)
	}
	return line
}

func authDisplayName(v CredentialView) string {
	if v.Email != "" {
		return v.Email
	}
	if v.AuthType == AuthAPIKey {
		return "API key"
	}
	return v.Label
}

func authSourceLabel(v CredentialView) string {
	switch v.SourceKind {
	case "managed":
		return "Launchdock"
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "env":
		return "Environment"
	default:
		return v.SourceKind
	}
}

func truncateAuth(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
