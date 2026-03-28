package main

import (
	"net/http"
	"strings"
)

// Provider handles upstream communication for a specific backend.
type Provider interface {
	// Match returns true if this provider handles the given model name.
	Match(model string) bool

	// Prepare adds auth headers and provider-specific modifications to the upstream request.
	Prepare(req *http.Request, cred *Credential)

	// ProviderName returns the credential provider name (e.g. "anthropic", "openai").
	ProviderName() string

	// BaseURL returns the API base URL.
	BaseURL() string

	// TranslateRequest converts a ChatRequest into the provider's native request body.
	// Returns the body bytes and target URL path.
	TranslateRequest(chatReq *ChatRequest) (body []byte, path string, err error)
}

// RouteProvider selects the appropriate provider based on model name.
func RouteProvider(providers []Provider, model string) Provider {
	for _, p := range providers {
		if p.Match(model) {
			return p
		}
	}
	return nil
}

// ModelToProvider maps model name prefix to provider name.
func ModelToProvider(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "gpt-"),
		strings.HasPrefix(m, "o1"),
		strings.HasPrefix(m, "o3"),
		strings.HasPrefix(m, "o4"),
		strings.HasPrefix(m, "chatgpt"):
		return "openai"
	case strings.HasPrefix(m, "gemini"):
		return "gemini"
	default:
		return "anthropic" // default to anthropic
	}
}
