package main

import (
	"encoding/json"
	"net/http"
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

func HandleModels(pool *Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Return commonly used models based on available credentials
		var models []map[string]any

		if pool.Count("anthropic") > 0 {
			for _, m := range []string{
				"claude-sonnet-4-20250514",
				"claude-opus-4-20250514",
				"claude-haiku-3-5-20241022",
			} {
				models = append(models, map[string]any{
					"id":       m,
					"object":   "model",
					"created":  time.Now().Unix(),
					"owned_by": "anthropic",
				})
			}
		}

		if pool.Count("openai") > 0 {
			for _, m := range []string{
				"gpt-4o",
				"gpt-4o-mini",
				"o1-preview",
			} {
				models = append(models, map[string]any{
					"id":       m,
					"object":   "model",
					"created":  time.Now().Unix(),
					"owned_by": "openai",
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   models,
		})
	}
}
