package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

func HandleHealth(pool *Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"providers": pool.Providers(),
			"credentials": map[string]int{
				"anthropic": pool.Count("anthropic"),
				"openai":    pool.Count("openai"),
				"total":     pool.Count(""),
			},
		})
	}
}

// --- Models cache ---

var (
	modelCache     []map[string]any
	modelCacheMu   sync.RWMutex
	modelCacheTime time.Time
	modelCacheTTL  = 10 * time.Minute
)

func HandleModels(pool *Pool, anthropic *AnthropicProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		models := getCachedModels(pool, anthropic)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   models,
		})
	}
}

func getCachedModels(pool *Pool, anthropic *AnthropicProvider) []map[string]any {
	modelCacheMu.RLock()
	if modelCache != nil && time.Since(modelCacheTime) < modelCacheTTL {
		defer modelCacheMu.RUnlock()
		return modelCache
	}
	modelCacheMu.RUnlock()

	models := fetchAllModels(pool, anthropic)

	modelCacheMu.Lock()
	modelCache = models
	modelCacheTime = time.Now()
	modelCacheMu.Unlock()

	return models
}

func fetchAllModels(pool *Pool, anthropic *AnthropicProvider) []map[string]any {
	var models []map[string]any

	// Anthropic — fetch from API
	if pool.Count("anthropic") > 0 {
		apiModels := fetchAnthropicModels(pool, anthropic)
		if len(apiModels) > 0 {
			models = append(models, apiModels...)
		} else {
			// Fallback to hardcoded if API fails
			models = append(models, anthropicFallbackModels()...)
		}
	}

	// OpenAI — hardcoded (Codex OAuth lacks api.model.read scope)
	if pool.Count("openai") > 0 {
		models = append(models, openAIModels()...)
	}

	return models
}

func fetchAnthropicModels(pool *Pool, provider *AnthropicProvider) []map[string]any {
	cred, err := pool.Pick("anthropic")
	if err != nil {
		slog.Debug("no anthropic credential for models fetch", "error", err)
		return nil
	}

	req, err := http.NewRequest("GET", provider.BaseURL()+"/v1/models", nil)
	if err != nil {
		return nil
	}
	provider.PrepareWithModel(req, cred, "")

	resp, err := APIClient.Do(req)
	if err != nil {
		slog.Debug("anthropic models fetch failed", "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		slog.Debug("anthropic models fetch non-200", "status", resp.StatusCode)
		return nil
	}

	var result struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Debug("anthropic models parse failed", "error", err)
		return nil
	}

	var models []map[string]any
	for _, m := range result.Data {
		models = append(models, map[string]any{
			"id":       m.ID,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "anthropic",
		})
	}

	slog.Info("fetched anthropic models", "count", len(models))
	return models
}

func anthropicFallbackModels() []map[string]any {
	var models []map[string]any
	for _, m := range []string{
		"claude-opus-4-20250514",
		"claude-opus-4-1-20250805",
		"claude-sonnet-4-20250514",
		"claude-sonnet-4-1-20250514",
		"claude-haiku-3-5-20241022",
	} {
		models = append(models, map[string]any{
			"id":       m,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "anthropic",
		})
	}
	return models
}

const codexModelsURL = "https://raw.githubusercontent.com/openai/codex/main/codex-rs/core/models.json"

func openAIModels() []map[string]any {
	// Try fetching from Codex repo
	if models := fetchCodexModels(); len(models) > 0 {
		return models
	}
	// Fallback hardcoded
	return openAIFallbackModels()
}

func fetchCodexModels() []map[string]any {
	resp, err := APIClient.Get(codexModelsURL)
	if err != nil {
		slog.Debug("codex models fetch failed", "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil
	}

	var result struct {
		Models []struct {
			Slug          string `json:"slug"`
			DisplayName   string `json:"display_name"`
			Visibility    string `json:"visibility"`
			ContextWindow int    `json:"context_window"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Debug("codex models parse failed", "error", err)
		return nil
	}

	var models []map[string]any
	for _, m := range result.Models {
		if m.Visibility != "list" {
			continue
		}
		models = append(models, map[string]any{
			"id":       m.Slug,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "openai",
		})
	}

	slog.Info("fetched codex models", "count", len(models))
	return models
}

func openAIFallbackModels() []map[string]any {
	var models []map[string]any
	for _, m := range []string{
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex",
		"o3-mini",
		"o4-mini",
	} {
		models = append(models, map[string]any{
			"id":       m,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "openai",
		})
	}
	return models
}
