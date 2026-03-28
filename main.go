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
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "auth":
			handleAuthCommand()
			return
		case "launch":
			handleLaunchCommand()
			return
		case "start":
			handleStartCommand()
			return
		case "ps":
			handlePSCommand()
			return
		case "logs":
			handleLogsCommand()
			return
		case "stop":
			handleStopCommand()
			return
		case "restart":
			handleRestartCommand()
			return
		}
	}

	creds := LoadAllCredentials()
	if len(creds) == 0 {
		slog.Error("no credentials found", "hint", "set ANTHROPIC_API_KEY, OPENAI_API_KEY, or run `launchdock auth login claude`")
		os.Exit(1)
	}
	for _, c := range creds {
		slog.Info("loaded credential", "provider", c.Provider, "type", c.AuthType, "label", c.Label, "source", c.Source)
	}

	pool := NewPool(creds)
	anthropicProvider := &AnthropicProvider{}
	openaiProvider := &OpenAIProvider{}
	providers := []Provider{anthropicProvider, openaiProvider}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", HandleChatCompletions(pool, providers))
	mux.HandleFunc("/v1/messages", HandleMessages(pool, anthropicProvider))
	mux.HandleFunc("/v1/responses", HandleResponses(pool, openaiProvider))
	mux.HandleFunc("/v1/models", HandleModels(pool, anthropicProvider))
	mux.HandleFunc("/health", HandleHealth(pool))
	mux.HandleFunc("/", HandleHealth(pool))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      withCORS(withRequestID(mux)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	slog.Info("launchdock listening", "addr", ":"+port, "credentials", len(creds), "providers", pool.Providers())

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

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = shortID()
		}
		w.Header().Set("X-Request-ID", reqID)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "req_id", reqID)
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
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}
