package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ClaudeChatAdapter struct {
	chatID   string
	model    string
	created  int64
	isOAuth  bool
	toolCall *claudeToolCallState
}

type claudeToolCallState struct {
	id   string
	name string
	args string
	idx  int
}

func NewClaudeChatAdapter(model string, isOAuth bool) *ClaudeChatAdapter {
	return &ClaudeChatAdapter{
		chatID:  fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		model:   model,
		created: time.Now().Unix(),
		isOAuth: isOAuth,
	}
}

func (a *ClaudeChatAdapter) Consume(eventType, data string) []ChatStreamChunk {
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}
	if event.Type == "" {
		event.Type = eventType
	}

	switch event.Type {
	case "message_start":
		return []ChatStreamChunk{a.chunk(&ChatMessage{Role: "assistant"}, nil)}

	case "content_block_start":
		var cbs ClaudeContentBlockStart
		if json.Unmarshal([]byte(data), &cbs) != nil {
			return nil
		}
		if cbs.ContentBlock.Type != "tool_use" {
			return nil
		}
		name := cbs.ContentBlock.Name
		if a.isOAuth {
			name = strings.TrimPrefix(name, "mcp_")
		}
		a.toolCall = &claudeToolCallState{id: cbs.ContentBlock.ID, name: name, idx: 0}
		return []ChatStreamChunk{a.chunk(&ChatMessage{ToolCalls: []ChatToolCall{{
			Index: 0,
			ID:    a.toolCall.id,
			Type:  "function",
			Function: ChatFunctionCall{
				Name: name,
			},
		}}}, nil)}

	case "content_block_delta":
		var cbd ClaudeContentBlockDelta
		if json.Unmarshal([]byte(data), &cbd) != nil {
			return nil
		}
		switch cbd.Delta.Type {
		case "text_delta":
			if cbd.Delta.Text == "" {
				return nil
			}
			return []ChatStreamChunk{a.chunk(&ChatMessage{Content: cbd.Delta.Text}, nil)}
		case "thinking_delta":
			if cbd.Delta.Thinking == "" {
				return nil
			}
			return []ChatStreamChunk{a.chunk(&ChatMessage{ReasoningContent: cbd.Delta.Thinking}, nil)}
		case "input_json_delta":
			if a.toolCall == nil || cbd.Delta.PartialJSON == "" {
				return nil
			}
			a.toolCall.args += cbd.Delta.PartialJSON
		}
		return nil

	case "content_block_stop":
		if a.toolCall == nil || a.toolCall.args == "" {
			a.toolCall = nil
			return nil
		}
		chunk := a.chunk(&ChatMessage{ToolCalls: []ChatToolCall{{
			Index: 0,
			ID:    a.toolCall.id,
			Type:  "function",
			Function: ChatFunctionCall{
				Name:      a.toolCall.name,
				Arguments: a.toolCall.args,
			},
		}}}, nil)
		a.toolCall = nil
		return []ChatStreamChunk{chunk}

	case "message_delta":
		var md ClaudeMessageDelta
		if json.Unmarshal([]byte(data), &md) != nil {
			return nil
		}
		finish := ClaudeStopToChat(md.Delta.StopReason)
		chunk := a.chunk(&ChatMessage{}, &finish)
		if md.Usage != nil {
			chunk.Usage = &ChatUsage{
				PromptTokens:     md.Usage.InputTokens,
				CompletionTokens: md.Usage.OutputTokens,
				TotalTokens:      md.Usage.InputTokens + md.Usage.OutputTokens,
			}
		}
		return []ChatStreamChunk{chunk}
	}

	return nil
}

func (a *ClaudeChatAdapter) chunk(delta *ChatMessage, finish *string) ChatStreamChunk {
	return ChatStreamChunk{
		ID:      a.chatID,
		Object:  "chat.completion.chunk",
		Created: a.created,
		Model:   a.model,
		Choices: []ChatChoice{{Index: 0, Delta: delta, FinishReason: finish}},
	}
}
