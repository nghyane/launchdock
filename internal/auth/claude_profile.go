package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ClaudeProfile struct {
	Email            string
	AccountID        string
	OrganizationID   string
	DisplayName      string
	OrganizationName string
	SubscriptionType string
}

func LoadClaudeProfile() ClaudeProfile {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ClaudeProfile{}
	}
	patterns := []string{
		filepath.Join(home, ".claude", "backups", ".claude.json.backup.*"),
		filepath.Join(home, ".claude", "telemetry", "*.json"),
		filepath.Join(home, "Library", "Application Support", "Claude", "local-agent-mode-sessions", "*", "*", "local_*.json"),
		filepath.Join(home, "Library", "Application Support", "Claude", "local-agent-mode-sessions", "*", "*", "local_*", ".claude", ".claude.json"),
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	var files []candidate
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && !info.IsDir() {
				files = append(files, candidate{path: match, modTime: info.ModTime()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })

	best := ClaudeProfile{}
	for _, file := range files {
		profile := parseClaudeProfileFile(file.path)
		if best.Email == "" && profile.Email != "" {
			best.Email = profile.Email
		}
		if best.AccountID == "" && profile.AccountID != "" {
			best.AccountID = profile.AccountID
		}
		if best.OrganizationID == "" && profile.OrganizationID != "" {
			best.OrganizationID = profile.OrganizationID
		}
		if best.DisplayName == "" && profile.DisplayName != "" {
			best.DisplayName = profile.DisplayName
		}
		if best.OrganizationName == "" && profile.OrganizationName != "" {
			best.OrganizationName = profile.OrganizationName
		}
		if best.SubscriptionType == "" && profile.SubscriptionType != "" {
			best.SubscriptionType = profile.SubscriptionType
		}
		if best.Email != "" && best.AccountID != "" {
			break
		}
	}
	return best
}

func parseClaudeProfileFile(path string) ClaudeProfile {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClaudeProfile{}
	}
	values := map[string]string{}
	var payload any
	if err := json.Unmarshal(data, &payload); err == nil {
		collectProfileStrings(payload, values)
	} else {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var item any
			if json.Unmarshal([]byte(line), &item) == nil {
				collectProfileStrings(item, values)
			}
		}
	}
	return ClaudeProfile{
		Email:            firstNonEmpty(values["emailaddress"], values["email"]),
		AccountID:        firstNonEmpty(values["account_uuid"], values["accountid"]),
		OrganizationID:   firstNonEmpty(values["organization_uuid"], values["organizationid"]),
		DisplayName:      firstNonEmpty(values["displayname"], values["accountname"]),
		OrganizationName: values["organizationname"],
		SubscriptionType: firstNonEmpty(values["subscriptiontype"], values["billingtype"], values["ratelimittier"]),
	}
}

func collectProfileStrings(v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			key := strings.ToLower(k)
			if s, ok := child.(string); ok && s != "" {
				if _, exists := out[key]; !exists {
					out[key] = s
				}
			}
			collectProfileStrings(child, out)
		}
	case []any:
		for _, child := range x {
			collectProfileStrings(child, out)
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
