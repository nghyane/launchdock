package launchdock

import (
	"io"
	"net/http"

	protocol "github.com/nghiahoang/launchdock/internal/protocol"
)

type ChatRequest = protocol.ChatRequest
type ChatMessage = protocol.ChatMessage
type ChatContentPart = protocol.ChatContentPart
type ChatImageURL = protocol.ChatImageURL
type ChatTool = protocol.ChatTool
type ChatFunction = protocol.ChatFunction
type ChatToolCall = protocol.ChatToolCall
type ChatFunctionCall = protocol.ChatFunctionCall
type ChatResponse = protocol.ChatResponse
type ChatChoice = protocol.ChatChoice
type ChatUsage = protocol.ChatUsage
type PromptTokensDetails = protocol.PromptTokensDetails
type ChatStreamChunk = protocol.ChatStreamChunk

type ClaudeRequest = protocol.ClaudeRequest
type ClaudeMetadata = protocol.ClaudeMetadata
type ClaudeMessage = protocol.ClaudeMessage
type ClaudeContent = protocol.ClaudeContent
type ClaudeImageSource = protocol.ClaudeImageSource
type ClaudeTool = protocol.ClaudeTool
type ClaudeResponse = protocol.ClaudeResponse
type ClaudeUsage = protocol.ClaudeUsage
type ClaudeSSEEvent = protocol.ClaudeSSEEvent
type ClaudeMessageStart = protocol.ClaudeMessageStart
type ClaudeContentBlockStart = protocol.ClaudeContentBlockStart
type ClaudeContentBlockDelta = protocol.ClaudeContentBlockDelta
type ClaudeDelta = protocol.ClaudeDelta
type ClaudeMessageDelta = protocol.ClaudeMessageDelta
type ClaudeMessageDeltaD = protocol.ClaudeMessageDeltaD

type SSEWriter = protocol.SSEWriter
type SSEEvent = protocol.SSEEvent

func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, bool) { return protocol.NewSSEWriter(w) }
func ReadSSE(r io.Reader, fn func(SSEEvent) error) error    { return protocol.ReadSSE(r, fn) }

func ChatToClaudeRequest(chat *ChatRequest) (*ClaudeRequest, error) {
	return protocol.ChatToClaudeRequest(chat)
}

func ClaudeToChat(cr *ClaudeResponse, model string) *ChatResponse {
	return protocol.ClaudeToChat(cr, model)
}

func ChatToResponsesRequest(body []byte) ([]byte, error) {
	return protocol.ChatToResponsesRequest(body)
}

func ResponsesSSEToChatSSE(eventType, data string, model string, chatID string, created int64, isFirst *bool) string {
	return protocol.ResponsesSSEToChatSSE(eventType, data, model, chatID, created, isFirst)
}

func ResponsesNonStreamToChat(body []byte, model string) ([]byte, error) {
	return protocol.ResponsesNonStreamToChat(body, model)
}

func ClaudeStopToChat(reason *string) string { return protocol.ClaudeStopToChat(reason) }
