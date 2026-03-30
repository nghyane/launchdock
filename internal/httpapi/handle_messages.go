package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// HandleMessages handles POST /v1/messages (Claude Messages API passthrough)
// Clients that speak Claude format natively hit this endpoint.
// The mux adds credential auth + OAuth quirks, then forwards to Anthropic.
func HandleMessages(pool *Pool, anthropic *AnthropicProvider) http.HandlerFunc {
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

		// Extract model for logging and header building
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
		cred, err := pool.Pick("anthropic")
		if err != nil {
			httpError(w, http.StatusServiceUnavailable, err.Error())
			return
		}

		slog.Info("routing messages",
			"model", peek.Model,
			"credential", cred.Label,
			"stream", peek.Stream,
		)

		// Apply OAuth quirks if needed
		if cred.AuthType == AuthOAuth {
			body, _ = PrefixTools(body, "mcp_")
			body, _ = EnsureOAuthRequirements(body)
		}

		upResp, cred, err := ensureOKOrRetry(pool, "anthropic", cred, func(current *Credential) (*http.Response, error) {
			upReq, err := http.NewRequestWithContext(r.Context(), "POST",
				anthropic.BaseURL()+"/v1/messages", bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			anthropic.PrepareWithModel(upReq, current, peek.Model)
			return StreamClient.Do(upReq)
		})
		if err != nil {
			httpError(w, http.StatusBadGateway, "upstream: "+err.Error())
			return
		}
		defer upResp.Body.Close()

		if upResp.StatusCode != http.StatusOK {
			logAnthropicRequestSummary("messages", body)
		}

		if peek.Stream {
			relayClaudeMessagesStream(w, upResp, cred)
		} else {
			relayClaudeMessagesNonStream(w, upResp, cred)
		}
	}
}

// relayClaudeMessagesStream forwards Claude SSE as-is, only stripping mcp_ prefix.
func relayClaudeMessagesStream(w http.ResponseWriter, upResp *http.Response, cred *Credential) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Copy upstream headers
	for _, h := range []string{"Content-Type", "Cache-Control"} {
		if v := upResp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	isOAuth := cred.AuthType == AuthOAuth

	ReadSSE(upResp.Body, func(ev SSEEvent) error {
		data := ev.Data
		// Strip mcp_ prefix from tool names in SSE data
		if isOAuth {
			data = string(StripToolPrefix([]byte(data), "mcp_"))
		}

		if ev.Event != "" {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, data)
		} else {
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		flusher.Flush()
		return nil
	})
}

// relayClaudeMessagesNonStream forwards the Claude JSON response, stripping mcp_ prefix.
func relayClaudeMessagesNonStream(w http.ResponseWriter, upResp *http.Response, cred *Credential) {
	body, err := io.ReadAll(upResp.Body)
	if err != nil {
		httpError(w, http.StatusBadGateway, "read upstream: "+err.Error())
		return
	}

	if cred.AuthType == AuthOAuth {
		body = StripToolPrefix(body, "mcp_")
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
