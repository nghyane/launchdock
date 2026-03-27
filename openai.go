package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// OpenAIProvider handles OpenAI API communication (mostly passthrough).
type OpenAIProvider struct{}

func (p *OpenAIProvider) Match(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-") ||
		strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4") ||
		strings.HasPrefix(m, "chatgpt")
}

func (p *OpenAIProvider) ProviderName() string { return "openai" }
func (p *OpenAIProvider) BaseURL() string      { return "https://api.openai.com" }

func (p *OpenAIProvider) Prepare(req *http.Request, cred *Credential) {
	switch cred.AuthType {
	case AuthOAuth:
		req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
		if cred.AccountID != "" {
			req.Header.Set("chatgpt-account-id", cred.AccountID)
		}
	case AuthAPIKey:
		req.Header.Set("Authorization", "Bearer "+cred.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
}

func (p *OpenAIProvider) TranslateRequest(chatReq *ChatRequest) ([]byte, string, error) {
	// OpenAI Chat Completions is passthrough — no translation needed.
	// We re-encode to ensure clean JSON.
	body, err := json.Marshal(chatReq)
	return body, "/v1/chat/completions", err
}
