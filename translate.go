package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Chat ↔ Claude translation

const defaultMaxTokens = 8192

// ChatToClaudeRequest translates an OpenAI Chat Completions request to Claude Messages API.
func ChatToClaudeRequest(chat *ChatRequest) (*ClaudeRequest, error) {
	cr := &ClaudeRequest{
		Model:     chat.Model,
		Stream:    chat.Stream,
		MaxTokens: defaultMaxTokens,
	}

	if chat.MaxTokens != nil {
		cr.MaxTokens = *chat.MaxTokens
	}
	cr.Temperature = chat.Temperature
	cr.TopP = chat.TopP
	cr.Thinking = chat.Thinking

	// Extract system messages
	var systemParts []string
	var messages []ClaudeMessage

	for _, msg := range chat.Messages {
		switch msg.Role {
		case "system":
			text := contentToString(msg.Content)
			if text != "" {
				systemParts = append(systemParts, text)
			}

		case "user":
			cm, err := chatMsgToClaudeMsg(msg)
			if err != nil {
				return nil, err
			}
			messages = append(messages, cm)

		case "assistant":
			cm, err := assistantToClaudeMsg(msg)
			if err != nil {
				return nil, err
			}
			messages = append(messages, cm)

		case "tool":
			cm := toolResultToClaudeMsg(msg)
			messages = append(messages, cm)
		}
	}

	if len(systemParts) > 0 {
		cr.System = strings.Join(systemParts, "\n\n")
	}

	// Merge consecutive same-role messages (Claude requires alternating roles)
	cr.Messages = mergeConsecutiveMessages(messages)

	// Translate tools
	for _, t := range chat.Tools {
		cr.Tools = append(cr.Tools, ClaudeTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}

	// Translate tool_choice
	if chat.ToolChoice != nil {
		cr.ToolChoice = translateToolChoice(chat.ToolChoice)
	}

	return cr, nil
}

func chatMsgToClaudeMsg(msg ChatMessage) (ClaudeMessage, error) {
	content, err := toClaudeContent(msg.Content)
	if err != nil {
		return ClaudeMessage{}, err
	}
	return ClaudeMessage{Role: "user", Content: content}, nil
}

func assistantToClaudeMsg(msg ChatMessage) (ClaudeMessage, error) {
	var parts []ClaudeContent

	// Text content
	text := contentToString(msg.Content)
	if text != "" {
		parts = append(parts, ClaudeContent{Type: "text", Text: text})
	}

	// Tool calls
	for _, tc := range msg.ToolCalls {
		parts = append(parts, ClaudeContent{
			Type:  "tool_use",
			ID:    sanitizeToolCallID(tc.ID),
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}

	if len(parts) == 0 {
		// Claude rejects empty assistant messages
		parts = append(parts, ClaudeContent{Type: "text", Text: "."})
	}

	return ClaudeMessage{Role: "assistant", Content: parts}, nil
}

func toolResultToClaudeMsg(msg ChatMessage) ClaudeMessage {
	content := contentToString(msg.Content)
	return ClaudeMessage{
		Role: "user",
		Content: []ClaudeContent{{
			Type:      "tool_result",
			ToolUseID: sanitizeToolCallID(msg.ToolCallID),
			Content:   content,
		}},
	}
}

// --- Response translation ---

// ClaudeToChat translates a non-streaming Claude response to Chat Completions format.
func ClaudeToChat(cr *ClaudeResponse, model string) *ChatResponse {
	msg := &ChatMessage{Role: "assistant"}

	var textParts []string
	var toolCalls []ChatToolCall

	for _, c := range cr.Content {
		switch c.Type {
		case "text":
			textParts = append(textParts, c.Text)
		case "tool_use":
			toolCalls = append(toolCalls, ChatToolCall{
				ID:   c.ID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      c.Name,
					Arguments: string(c.Input),
				},
			})
		}
	}

	if len(textParts) > 0 {
		text := strings.Join(textParts, "")
		msg.Content = text
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	finishReason := claudeStopToChat(cr.StopReason)

	return &ChatResponse{
		ID:      "chatcmpl-" + cr.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: &finishReason,
		}},
		Usage: &ChatUsage{
			PromptTokens:     cr.Usage.InputTokens,
			CompletionTokens: cr.Usage.OutputTokens,
			TotalTokens:      cr.Usage.InputTokens + cr.Usage.OutputTokens,
		},
	}
}

func claudeStopToChat(reason *string) string {
	if reason == nil {
		return "stop"
	}
	switch *reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

// --- Helpers ---

func contentToString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if text, _ := m["text"].(string); text != "" {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "")
	default:
		if b, err := json.Marshal(content); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", content)
	}
}

