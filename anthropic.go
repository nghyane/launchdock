package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AnthropicProvider handles Claude API communication.
type AnthropicProvider struct {
	SystemPrompt string // injected system prompt for OAuth accounts
}

func (p *AnthropicProvider) Match(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "claude")
}

func (p *AnthropicProvider) ProviderName() string { return "anthropic" }
func (p *AnthropicProvider) BaseURL() string      { return "https://api.anthropic.com" }

func (p *AnthropicProvider) Prepare(req *http.Request, cred *Credential) {
	p.PrepareWithModel(req, cred, "")
}

func (p *AnthropicProvider) PrepareWithModel(req *http.Request, cred *Credential, model string) {
	switch cred.AuthType {
	case AuthOAuth:
		req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
		p.applyOAuthHeaders(req, model)
	case AuthAPIKey:
		req.Header.Set("x-api-key", cred.APIKey)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
}

func (p *AnthropicProvider) TranslateRequest(chatReq *ChatRequest) ([]byte, string, error) {
	claudeReq, err := ChatToClaudeRequest(chatReq)
	if err != nil {
		return nil, "", err
	}

	body, err := json.Marshal(claudeReq)
	if err != nil {
		return nil, "", err
	}

	return body, "/v1/messages", nil
}

// --- OAuth-specific headers ---

var baseBetas = []string{
	"claude-code-20250219",
	"oauth-2025-04-20",
	"interleaved-thinking-2025-05-14",
	"prompt-caching-scope-2026-01-05",
	"context-management-2025-06-27",
}

func (p *AnthropicProvider) applyOAuthHeaders(req *http.Request, model string) {
	// Beta headers
	req.Header.Set("anthropic-beta", strings.Join(baseBetas, ","))

	// Required identity headers
	req.Header.Set("x-app", "cli")
	req.Header.Set("User-Agent", "claude-cli/2.1.80 (external, cli)")

	// Billing header (mirrors opencode-claude-auth)
	req.Header.Set("x-anthropic-billing-header",
		fmt.Sprintf("cc_version=2.1.80.%s; cc_entrypoint=cli; cch=00000;", model))
}

// PrefixTools adds "mcp_" prefix to all tool names in the request body.
// Required for Claude OAuth accounts.
func PrefixTools(body []byte, prefix string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, err
	}

	// Prefix tool definitions
	if tools, ok := req["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if name, ok := tm["name"].(string); ok && !strings.HasPrefix(name, prefix) {
					tm["name"] = prefix + name
				}
			}
		}
	}

	// Prefix tool_use blocks in messages
	if msgs, ok := req["messages"].([]any); ok {
		for _, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			content, ok := mm["content"].([]any)
			if !ok {
				continue
			}
			for _, c := range content {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				switch cm["type"] {
				case "tool_use":
					if name, ok := cm["name"].(string); ok && !strings.HasPrefix(name, prefix) {
						cm["name"] = prefix + name
					}
				}
			}
		}
	}

	return json.Marshal(req)
}

// StripToolPrefix removes prefix from tool names in response data.
func StripToolPrefix(data []byte, prefix string) []byte {
	// Simple string replacement for SSE chunks
	return []byte(strings.ReplaceAll(string(data), fmt.Sprintf(`"name":"%s`, prefix), `"name":"`))
}

// ensureOAuthRequirements ensures the request has the system prompt identity
// and at least one tool (required by Claude OAuth).
func ensureOAuthRequirements(body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, err
	}

	// Ensure system prompt starts with Claude Code identity
	const identity = "You are Claude Code, Anthropic's official CLI for Claude."
	system, _ := req["system"].(string)
	if !strings.Contains(system, identity) {
		if system != "" {
			req["system"] = []map[string]string{
				{"type": "text", "text": identity},
				{"type": "text", "text": system},
			}
		} else {
			req["system"] = []map[string]string{
				{"type": "text", "text": identity},
			}
		}
	}

	// Ensure at least one tool exists (OAuth requires tools)
	tools, _ := req["tools"].([]any)
	if len(tools) == 0 {
		req["tools"] = []map[string]any{{
			"name":        "mcp_noop",
			"description": "No-op placeholder tool",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}}
	}

	return json.Marshal(req)
}

// InjectSystemPrompt prepends a system prompt to the Claude request.
func InjectSystemPrompt(body []byte, prompt string) ([]byte, error) {
	if prompt == "" {
		return body, nil
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, err
	}

	existing, _ := req["system"].(string)
	if existing != "" {
		req["system"] = prompt + "\n\n" + existing
	} else {
		req["system"] = prompt
	}

	return json.Marshal(req)
}
