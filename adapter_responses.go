package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Chat Completions ↔ Responses API translation
// Based on ZipZhu/ResponseBridge and huggingface/responses.js patterns

// ChatToResponsesRequest translates a Chat Completions request to Responses API format.
func ChatToResponsesRequest(body []byte) ([]byte, error) {
	var chat map[string]any
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, err
	}

	resp := map[string]any{
		"model": chat["model"],
		"store": false,
	}

	// Convert messages → input + instructions
	messages, _ := chat["messages"].([]any)
	var input []any
	var instructions []string

	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		content := msg["content"]

		switch role {
		case "system", "developer":
			// System messages become instructions
			if text := contentToString(content); text != "" {
				instructions = append(instructions, text)
			}

		case "user":
			parts := contentToParts(content, "input_text")
			input = append(input, map[string]any{
				"role":    "user",
				"content": parts,
			})

		case "assistant":
			// Assistant messages with tool_calls
			toolCalls, _ := msg["tool_calls"].([]any)
			if len(toolCalls) > 0 {
				for _, tc := range toolCalls {
					tcMap, _ := tc.(map[string]any)
					fn, _ := tcMap["function"].(map[string]any)
					name, _ := fn["name"].(string)
					args, _ := fn["arguments"].(string)
					callID, _ := tcMap["id"].(string)
					input = append(input, map[string]any{
						"type":      "function_call",
						"name":      name,
						"arguments": args,
						"call_id":   callID,
					})
				}
			}
			// Text content
			text := contentToString(content)
			if text != "" {
				parts := contentToParts(content, "output_text")
				input = append(input, map[string]any{
					"role":    "assistant",
					"content": parts,
				})
			}

		case "tool":
			callID, _ := msg["tool_call_id"].(string)
			text := contentToString(content)
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  text,
			})
		}
	}

	resp["input"] = input

	if len(instructions) > 0 {
		resp["instructions"] = strings.Join(instructions, "\n\n")
	} else {
		resp["instructions"] = "You are a helpful assistant."
	}

	// ChatGPT backend requires stream=true always
	resp["stream"] = true

	// Passthrough common params (skip max_tokens — not supported by ChatGPT backend)
	for _, key := range []string{"temperature", "top_p"} {
		if v, ok := chat[key]; ok {
			resp[key] = v
		}
	}

	// Tools — same format in both APIs
	if tools, ok := chat["tools"]; ok {
		resp["tools"] = tools
		resp["tool_choice"] = "auto"
		resp["parallel_tool_calls"] = true
	}

	// Service tier — only set if explicitly passed
	if tier, ok := chat["service_tier"]; ok {
		resp["service_tier"] = tier
	}

	// Reasoning — passthrough if present, or set defaults for capable models
	if reasoning, ok := chat["reasoning"]; ok {
		resp["reasoning"] = reasoning
	} else if reasoning, ok := chat["reasoning_effort"]; ok {
		// OpenAI SDK sends reasoning_effort as string
		resp["reasoning"] = map[string]any{"effort": reasoning}
	}

	// Text verbosity — low for concise responses by default
	if text, ok := chat["text"]; ok {
		resp["text"] = text
	}

	// Prompt cache key — use conversation_id if provided, or generate stable one
	// This enables server-side prompt caching across turns
	if cacheKey, ok := chat["prompt_cache_key"]; ok {
		resp["prompt_cache_key"] = cacheKey
	}

	// Previous response ID — chain responses for incremental context
	if prevID, ok := chat["previous_response_id"]; ok {
		resp["previous_response_id"] = prevID
	}

	return json.Marshal(resp)
}

// contentToParts converts message content to Responses API content parts.
func contentToParts(content any, textType string) []map[string]any {
	switch v := content.(type) {
	case string:
		if v == "" {
			return []map[string]any{}
		}
		return []map[string]any{{"type": textType, "text": v}}
	case []any:
		var parts []map[string]any
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			switch t {
			case "text":
				text, _ := m["text"].(string)
				parts = append(parts, map[string]any{"type": textType, "text": text})
			case "image_url":
				// Pass through image URLs
				parts = append(parts, m)
			}
		}
		return parts
	default:
		return []map[string]any{}
	}
}

