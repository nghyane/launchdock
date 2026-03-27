package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Setup structured logging
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Load credentials from all sources
	creds := LoadAllCredentials()
	if len(creds) == 0 {
		slog.Error("no credentials found",
			"hint", "set ANTHROPIC_API_KEY, OPENAI_API_KEY, or authenticate via `claude` / `codex`",
		)
		os.Exit(1)
	}

	for _, c := range creds {
		slog.Info("loaded credential",
			"provider", c.Provider,
			"type", c.AuthType,
			"label", c.Label,
			"source", c.Source,
		)
	}

	pool := NewPool(creds)

	// Setup providers
	anthropicProvider := &AnthropicProvider{}
	providers := []Provider{
		anthropicProvider,
		&OpenAIProvider{},
	}

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", HandleChatCompletions(pool, providers))
	mux.HandleFunc("/v1/messages", HandleMessages(pool, anthropicProvider))
	mux.HandleFunc("/v1/models", HandleModels(pool))
	mux.HandleFunc("/health", HandleHealth(pool))
	mux.HandleFunc("/", HandleHealth(pool))

	// Wrap with middleware
	handler := withCORS(withRequestID(mux))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	addr := ":" + port
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Minute, // long for streaming
		IdleTimeout:  120 * time.Second,
	}

	slog.Info("llm-mux listening",
		"addr", addr,
		"credentials", len(creds),
		"providers", pool.Providers(),
	)

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		slog.Info("shutting down", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			slog.Error("shutdown error", "error", err)
		}
	}()

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}

// --- Middleware ---

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = shortID()
		}
		w.Header().Set("X-Request-ID", reqID)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"req_id", reqID,
		)
		next.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, anthropic-version, anthropic-beta, x-api-key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func shortID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
