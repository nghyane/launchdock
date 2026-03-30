package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	authpkg "github.com/nghiahoang/launchdock/internal/auth"
	protocol "github.com/nghiahoang/launchdock/internal/protocol"
	providerspkg "github.com/nghiahoang/launchdock/internal/providers"
)

// HandleChatCompletions handles POST /v1/chat/completions
func HandleChatCompletions(pool *providerspkg.Pool, providers []providerspkg.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			httpError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// Read and parse request
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}

		var chatReq protocol.ChatRequest
		if err := json.Unmarshal(body, &chatReq); err != nil {
			httpError(w, http.StatusBadRequest, "parse request: "+err.Error())
			return
		}

		if strings.HasPrefix(strings.ToLower(chatReq.Model), "claude") {
			thinking, _ := json.Marshal(chatReq.Thinking)
			reasoning, _ := json.Marshal(chatReq.Reasoning)
			slog.Info("claude chat request",
				"model", chatReq.Model,
				"has_thinking", chatReq.Thinking != nil,
				"thinking", trimForLog(string(thinking), 400),
				"reasoning", trimForLog(string(reasoning), 400),
				"reasoning_effort", chatReq.ReasoningEffort,
			)
		}

		if chatReq.Model == "" {
			httpError(w, http.StatusBadRequest, "model is required")
			return
		}

		applyClaudeThinkingAlias(&chatReq)

		// Route to provider
		provider := providerspkg.RouteProvider(providers, chatReq.Model)
		if provider == nil {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("no provider for model %q", chatReq.Model))
			return
		}

		// Pick credential
		cred, err := pool.Pick(provider.ProviderName())
		if err != nil {
			httpError(w, http.StatusServiceUnavailable, err.Error())
			return
		}

		slog.Info("routing request",
			"model", chatReq.Model,
			"provider", provider.ProviderName(),
			"credential", cred.Label,
			"stream", chatReq.Stream,
		)

		// OpenAI OAuth (Codex) → route through Responses API with translation
		if _, ok := provider.(*providerspkg.OpenAIProvider); ok && cred.AuthType == authpkg.AuthOAuth {
			handleChatViaResponsesAPI(w, r, &chatReq, body, provider.(*providerspkg.OpenAIProvider), pool, cred)
			return
		}

		// Translate request
		upstreamBody, urlPath, err := provider.TranslateRequest(&chatReq)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "translate: "+err.Error())
			return
		}

		// Apply OAuth quirks for Anthropic
		if _, ok := provider.(*providerspkg.AnthropicProvider); ok && cred.AuthType == authpkg.AuthOAuth {
			upstreamBody, _ = providerspkg.PrefixTools(upstreamBody, "mcp_")
			upstreamBody, _ = providerspkg.EnsureOAuthRequirements(upstreamBody)
		}

		// Send with retry on retryable errors
		upResp, cred, err := sendWithRetry(r, provider, pool, cred, chatReq.Model, upstreamBody, urlPath)
		if err != nil {
			httpError(w, http.StatusBadGateway, "upstream: "+err.Error())
			return
		}
		defer upResp.Body.Close()

		// Handle errors (non-retryable at this point)
		if upResp.StatusCode != http.StatusOK {
			if provider.ProviderName() == "anthropic" {
				logAnthropicRequestSummary("chat.completions", upstreamBody)
			}
			handleUpstreamError(w, upResp, pool, cred)
			return
		}

		// Stream or non-stream response
		if chatReq.Stream {
			handleStreamResponse(w, upResp, provider, cred, chatReq.Model)
		} else {
			handleNonStreamResponse(w, upResp, provider, cred, chatReq.Model)
		}
	}
}

func applyClaudeThinkingAlias(chatReq *protocol.ChatRequest) {
	switch chatReq.Model {
	case "claude-opus-4-6-thinking":
		chatReq.Model = "claude-opus-4-6"
		if chatReq.Thinking == nil {
			chatReq.Thinking = map[string]any{"type": "enabled", "budget_tokens": 16000}
		}
	case "claude-sonnet-4-6-thinking":
		chatReq.Model = "claude-sonnet-4-6"
		if chatReq.Thinking == nil {
			chatReq.Thinking = map[string]any{"type": "enabled", "budget_tokens": 16000}
		}
	}
}

// sendWithRetry sends the upstream request, retrying with a different credential on retryable errors.
func sendWithRetry(r *http.Request, provider Provider, pool *Pool, cred *Credential, model string, body []byte, urlPath string) (*http.Response, *Credential, error) {
	return doWithCredentialRetry(pool, provider.ProviderName(), cred, func(current *Credential) (*http.Response, error) {
		upReq, err := http.NewRequestWithContext(r.Context(), "POST",
			provider.BaseURL()+urlPath, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}

		if ap, ok := provider.(*AnthropicProvider); ok {
			ap.PrepareWithModel(upReq, current, model)
		} else {
			provider.Prepare(upReq, current)
		}

		resp, err := StreamClient.Do(upReq)
		if err != nil {
			return nil, err
		}
		return resp, nil
	})
}

