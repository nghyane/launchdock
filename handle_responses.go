package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// HandleResponses handles POST /v1/responses (OpenAI Responses API passthrough)
// Codex CLI and other Responses API clients hit this endpoint.
// The mux adds credential auth, then forwards to OpenAI.
func HandleResponses(pool *Pool, openai *OpenAIProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			httpError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}

		// Extract model for logging
		var peek struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		json.Unmarshal(body, &peek)

		if peek.Model == "" {
			httpError(w, http.StatusBadRequest, "model is required")
			return
		}

		// Pick credential
		cred, err := pool.Pick("openai")
		if err != nil {
			httpError(w, http.StatusServiceUnavailable, err.Error())
			return
		}

		slog.Info("routing responses",
			"model", peek.Model,
			"credential", cred.Label,
			"stream", peek.Stream,
		)

		// ChatGPT backend requires "instructions" field
		if cred.AuthType == AuthOAuth {
			body = ensureResponsesInstructions(body)
		}

		upResp, cred, err := ensureOKOrRetry(pool, "openai", cred, func(current *Credential) (*http.Response, error) {
			var upstreamURL string
			if current.AuthType == AuthOAuth {
				upstreamURL = openai.ChatGPTBaseURL() + "/responses"
			} else {
				upstreamURL = openai.BaseURL() + "/v1/responses"
			}
			upReq, err := http.NewRequestWithContext(r.Context(), "POST",
				upstreamURL, bytes.NewReader(body))
			if err != nil {
				return nil, err
			}

			openai.Prepare(upReq, current)

			for _, h := range []string{
				"x-codex-turn-state",
				"x-codex-turn-metadata",
				"x-codex-beta-features",
				"x-client-request-id",
				"OpenAI-Beta",
			} {
				if v := r.Header.Get(h); v != "" {
					upReq.Header.Set(h, v)
				}
			}

			return StreamClient.Do(upReq)
		})
		if err != nil {
			httpError(w, http.StatusBadGateway, "upstream: "+err.Error())
			return
		}
		defer upResp.Body.Close()

		if peek.Stream {
			relayResponsesStream(w, upResp)
		} else {
			relayResponsesNonStream(w, upResp)
		}
	}
}

// ensureResponsesInstructions adds default instructions if missing.
// ChatGPT backend requires this field.
func ensureResponsesInstructions(body []byte) []byte {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	if _, ok := req["instructions"]; !ok {
		req["instructions"] = "You are a helpful assistant."
	}
	req["store"] = false

	// Codex-optimal defaults
	if _, ok := req["tool_choice"]; !ok {
		if tools, ok := req["tools"]; ok {
			if toolsArr, ok := tools.([]any); ok && len(toolsArr) > 0 {
				req["tool_choice"] = "auto"
				req["parallel_tool_calls"] = true
			}
		}
	}
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

// relayResponsesStream forwards Responses API SSE as-is.
func relayResponsesStream(w http.ResponseWriter, upResp *http.Response) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Copy response headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Forward x-codex-turn-state from upstream response
	if v := upResp.Header.Get("x-codex-turn-state"); v != "" {
		w.Header().Set("x-codex-turn-state", v)
	}

	w.WriteHeader(http.StatusOK)

	// Passthrough SSE — no translation needed
	ReadSSE(upResp.Body, func(ev SSEEvent) error {
		if ev.Event != "" {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
		} else {
			fmt.Fprintf(w, "data: %s\n\n", ev.Data)
		}
		flusher.Flush()
		return nil
	})
}

// relayResponsesNonStream forwards the JSON response as-is.
func relayResponsesNonStream(w http.ResponseWriter, upResp *http.Response) {
	body, err := io.ReadAll(upResp.Body)
	if err != nil {
		httpError(w, http.StatusBadGateway, "read upstream: "+err.Error())
		return
	}

	// Forward response headers
	w.Header().Set("Content-Type", "application/json")
	if v := upResp.Header.Get("x-codex-turn-state"); v != "" {
		w.Header().Set("x-codex-turn-state", v)
	}
	w.Write(body)
}
