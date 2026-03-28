package launchdock

import (
	"net/http"

	httpapipkg "github.com/nghiahoang/launchdock/internal/httpapi"
)

func HandleChatCompletions(pool *Pool, providers []Provider) http.HandlerFunc {
	return httpapipkg.HandleChatCompletions(pool, providers)
}

func HandleMessages(pool *Pool, anthropic *AnthropicProvider) http.HandlerFunc {
	return httpapipkg.HandleMessages(pool, anthropic)
}

func HandleResponses(pool *Pool, openai *OpenAIProvider) http.HandlerFunc {
	return httpapipkg.HandleResponses(pool, openai)
}

func HandleModels(pool *Pool, anthropic *AnthropicProvider) http.HandlerFunc {
	return httpapipkg.HandleModels(pool, anthropic)
}

func HandleHealth(pool *Pool) http.HandlerFunc {
	return httpapipkg.HandleHealth(pool)
}

func fetchAllModels(pool *Pool, anthropic *AnthropicProvider) []map[string]any {
	return httpapipkg.FetchAllModels(pool, anthropic)
}
