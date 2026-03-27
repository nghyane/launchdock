package main

import "encoding/json"

// Claude Messages API wire types

type ClaudeRequest struct {
	Model     string          `json:"model"`
	Messages  []ClaudeMessage `json:"messages"`
	System    any             `json:"system,omitempty"` // string or []ClaudeContent
	Stream    bool            `json:"stream,omitempty"`
	MaxTokens int             `json:"max_tokens"`
	Metadata  *ClaudeMetadata `json:"metadata,omitempty"`

	Temperature *float64     `json:"temperature,omitempty"`
	TopP        *float64     `json:"top_p,omitempty"`
	TopK        *int         `json:"top_k,omitempty"`
	Tools       []ClaudeTool `json:"tools,omitempty"`
	ToolChoice  any          `json:"tool_choice,omitempty"`
	StopSeqs    []string     `json:"stop_sequences,omitempty"`
	Thinking    any          `json:"thinking,omitempty"`
}

type ClaudeMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

type ClaudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []ClaudeContent
}

type ClaudeContent struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image
	Source *ClaudeImageSource `json:"source,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"` // string or []ClaudeContent (nested)
	IsError   bool   `json:"is_error,omitempty"`
}

type ClaudeImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // "image/png"
	Data      string `json:"data"`
}

type ClaudeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// --- Response types ---

type ClaudeResponse struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Content      []ClaudeContent `json:"content"`
	Model        string          `json:"model"`
	StopReason   *string         `json:"stop_reason"`
	StopSequence *string         `json:"stop_sequence"`
	Usage        ClaudeUsage     `json:"usage"`
}

type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- SSE event types ---

type ClaudeSSEEvent struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"-"` // full event data
}

type ClaudeMessageStart struct {
	Type    string         `json:"type"`
	Message ClaudeResponse `json:"message"`
}

type ClaudeContentBlockStart struct {
	Type         string        `json:"type"`
	Index        int           `json:"index"`
	ContentBlock ClaudeContent `json:"content_block"`
}

type ClaudeContentBlockDelta struct {
	Type  string      `json:"type"`
	Index int         `json:"index"`
	Delta ClaudeDelta `json:"delta"`
}

type ClaudeDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`         // text_delta
	PartialJSON string `json:"partial_json,omitempty"` // input_json_delta
	Thinking    string `json:"thinking,omitempty"`     // thinking_delta
}

type ClaudeMessageDelta struct {
	Type  string              `json:"type"`
	Delta ClaudeMessageDeltaD `json:"delta"`
	Usage *ClaudeUsage        `json:"usage,omitempty"`
}

type ClaudeMessageDeltaD struct {
	StopReason   *string `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}
