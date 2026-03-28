package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AnthropicProvider handles Claude API communication.
// OAuth mode matches Claude Code v2.1.81 binary fingerprint exactly.
type AnthropicProvider struct{}

const cliVersion = "2.1.81"

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
		applyOAuthHeaders(req, model)
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

// --- OAuth headers — match Claude Code v2.1.81 exactly ---

// buildBetas replicates hPq/oN from Claude Code binary.
// Logic: model-dependent beta selection.
func buildBetas(model string) []string {
	m := strings.ToLower(model)
	isHaiku := strings.Contains(m, "haiku")

	betas := []string{}

	// claude-code identity beta (all non-haiku models)
	if !isHaiku {
		betas = append(betas, "claude-code-20250219")
	}

	// OAuth required
	betas = append(betas, "oauth-2025-04-20")

	// Interleaved thinking (non-haiku, non-claude-3)
	if !isHaiku && !strings.Contains(m, "claude-3-") {
		betas = append(betas, "interleaved-thinking-2025-05-14")
	}

	// Prompt caching
	betas = append(betas, "prompt-caching-scope-2026-01-05")

	return betas
}

// computeCCH replicates PL6/w2q from Claude Code binary.
// Hash = sha256(salt + chars_at_positions_4_7_20_of_first_user_message + version)[:3]
func computeCCH(firstUserContent string) string {
	const salt = "59cf53e54c78"
	chars := ""
	positions := []int{4, 7, 20}
	for _, pos := range positions {
		if pos < len(firstUserContent) {
			chars += string(firstUserContent[pos])
		} else {
			chars += "0"
		}
	}
	input := salt + chars + cliVersion
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])[:3]
}

func applyOAuthHeaders(req *http.Request, model string) {
	// Beta headers — dynamic per model
	betas := buildBetas(model)
	req.Header.Set("anthropic-beta", strings.Join(betas, ","))

	// User-Agent — exact match Claude Code v2.1.81
	req.Header.Set("User-Agent", "claude-cli/"+cliVersion+" (external, cli)")

	// x-app header
	req.Header.Set("x-app", "cli")

	// Billing/attribution header — exact format from DDT() in binary
	req.Header.Set("x-anthropic-billing-header",
		fmt.Sprintf("cc_version=%s.%s; cc_entrypoint=cli; cch=00000;", cliVersion, model))
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
				if cm["type"] == "tool_use" {
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
	return []byte(strings.ReplaceAll(string(data), fmt.Sprintf(`"name":"%s`, prefix), `"name":"`))
}

// ensureOAuthRequirements ensures the request has system prompt identity
// and at least one tool (required by Claude OAuth).
func ensureOAuthRequirements(body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body, err
	}

	// System prompt — exact string from Claude Code binary
	const identity = "You are Claude Code, Anthropic's official CLI for Claude."

	// Check if system already contains identity
	hasIdentity := false
	switch s := req["system"].(type) {
	case string:
		hasIdentity = strings.Contains(s, identity)
	case []any:
		for _, item := range s {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok && strings.Contains(text, identity) {
					hasIdentity = true
					break
				}
			}
		}
	}

	if !hasIdentity {
		// Prepend identity as first system block (array format like Claude Code does)
		existing := req["system"]
		var systemBlocks []map[string]string
		systemBlocks = append(systemBlocks, map[string]string{"type": "text", "text": identity})

		switch s := existing.(type) {
		case string:
			if s != "" {
				systemBlocks = append(systemBlocks, map[string]string{"type": "text", "text": s})
			}
		case []any:
			for _, item := range s {
				if m, ok := item.(map[string]any); ok {
					text, _ := m["text"].(string)
					typ, _ := m["type"].(string)
					if typ == "" {
						typ = "text"
					}
					systemBlocks = append(systemBlocks, map[string]string{"type": typ, "text": text})
				}
			}
		}
		req["system"] = systemBlocks
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