// ResponsesSSEToChatSSE translates a single Responses API SSE event to Chat Completions SSE chunk.
// Returns empty string if the event should be skipped.
func ResponsesSSEToChatSSE(eventType, data string, model string, chatID string, created int64, isFirst *bool) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return ""
	}

	typ, _ := obj["type"].(string)
	if typ == "" {
		typ = eventType
	}

	switch {
	case typ == "response.output_text.delta":
		delta, _ := obj["delta"].(string)
		if delta == "" {
			return ""
		}
		chunk := buildChatChunk(chatID, model, created, *isFirst, delta, nil, nil)
		*isFirst = false
		b, _ := json.Marshal(chunk)
		return string(b)

	case typ == "response.function_call_arguments.delta":
		delta, _ := obj["delta"].(string)
		name, _ := obj["name"].(string)
		callID, _ := obj["call_id"].(string)

		var tc *ChatToolCall
		if name != "" || callID != "" {
			tc = &ChatToolCall{
				ID:   callID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      name,
					Arguments: delta,
				},
			}
		} else if delta != "" {
			tc = &ChatToolCall{
				Function: ChatFunctionCall{
					Arguments: delta,
				},
			}
		}
		if tc != nil {
			chunk := buildChatChunk(chatID, model, created, *isFirst, "", []ChatToolCall{*tc}, nil)
			*isFirst = false
			b, _ := json.Marshal(chunk)
			return string(b)
		}

	case typ == "response.completed" || typ == "response.done":
		finish := "stop"
		// Check if it was a tool call
		if resp, ok := obj["response"].(map[string]any); ok {
			if output, ok := resp["output"].([]any); ok {
				for _, item := range output {
					if itemMap, ok := item.(map[string]any); ok {
						if itemMap["type"] == "function_call" {
							finish = "tool_calls"
							break
						}
					}
				}
			}
		}
		chunk := buildChatChunk(chatID, model, created, false, "", nil, &finish)
		// Add usage if available
		if resp, ok := obj["response"].(map[string]any); ok {
			if usage, ok := resp["usage"].(map[string]any); ok {
				inputTokens, _ := usage["input_tokens"].(float64)
				outputTokens, _ := usage["output_tokens"].(float64)
				chunk["usage"] = map[string]any{
					"prompt_tokens":     int(inputTokens),
					"completion_tokens": int(outputTokens),
					"total_tokens":      int(inputTokens + outputTokens),
				}
			}
		}
		b, _ := json.Marshal(chunk)
		return string(b)

	case typ == "response.output_item.added":
		// Emit role chunk for first message item
		if *isFirst {
			chunk := buildChatChunk(chatID, model, created, true, "", nil, nil)
			*isFirst = false
			b, _ := json.Marshal(chunk)
			return string(b)
		}
	}

	return ""
}

// ResponsesNonStreamToChat translates a non-stream Responses API response to Chat Completions.
func ResponsesNonStreamToChat(body []byte, model string) ([]byte, error) {
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	var text string
	var toolCalls []ChatToolCall
	finishReason := "stop"

	output, _ := resp["output"].([]any)
	for _, item := range output {
		block, _ := item.(map[string]any)
		if block == nil {
			continue
		}

		switch block["type"] {
		case "message":
			content, _ := block["content"].([]any)
			for _, part := range content {
				p, _ := part.(map[string]any)
				if p == nil {
					continue
				}
				if t, _ := p["type"].(string); t == "output_text" {
					if s, _ := p["text"].(string); s != "" {
						text += s
					}
				}
			}
		case "function_call":
			name, _ := block["name"].(string)
			args, _ := block["arguments"].(string)
			callID, _ := block["call_id"].(string)
			toolCalls = append(toolCalls, ChatToolCall{
				ID:   callID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      name,
					Arguments: args,
				},
			})
			finishReason = "tool_calls"
		}
	}

	msg := ChatMessage{Role: "assistant"}
	if text != "" {
		msg.Content = text
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	rid, _ := resp["id"].(string)
	usage := resp["usage"]

	chatResp := map[string]any{
		"id":      fmt.Sprintf("chatcmpl-%s", rid),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason,
		}},
	}

	if u, ok := usage.(map[string]any); ok {
		inputTokens, _ := u["input_tokens"].(float64)
		outputTokens, _ := u["output_tokens"].(float64)
		chatResp["usage"] = map[string]any{
			"prompt_tokens":     int(inputTokens),
			"completion_tokens": int(outputTokens),
			"total_tokens":      int(inputTokens + outputTokens),
		}
	}

	return json.Marshal(chatResp)
}

func buildChatChunk(id, model string, created int64, withRole bool, content string, toolCalls []ChatToolCall, finishReason *string) map[string]any {
	delta := map[string]any{}
	if withRole {
		delta["role"] = "assistant"
	}
	if content != "" {
		delta["content"] = content
	}
	if len(toolCalls) > 0 {
		delta["tool_calls"] = toolCalls
	}

	choice := map[string]any{
		"index":         0,
		"delta":         delta,
		"finish_reason": finishReason,
	}

	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{choice},
	}
}
