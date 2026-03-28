package main

import (
	"fmt"
	"log/slog"
	"os"
)

type LaunchModel struct {
	ID       string
	Provider string
}

type LaunchConfig struct {
	BaseURL  string
	RawURL   string
	APIKey   string
	Models   []LaunchModel
	HasCreds bool
}

func (c LaunchConfig) ModelIDs() []string {
	var ids []string
	for _, m := range c.Models {
		ids = append(ids, m.ID)
	}
	return ids
}

func (c LaunchConfig) FilterModels(provider string) []LaunchModel {
	if provider == "" {
		return append([]LaunchModel(nil), c.Models...)
	}
	var out []LaunchModel
	for _, m := range c.Models {
		if m.Provider == provider {
			out = append(out, m)
		}
	}
	return out
}

func (c LaunchConfig) HasProvider(provider string) bool {
	return len(c.FilterModels(provider)) > 0
}

func providerDisplayName(provider string) string {
	switch provider {
	case "anthropic":
		return "Anthropic"
	case "openai":
		return "OpenAI"
	default:
		return provider
	}
}

func discoveredProviders(cfg LaunchConfig) []string {
	seen := map[string]bool{}
	var providers []string
	for _, m := range cfg.Models {
		if !seen[m.Provider] {
			seen[m.Provider] = true
			providers = append(providers, providerDisplayName(m.Provider))
		}
	}
	if len(providers) == 0 {
		return []string{"none"}
	}
	return providers
}

func resolveLaunchConfig() LaunchConfig {
	port := "8090"
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	raw := fmt.Sprintf("http://localhost:%s", port)
	cfg := LaunchConfig{
		BaseURL: raw + "/v1",
		RawURL:  raw,
		APIKey:  "launchdock",
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	})))

	creds := LoadAllCredentials()
	cfg.HasCreds = len(creds) > 0
	if !cfg.HasCreds {
		return cfg
	}

	pool := NewPool(creds)
	for _, m := range fetchAllModels(pool, &AnthropicProvider{}) {
		id, _ := m["id"].(string)
		owner, _ := m["owned_by"].(string)
		if id != "" {
			cfg.Models = append(cfg.Models, LaunchModel{ID: id, Provider: owner})
		}
	}
	return cfg
}
