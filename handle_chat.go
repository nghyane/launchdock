package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// HandleChatCompletions handles POST /v1/chat/completions
func HandleChatCompletions(pool *Pool, providers []Provider) http.HandlerFunc {
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

		var chatReq ChatRequest
		if err := json.Unmarshal(body, &chatReq); err != nil {
			httpError(w, http.StatusBadRequest, "parse request: "+err.Error())
			return
		}

		if chatReq.Model == "" {
			httpError(w, http.StatusBadRequest, "model is required")
			return
		}

		// Route to provider
		provider := RouteProvider(providers, chatReq.Model)
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
		if _, ok := provider.(*OpenAIProvider); ok && cred.AuthType == AuthOAuth {
			handleChatViaResponsesAPI(w, r, &chatReq, body, provider.(*OpenAIProvider), pool, cred)
			return
		}

		// Translate request
		upstreamBody, urlPath, err := provider.TranslateRequest(&chatReq)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "translate: "+err.Error())
			return
		}

		// Apply OAuth quirks for Anthropic
		if _, ok := provider.(*AnthropicProvider); ok && cred.AuthType == AuthOAuth {
			upstreamBody, _ = PrefixTools(upstreamBody, "mcp_")
			upstreamBody, _ = ensureOAuthRequirements(upstreamBody)
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

// sendWithRetry sends the upstream request, retrying with a different credential on retryable errors.
func sendWithRetry(r *http.Request, provider Provider, pool *Pool, cred *Credential, model string, body []byte, urlPath string) (*http.Response, *Credential, error) {
	maxRetries := 2

	for attempt := 0; attempt <= maxRetries; attempt++ {
		upReq, err := http.NewRequestWithContext(r.Context(), "POST",
			provider.BaseURL()+urlPath, bytes.NewReader(body))
		if err != nil {
			return nil, cred, err
		}

		if ap, ok := provider.(*AnthropicProvider); ok {
			ap.PrepareWithModel(upReq, cred, model)
		} else {
			provider.Prepare(upReq, cred)
		}

		resp, err := StreamClient.Do(upReq)
		if err != nil {
			return nil, cred, err
		}

		if resp.StatusCode == http.StatusOK || !isRetryable(resp.StatusCode) || attempt == maxRetries {
			return resp, cred, nil
		}

		// Retryable error — cooldown current credential, try next
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		slog.Warn("retryable upstream error, trying next credential",
			"status", resp.StatusCode,
			"credential", cred.Label,
			"attempt", attempt+1,
			"body", string(errBody),
		)

		switch resp.StatusCode {
		case 429:
			pool.Cooldown(cred, 60*time.Second)
		case 529, 503:
			pool.Cooldown(cred, 30*time.Second)
		}

		nextCred, err := pool.PickNext(provider.ProviderName(), cred)
		if err != nil {
			// No more credentials — return the error response
			return &http.Response{
				StatusCode: resp.StatusCode,
				Body:       io.NopCloser(bytes.NewReader(errBody)),
				Header:     resp.Header,
			}, cred, nil
		}
		cred = nextCred
		slog.Info("retrying with credential", "label", cred.Label, "attempt", attempt+2)
	}

	return nil, cred, fmt.Errorf("max retries exceeded")
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
	chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	isOAuth := cred.AuthType == AuthOAuth

	var currentToolCall *struct {
		index int
		id    string
		name  string
		args  string
	}

	ReadSSE(body, func(ev SSEEvent) error {
		data := ev.Data

		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil // skip unparseable
		}

		switch event.Type {
		case "message_start":
			// Emit initial chunk with role
			chunk := ChatStreamChunk{
				ID: chatID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: model,
				Choices: []ChatChoice{{Index: 0, Delta: &ChatMessage{Role: "assistant"}, FinishReason: nil}},
			}
			sse.WriteJSON(chunk)

		case "content_block_start":
			var cbs ClaudeContentBlockStart
			json.Unmarshal([]byte(data), &cbs)
			if cbs.ContentBlock.Type == "thinking" {
				// Skip — thinking deltas will be emitted in content_block_delta
			} else if cbs.ContentBlock.Type == "tool_use" {
				name := cbs.ContentBlock.Name
				if isOAuth {
					name = stripPrefix(name, "mcp_")
				}
				currentToolCall = &struct {
					index int
					id    string
					name  string
					args  string
				}{
					index: cbs.Index,
					id:    cbs.ContentBlock.ID,
					name:  name,
				}
				// Emit tool call start
				chunk := ChatStreamChunk{
					ID: chatID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: model,
					Choices: []ChatChoice{{
						Index: 0,
						Delta: &ChatMessage{
							ToolCalls: []ChatToolCall{{
								ID:   cbs.ContentBlock.ID,
								Type: "function",
								Function: ChatFunctionCall{
									Name:      name,
									Arguments: "",
								},
							}},
						},
						FinishReason: nil,
					}},
				}
				sse.WriteJSON(chunk)
			}

		case "content_block_delta":
			var cbd ClaudeContentBlockDelta
			json.Unmarshal([]byte(data), &cbd)

			switch cbd.Delta.Type {
			case "text_delta":
				chunk := ChatStreamChunk{
					ID: chatID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: model,
					Choices: []ChatChoice{{
						Index:        0,
						Delta:        &ChatMessage{Content: cbd.Delta.Text},
						FinishReason: nil,
					}},
				}
				sse.WriteJSON(chunk)

			case "thinking_delta":
				// Emit thinking as a special chunk — some clients understand this
				if cbd.Delta.Thinking != "" {
					chunk := ChatStreamChunk{
						ID: chatID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: model,
						Choices: []ChatChoice{{
							Index:        0,
							Delta:        &ChatMessage{Content: cbd.Delta.Thinking, Role: "thinking"},
							FinishReason: nil,
						}},
					}
					sse.WriteJSON(chunk)
				}

			case "input_json_delta":
				if currentToolCall != nil {
					// Only send arguments delta — no empty id/type/name fields
					chunk := ChatStreamChunk{
						ID: chatID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: model,
						Choices: []ChatChoice{{
							Index: 0,
							Delta: &ChatMessage{
								ToolCalls: []ChatToolCall{{
									Index: currentToolCall.index,
									Function: ChatFunctionCall{
										Arguments: cbd.Delta.PartialJSON,
									},
								}},
							},
							FinishReason: nil,
						}},
					}
					sse.WriteJSON(chunk)
				}
			}

		case "content_block_stop":
			currentToolCall = nil

		case "message_delta":
			var md ClaudeMessageDelta
			json.Unmarshal([]byte(data), &md)

			finishReason := claudeStopToChat(md.Delta.StopReason)
			chunk := ChatStreamChunk{
				ID: chatID, Object: "chat.completion.chunk", Created: time.Now().Unix(), Model: model,
				Choices: []ChatChoice{{
					Index:        0,
					Delta:        &ChatMessage{},
					FinishReason: &finishReason,
				}},
			}
			if md.Usage != nil {
				chunk.Usage = &ChatUsage{
					PromptTokens:     md.Usage.InputTokens,
					CompletionTokens: md.Usage.OutputTokens,
					TotalTokens:      md.Usage.InputTokens + md.Usage.OutputTokens,
				}
			}
			sse.WriteJSON(chunk)

		case "message_stop":
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
		"body", string(body),
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

	upstreamURL := openai.ChatGPTBaseURL() + "/responses"
	upReq, err := http.NewRequestWithContext(r.Context(), "POST", upstreamURL, bytes.NewReader(respBody))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "build request: "+err.Error())
		return
	}
	openai.Prepare(upReq, cred)

	upResp, err := StreamClient.Do(upReq)
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
			case "response.function_call_arguments.delta":
				// Accumulate tool calls
				name, _ := obj["name"].(string)
				callID, _ := obj["call_id"].(string)
				delta, _ := obj["delta"].(string)
				if name != "" {
					toolCalls = append(toolCalls, ChatToolCall{
						ID:       callID,
						Type:     "function",
						Function: ChatFunctionCall{Name: name, Arguments: delta},
					})
					finishReason = "tool_calls"
				} else if len(toolCalls) > 0 {
					last := &toolCalls[len(toolCalls)-1]
					last.Function.Arguments += delta
				}
			case "response.completed":
				if resp, ok := obj["response"].(map[string]any); ok {
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