func handleStreamResponse(w http.ResponseWriter, upResp *http.Response, provider Provider, cred *Credential, model string) {
	sse, ok := NewSSEWriter(w)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	isAnthropic := provider.ProviderName() == "anthropic"

	if isAnthropic {
		relayClaudeSSEAsChat(sse, upResp.Body, model, cred)
	} else {
		// OpenAI — passthrough SSE
		ReadSSE(upResp.Body, func(ev SSEEvent) error {
			if ev.Data == "[DONE]" {
				sse.WriteDone()
				return nil
			}
			sse.WriteData(ev.Data)
			return nil
		})
	}
}

func relayClaudeSSEAsChat(sse *SSEWriter, body io.Reader, model string, cred *Credential) {
	adapter := NewClaudeChatAdapter(model, cred.AuthType == AuthOAuth)
	ReadSSE(body, func(ev SSEEvent) error {
		for _, chunk := range adapter.Consume(ev.Event, ev.Data) {
			sse.WriteJSON(chunk)
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(ev.Data), &event) == nil && event.Type == "message_stop" {
			sse.WriteDone()
		}
		return nil
	})
}

func handleNonStreamResponse(w http.ResponseWriter, upResp *http.Response, provider Provider, cred *Credential, model string) {
	body, err := io.ReadAll(upResp.Body)
	if err != nil {
		httpError(w, http.StatusBadGateway, "read upstream: "+err.Error())
		return
	}

	if provider.ProviderName() == "anthropic" {
		var claudeResp ClaudeResponse
		if err := json.Unmarshal(body, &claudeResp); err != nil {
			httpError(w, http.StatusBadGateway, "parse claude response: "+err.Error())
			return
		}
		// Strip mcp_ prefix from tool names for OAuth
		if cred.AuthType == AuthOAuth {
			for i, c := range claudeResp.Content {
				if c.Type == "tool_use" {
					claudeResp.Content[i].Name = stripPrefix(c.Name, "mcp_")
				}
			}
		}
		chatResp := ClaudeToChat(&claudeResp, model)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatResp)
	} else {
		// OpenAI — passthrough
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

// isRetryable returns true if the upstream error warrants trying another credential.
func isRetryable(statusCode int) bool {
	return statusCode == 429 || statusCode == 500 || statusCode == 502 ||
		statusCode == 503 || statusCode == 529
}

func handleUpstreamError(w http.ResponseWriter, resp *http.Response, pool *Pool, cred *Credential) {
	body, _ := io.ReadAll(resp.Body)
	slog.Warn("upstream error",
		"status", resp.StatusCode,
		"credential", cred.Label,
		"body", trimForLog(string(body), 1200),
	)

	// Cooldown on rate limit or overload
	switch resp.StatusCode {
	case 429:
		pool.Cooldown(cred, 60*time.Second)
	case 529, 503:
		pool.Cooldown(cred, 30*time.Second)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func logAnthropicRequestSummary(endpoint string, body []byte) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		slog.Warn("anthropic request summary unavailable", "endpoint", endpoint, "error", err)
		return
	}

	toolNames := []string{}
	if tools, ok := req["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if name, _ := tm["name"].(string); name != "" {
					toolNames = append(toolNames, name)
				}
			}
		}
	}

	lastRole := ""
	lastTypes := []string{}
	assistantToolCalls := []string{}
	if msgs, ok := req["messages"].([]any); ok && len(msgs) > 0 {
		if last, ok := msgs[len(msgs)-1].(map[string]any); ok {
			lastRole, _ = last["role"].(string)
			switch content := last["content"].(type) {
			case string:
				lastTypes = append(lastTypes, "text")
			case []any:
				for _, item := range content {
					if cm, ok := item.(map[string]any); ok {
						if typ, _ := cm["type"].(string); typ != "" {
							lastTypes = append(lastTypes, typ)
						}
					}
				}
			}
			if toolCalls, ok := last["tool_calls"].([]any); ok {
				for _, tc := range toolCalls {
					if tcm, ok := tc.(map[string]any); ok {
						if fn, ok := tcm["function"].(map[string]any); ok {
							if name, _ := fn["name"].(string); name != "" {
								assistantToolCalls = append(assistantToolCalls, name)
							}
						}
					}
				}
			}
		}
	}

	toolChoice, _ := json.Marshal(req["tool_choice"])
	thinking, _ := json.Marshal(req["thinking"])
	system, _ := json.Marshal(req["system"])

	slog.Warn("anthropic request summary",
		"endpoint", endpoint,
		"model", req["model"],
		"stream", req["stream"],
		"max_tokens", req["max_tokens"],
		"tool_names", toolNames,
		"tool_choice", string(toolChoice),
		"thinking", trimForLog(string(thinking), 400),
		"last_role", lastRole,
		"last_content_types", lastTypes,
		"last_tool_calls", assistantToolCalls,
		"system", trimForLog(string(system), 400),
	)
}

func trimForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "error",
			"code":    code,
		},
	})
}