func toClaudeContent(content any) (any, error) {
	switch v := content.(type) {
	case string:
		if v == "" {
			return []ClaudeContent{{Type: "text", Text: "."}}, nil
		}
		return v, nil
	case []any:
		var parts []ClaudeContent
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case "text":
				text, _ := m["text"].(string)
				if text != "" {
					parts = append(parts, ClaudeContent{Type: "text", Text: text})
				}
			case "image_url":
				if iu, ok := m["image_url"].(map[string]any); ok {
					url, _ := iu["url"].(string)
					if strings.HasPrefix(url, "data:") {
						// data:image/png;base64,xxx
						mt, data := parseDataURI(url)
						parts = append(parts, ClaudeContent{
							Type: "image",
							Source: &ClaudeImageSource{
								Type:      "base64",
								MediaType: mt,
								Data:      data,
							},
						})
					}
				}
			}
		}
		if len(parts) == 0 {
			return []ClaudeContent{{Type: "text", Text: "."}}, nil
		}
		return parts, nil
	default:
		return content, nil
	}
}

func parseDataURI(uri string) (mediaType, data string) {
	// data:image/png;base64,xxxx
	after, _ := strings.CutPrefix(uri, "data:")
	parts := strings.SplitN(after, ",", 2)
	if len(parts) != 2 {
		return "application/octet-stream", ""
	}
	meta := parts[0] // image/png;base64
	mt, _, _ := strings.Cut(meta, ";")
	return mt, parts[1]
}

var validToolCallID = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeToolCallID(id string) string {
	return validToolCallID.ReplaceAllString(id, "_")
}

func mergeConsecutiveMessages(msgs []ClaudeMessage) []ClaudeMessage {
	if len(msgs) == 0 {
		return msgs
	}
	var result []ClaudeMessage
	result = append(result, msgs[0])

	for i := 1; i < len(msgs); i++ {
		last := &result[len(result)-1]
		if last.Role == msgs[i].Role {
			// Merge content arrays
			lastContent := toContentSlice(last.Content)
			newContent := toContentSlice(msgs[i].Content)
			last.Content = append(lastContent, newContent...)
		} else {
			result = append(result, msgs[i])
		}
	}
	return result
}

func toContentSlice(content any) []ClaudeContent {
	switch v := content.(type) {
	case []ClaudeContent:
		return v
	case string:
		return []ClaudeContent{{Type: "text", Text: v}}
	default:
		// Try JSON round-trip
		b, _ := json.Marshal(content)
		var parts []ClaudeContent
		if json.Unmarshal(b, &parts) == nil {
			return parts
		}
		return []ClaudeContent{{Type: "text", Text: fmt.Sprintf("%v", content)}}
	}
}

func translateToolChoice(tc any) any {
	switch v := tc.(type) {
	case string:
		switch v {
		case "none":
			return map[string]string{"type": "none"}
		case "auto":
			return map[string]string{"type": "auto"}
		case "required":
			return map[string]string{"type": "any"}
		default:
			return map[string]string{"type": "auto"}
		}
	case map[string]any:
		if fn, ok := v["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]any{"type": "tool", "name": name}
			}
		}
	}
	return nil
}
