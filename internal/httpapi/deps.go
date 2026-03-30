package httpapi

import (
	"io"
	"net/http"

	authpkg "github.com/nghiahoang/launchdock/internal/auth"
	httpxpkg "github.com/nghiahoang/launchdock/internal/httpx"
	protocol "github.com/nghiahoang/launchdock/internal/protocol"
	providerspkg "github.com/nghiahoang/launchdock/internal/providers"
)

type Credential = authpkg.Credential

const AuthOAuth = authpkg.AuthOAuth

type Pool = providerspkg.Pool
type Provider = providerspkg.Provider
type OpenAIProvider = providerspkg.OpenAIProvider
type AnthropicProvider = providerspkg.AnthropicProvider

type ChatRequest = protocol.ChatRequest
type ChatMessage = protocol.ChatMessage
type ChatTool = protocol.ChatTool
type ChatToolCall = protocol.ChatToolCall
type ChatFunctionCall = protocol.ChatFunctionCall
type ChatStreamChunk = protocol.ChatStreamChunk
type ChatChoice = protocol.ChatChoice
type ChatResponse = protocol.ChatResponse
type ChatUsage = protocol.ChatUsage
type SSEWriter = protocol.SSEWriter
type SSEEvent = protocol.SSEEvent
type ClaudeResponse = protocol.ClaudeResponse
type ClaudeMessageDelta = protocol.ClaudeMessageDelta
type ClaudeContentBlockStart = protocol.ClaudeContentBlockStart
type ClaudeContentBlockDelta = protocol.ClaudeContentBlockDelta
type ClaudeMessageStart = protocol.ClaudeMessageStart
type ClaudeChatAdapter = protocol.ClaudeChatAdapter

var (
	StreamClient = httpxpkg.StreamClient
	APIClient    = httpxpkg.APIClient
)

func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, bool) { return protocol.NewSSEWriter(w) }
func ReadSSE(r io.Reader, fn func(SSEEvent) error) error    { return protocol.ReadSSE(r, fn) }
func PrefixTools(body []byte, prefix string) ([]byte, error) {
	return providerspkg.PrefixTools(body, prefix)
}
func EnsureOAuthRequirements(body []byte) ([]byte, error) {
	return providerspkg.EnsureOAuthRequirements(body)
}
func StripToolPrefix(data []byte, prefix string) []byte {
	return providerspkg.StripToolPrefix(data, prefix)
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
func ClaudeToChat(cr *ClaudeResponse, model string) *ChatResponse {
	return protocol.ClaudeToChat(cr, model)
}
func ChatToClaudeRequest(chat *ChatRequest) (*protocol.ClaudeRequest, error) {
	return protocol.ChatToClaudeRequest(chat)
}
func NewClaudeChatAdapter(model string, isOAuth bool) *protocol.ClaudeChatAdapter {
	return protocol.NewClaudeChatAdapter(model, isOAuth)
}