func stripPrefix(s, prefix string) string {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

// handleChatViaResponsesAPI translates Chat Completions → Responses API for OpenAI OAuth.
func handleChatViaResponsesAPI(w http.ResponseWriter, r *http.Request, chatReq *ChatRequest, body []byte, openai *OpenAIProvider, pool *Pool, cred *Credential) {
	// Translate Chat → Responses format
	respBody, err := ChatToResponsesRequest(body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "translate to responses: "+err.Error())
		return
	}

	upResp, cred, err := doWithCredentialRetry(pool, "openai", cred, func(current *Credential) (*http.Response, error) {
		upstreamURL := openai.ChatGPTBaseURL() + "/responses"
		upReq, err := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(respBody))
		if err != nil {
			return nil, err
		}
		openai.Prepare(upReq, current)
		return StreamClient.Do(upReq)
	})
	if err != nil {
		httpError(w, http.StatusBadGateway, "upstream: "+err.Error())
		return
	}
	defer upResp.Body.Close()

	if upResp.StatusCode != http.StatusOK {
		handleUpstreamError(w, upResp, pool, cred)
		return
	}

	// ChatGPT backend always streams — handle both client modes
	if chatReq.Stream {
		// Stream: translate Responses SSE → Chat SSE
		sse, ok := NewSSEWriter(w)
		if !ok {
			httpError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}
		chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
		created := time.Now().Unix()
		isFirst := true

		ReadSSE(upResp.Body, func(ev SSEEvent) error {
			chunk := ResponsesSSEToChatSSE(ev.Event, ev.Data, chatReq.Model, chatID, created, &isFirst)
			if chunk != "" {
				sse.WriteData(chunk)
			}
			var obj map[string]any
			if json.Unmarshal([]byte(ev.Data), &obj) == nil {
				if t, _ := obj["type"].(string); t == "response.completed" || t == "response.done" {
					sse.WriteDone()
				}
			}
			return nil
		})
	} else {
		// Non-stream client but upstream always streams — collect SSE and build response
		var textParts []string
		var toolCalls []ChatToolCall
		finishReason := "stop"
		var usage map[string]any

		ReadSSE(upResp.Body, func(ev SSEEvent) error {
			var obj map[string]any
			if json.Unmarshal([]byte(ev.Data), &obj) != nil {
				return nil
			}
			typ, _ := obj["type"].(string)
			switch typ {
			case "response.output_text.delta":
				if delta, ok := obj["delta"].(string); ok {
					textParts = append(textParts, delta)
				}
			case "response.output_item.added", "response.output_item.done":
				if item, ok := obj["item"].(map[string]any); ok {
					if item["type"] == "function_call" {
						name, _ := item["name"].(string)
						args, _ := item["arguments"].(string)
						callID, _ := item["call_id"].(string)
						if name != "" {
							toolCalls = upsertToolCall(toolCalls, ChatToolCall{
								ID:       callID,
								Type:     "function",
								Function: ChatFunctionCall{Name: name, Arguments: args},
							})
							finishReason = "tool_calls"
						}
					}
				}
			case "response.function_call_arguments.delta":
				delta, _ := obj["delta"].(string)
				itemID, _ := obj["item_id"].(string)
				if len(toolCalls) > 0 {
					for i := range toolCalls {
						if toolCalls[i].ID == itemID || (itemID == "" && i == len(toolCalls)-1) {
							toolCalls[i].Function.Arguments += delta
							finishReason = "tool_calls"
							break
						}
					}
				}
			case "response.completed":
				if resp, ok := obj["response"].(map[string]any); ok {
					if output, ok := resp["output"].([]any); ok {
						for _, item := range output {
							if itemMap, ok := item.(map[string]any); ok && itemMap["type"] == "function_call" {
								name, _ := itemMap["name"].(string)
								args, _ := itemMap["arguments"].(string)
								callID, _ := itemMap["call_id"].(string)
								if name != "" {
									toolCalls = upsertToolCall(toolCalls, ChatToolCall{
										ID:       callID,
										Type:     "function",
										Function: ChatFunctionCall{Name: name, Arguments: args},
									})
									finishReason = "tool_calls"
								}
							}
						}
					}
					if u, ok := resp["usage"].(map[string]any); ok {
						usage = u
					}
				}
			}
			return nil
		})

		msg := ChatMessage{Role: "assistant"}
		text := strings.Join(textParts, "")
		if text != "" {
			msg.Content = text
		}
		if len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
		}

		chatResp := map[string]any{
			"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   chatReq.Model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       msg,
				"finish_reason": finishReason,
			}},
		}
		if usage != nil {
			inputTokens, _ := usage["input_tokens"].(float64)
			outputTokens, _ := usage["output_tokens"].(float64)
			chatResp["usage"] = map[string]any{
				"prompt_tokens":     int(inputTokens),
				"completion_tokens": int(outputTokens),
				"total_tokens":      int(inputTokens + outputTokens),
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatResp)
	}
}

func upsertToolCall(toolCalls []ChatToolCall, tc ChatToolCall) []ChatToolCall {
	for i := range toolCalls {
		if tc.ID != "" && toolCalls[i].ID == tc.ID {
			toolCalls[i] = tc
			return toolCalls
		}
	}
	return append(toolCalls, tc)
}
