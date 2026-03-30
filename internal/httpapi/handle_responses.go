package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// HandleResponses handles POST /v1/responses (OpenAI Responses API passthrough)
// Codex CLI and other Responses API clients hit this endpoint.
// The mux adds credential auth, then forwards to OpenAI.
func HandleResponses(pool *Pool, openai *OpenAIProvider, anthropic *AnthropicProvider) http.HandlerFunc {
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

		if strings.HasPrefix(peek.Model, "claude") {
			handleClaudeResponses(w, r, body, &peek, pool, anthropic)
			return
		}

		credMatcher := func(c *Credential) bool {
			return c.Provider == "openai" && c.Kind == "codex_chatgpt"
		}

		// Pick credential compatible with ChatGPT Codex backend.
		cred, err := pool.PickMatching("openai", nil, credMatcher)
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
			body = ensureResponsesInputList(body)
		}

		upResp, cred, err := ensureOKOrRetryMatching(pool, "openai", cred, credMatcher, func(current *Credential) (*http.Response, error) {
			var upstreamURL string
			requestBody := body
			if current.AuthType == AuthOAuth {
				upstreamURL = openai.ChatGPTBaseURL() + "/responses"
				if !peek.Stream {
					requestBody = ensureResponsesStream(body)
				}
			} else {
				upstreamURL = openai.BaseURL() + "/v1/responses"
			}
			upReq, err := http.NewRequestWithContext(r.Context(), "POST",
				upstreamURL, bytes.NewReader(requestBody))
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
			if cred.AuthType == AuthOAuth {
				relayResponsesCollectedNonStream(w, upResp)
			} else {
				relayResponsesNonStream(w, upResp)
			}
		}
	}
}

func handleClaudeResponses(w http.ResponseWriter, r *http.Request, body []byte, peek *struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}, pool *Pool, anthropic *AnthropicProvider) {
	chatReq, err := responsesToChatRequest(body)
	if err != nil {
		httpError(w, http.StatusBadRequest, "translate responses: "+err.Error())
		return
	}
	applyClaudeThinkingAlias(chatReq)
	chatReq.Stream = true

	cred, err := pool.Pick("anthropic")
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	upstreamBody, _, err := anthropic.TranslateRequest(chatReq)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "translate: "+err.Error())
		return
	}
	if cred.AuthType == AuthOAuth {
		upstreamBody, _ = PrefixTools(upstreamBody, "mcp_")
		upstreamBody, _ = EnsureOAuthRequirements(upstreamBody)
	}

	upResp, cred, err := sendWithRetry(r, anthropic, pool, cred, chatReq.Model, upstreamBody, "/v1/messages")
	if err != nil {
		httpError(w, http.StatusBadGateway, "upstream: "+err.Error())
		return
	}
	defer upResp.Body.Close()
	if upResp.StatusCode != http.StatusOK {
		handleUpstreamError(w, upResp, pool, cred)
		return
	}

	if peek.Stream {
		relayClaudeResponsesStream(w, upResp, chatReq.Model)
		return
	}
	respObj, err := collectClaudeResponses(upResp.Body, chatReq.Model)
	if err != nil {
		httpError(w, http.StatusBadGateway, "collect responses: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(respObj)
}

func responsesToChatRequest(body []byte) (*ChatRequest, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	chat := &ChatRequest{Model: asString(req["model"]), Stream: true}
	if inputStr, ok := req["input"].(string); ok && inputStr != "" {
		chat.Messages = append(chat.Messages, ChatMessage{Role: "user", Content: inputStr})
	}
	if input, ok := req["input"].([]any); ok {
		for _, item := range input {
			m, _ := item.(map[string]any)
			if m == nil {
				if s, ok := item.(string); ok && s != "" {
					chat.Messages = append(chat.Messages, ChatMessage{Role: "user", Content: s})
				}
				continue
			}
			role, _ := m["role"].(string)
			if role == "" {
				role = "user"
			}
			content := m["content"]
			if parts, ok := content.([]any); ok {
				var texts []string
				for _, p := range parts {
					pm, _ := p.(map[string]any)
					if pm == nil {
						continue
					}
					if t, _ := pm["type"].(string); t == "input_text" || t == "output_text" {
						if s, _ := pm["text"].(string); s != "" {
							texts = append(texts, s)
						}
					}
				}
				content = strings.Join(texts, "")
			}
			chat.Messages = append(chat.Messages, ChatMessage{Role: role, Content: content})
		}
	}
	if len(chat.Messages) == 0 {
		return nil, fmt.Errorf("messages: Input should be a valid list")
	}
	if tools, ok := req["tools"].([]any); ok {
		for _, item := range tools {
			m, _ := item.(map[string]any)
			if m == nil {
				continue
			}
			typ, _ := m["type"].(string)
			if typ != "function" {
				continue
			}
			tool := ChatTool{Type: "function"}
			if name, _ := m["name"].(string); name != "" {
				tool.Function.Name = name
			}
			if desc, _ := m["description"].(string); desc != "" {
				tool.Function.Description = desc
			}
			if params, ok := m["parameters"]; ok {
				if b, err := json.Marshal(params); err == nil {
					tool.Function.Parameters = b
				}
			}
			if tool.Function.Name != "" {
				chat.Tools = append(chat.Tools, tool)
			}
		}
	}
	if tc, ok := req["tool_choice"]; ok {
		chat.ToolChoice = tc
	}
	if instructions, _ := req["instructions"].(string); instructions != "" {
		chat.Messages = append([]ChatMessage{{Role: "system", Content: instructions}}, chat.Messages...)
	}
	return chat, nil
}

func relayClaudeResponsesStream(w http.ResponseWriter, upResp *http.Response, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	state := newClaudeResponsesState(model)

	writeEvent := func(event string, payload map[string]any) {
		payload["sequence_number"] = state.sequence
		state.sequence++
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b))
		flusher.Flush()
	}

	writeEvent("response.created", map[string]any{"type": "response.created", "response": state.baseResponse("in_progress")})
	writeEvent("response.in_progress", map[string]any{"type": "response.in_progress", "response": state.baseResponse("in_progress")})

	ReadSSE(upResp.Body, func(ev SSEEvent) error {
		for _, out := range state.consumeEvent(ev) {
			writeEvent(out.event, out.payload)
		}
		return nil
	})
}

type claudeResponsesState struct {
	model            string
	respID           string
	created          int64
	sequence         int
	reasoningID      string
	messageID        string
	toolCallID       string
	toolCallName     string
	reasoningStarted bool
	messageStarted   bool
	toolStarted      bool
	reasoningText    strings.Builder
	outputText       strings.Builder
	toolArgs         strings.Builder
}

type responsesEventOut struct {
	event   string
	payload map[string]any
}

func newClaudeResponsesState(model string) *claudeResponsesState {
	return &claudeResponsesState{model: model, respID: fmt.Sprintf("resp_%d", time.Now().UnixNano()), created: time.Now().Unix(), reasoningID: fmt.Sprintf("rs_%d", time.Now().UnixNano()), messageID: fmt.Sprintf("msg_%d", time.Now().UnixNano())}
}

func (s *claudeResponsesState) baseResponse(status string) map[string]any {
	return map[string]any{"id": s.respID, "object": "response", "created_at": s.created, "status": status, "model": s.model, "output": []any{}}
}

func (s *claudeResponsesState) consumeEvent(ev SSEEvent) []responsesEventOut {
	var outs []responsesEventOut
	switch ev.Event {
	case "content_block_start":
		var cbs ClaudeContentBlockStart
		if json.Unmarshal([]byte(ev.Data), &cbs) != nil {
			return nil
		}
		if cbs.ContentBlock.Type == "tool_use" {
			s.toolStarted = true
			s.toolCallID = cbs.ContentBlock.ID
			s.toolCallName = stripPrefix(cbs.ContentBlock.Name, "mcp_")
			outs = append(outs, responsesEventOut{"response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": s.toolCallID, "type": "function_call", "call_id": s.toolCallID, "name": s.toolCallName, "arguments": "", "status": "in_progress"}, "output_index": 0}})
		}
	case "content_block_delta":
		var cbd ClaudeContentBlockDelta
		if json.Unmarshal([]byte(ev.Data), &cbd) != nil {
			return nil
		}
		switch cbd.Delta.Type {
		case "thinking_delta":
			if cbd.Delta.Thinking == "" {
				return nil
			}
			if !s.reasoningStarted {
				s.reasoningStarted = true
				outs = append(outs,
					responsesEventOut{"response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": s.reasoningID, "type": "reasoning", "summary": []any{}}, "output_index": 0}},
					responsesEventOut{"response.reasoning_summary_part.added", map[string]any{"type": "response.reasoning_summary_part.added", "item_id": s.reasoningID, "output_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}, "summary_index": 0}},
				)
			}
			s.reasoningText.WriteString(cbd.Delta.Thinking)
			outs = append(outs, responsesEventOut{"response.reasoning_summary_text.delta", map[string]any{"type": "response.reasoning_summary_text.delta", "delta": cbd.Delta.Thinking, "item_id": s.reasoningID, "output_index": 0, "summary_index": 0}})
		case "text_delta":
			if cbd.Delta.Text == "" {
				return nil
			}
			if !s.messageStarted {
				s.messageStarted = true
				idx := 0
				if s.reasoningStarted {
					idx = 1
				}
				outs = append(outs,
					responsesEventOut{"response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": s.messageID, "type": "message", "status": "in_progress", "content": []any{}, "phase": "final_answer", "role": "assistant"}, "output_index": idx}},
					responsesEventOut{"response.content_part.added", map[string]any{"type": "response.content_part.added", "content_index": 0, "item_id": s.messageID, "output_index": idx, "part": map[string]any{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": ""}}},
				)
			}
			s.outputText.WriteString(cbd.Delta.Text)
			idx := 0
			if s.reasoningStarted {
				idx = 1
			}
			outs = append(outs, responsesEventOut{"response.output_text.delta", map[string]any{"type": "response.output_text.delta", "content_index": 0, "delta": cbd.Delta.Text, "item_id": s.messageID, "logprobs": []any{}, "output_index": idx}})
		case "input_json_delta":
			if !s.toolStarted || cbd.Delta.PartialJSON == "" {
				return nil
			}
			s.toolArgs.WriteString(cbd.Delta.PartialJSON)
			outs = append(outs, responsesEventOut{"response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "delta": cbd.Delta.PartialJSON, "item_id": s.toolCallID, "output_index": 0}})
		}
	case "message_stop":
		if s.toolStarted {
			args := s.toolArgs.String()
			outs = append(outs, responsesEventOut{"response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "arguments": args, "item_id": s.toolCallID, "output_index": 0}})
			outs = append(outs, responsesEventOut{"response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": s.toolCallID, "type": "function_call", "call_id": s.toolCallID, "name": s.toolCallName, "arguments": args, "status": "completed"}, "output_index": 0}})
		}
		if s.reasoningStarted {
			text := s.reasoningText.String()
			outs = append(outs,
				responsesEventOut{"response.reasoning_summary_text.done", map[string]any{"type": "response.reasoning_summary_text.done", "item_id": s.reasoningID, "output_index": 0, "summary_index": 0, "text": text}},
				responsesEventOut{"response.reasoning_summary_part.done", map[string]any{"type": "response.reasoning_summary_part.done", "item_id": s.reasoningID, "output_index": 0, "part": map[string]any{"type": "summary_text", "text": text}, "summary_index": 0}},
				responsesEventOut{"response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": s.reasoningID, "type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": text}}}, "output_index": 0}},
			)
		}
		if s.messageStarted {
			idx := 0
			if s.reasoningStarted {
				idx = 1
			}
			text := s.outputText.String()
			outs = append(outs,
				responsesEventOut{"response.output_text.done", map[string]any{"type": "response.output_text.done", "content_index": 0, "item_id": s.messageID, "logprobs": []any{}, "output_index": idx, "text": text}},
				responsesEventOut{"response.content_part.done", map[string]any{"type": "response.content_part.done", "content_index": 0, "item_id": s.messageID, "output_index": idx, "part": map[string]any{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": text}}},
				responsesEventOut{"response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": s.messageID, "type": "message", "status": "completed", "content": []any{map[string]any{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": text}}, "phase": "final_answer", "role": "assistant"}, "output_index": idx}},
			)
		}
		outs = append(outs, responsesEventOut{"response.completed", map[string]any{"type": "response.completed", "response": s.finalResponse()}})
	}
	return outs
}

func (s *claudeResponsesState) finalResponse() map[string]any {
	usage := map[string]any{"input_tokens": 0, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": 0, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": 0}
	output := buildClaudeResponseOutput(s.reasoningStarted, s.reasoningID, s.reasoningText.String(), s.messageStarted, s.messageID, s.outputText.String())
	if s.toolStarted {
		output = append(output, map[string]any{"id": s.toolCallID, "type": "function_call", "call_id": s.toolCallID, "name": s.toolCallName, "arguments": s.toolArgs.String(), "status": "completed"})
	}
	return map[string]any{"id": s.respID, "object": "response", "created_at": s.created, "status": "completed", "model": s.model, "output": output, "reasoning": map[string]any{"effort": "high", "summary": "detailed"}, "usage": usage}
}

func collectClaudeResponses(r io.Reader, model string) (map[string]any, error) {
	state := newClaudeResponsesState(model)
	err := ReadSSE(r, func(ev SSEEvent) error {
		state.consumeEvent(ev)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state.finalResponse(), nil
}

func buildClaudeResponseOutput(hasReasoning bool, reasoningID, reasoningText string, hasMessage bool, messageID, outputText string) []any {
	var out []any
	if hasReasoning {
		out = append(out, map[string]any{"id": reasoningID, "type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": reasoningText}}})
	}
	if hasMessage {
		out = append(out, map[string]any{"id": messageID, "type": "message", "status": "completed", "content": []any{map[string]any{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": outputText}}, "phase": "final_answer", "role": "assistant"})
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
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

func ensureResponsesStream(body []byte) []byte {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	req["stream"] = true
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

func ensureResponsesInputList(body []byte) []byte {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}

	input, ok := req["input"]
	if !ok || input == nil {
		return body
	}

	switch v := input.(type) {
	case []any:
		return body
	case string:
		req["input"] = []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": v,
			}},
		}}
	case map[string]any:
		if _, hasRole := v["role"]; hasRole {
			req["input"] = []any{v}
		} else if text, _ := v["text"].(string); text != "" {
			req["input"] = []map[string]any{{
				"role": "user",
				"content": []map[string]any{{
					"type": "input_text",
					"text": text,
				}},
			}}
		}
	default:
		return body
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

func relayResponsesCollectedNonStream(w http.ResponseWriter, upResp *http.Response) {
	var finalResponse any
	ReadSSE(upResp.Body, func(ev SSEEvent) error {
		var obj map[string]any
		if json.Unmarshal([]byte(ev.Data), &obj) != nil {
			return nil
		}
		if typ, _ := obj["type"].(string); typ == "response.completed" {
			finalResponse = obj["response"]
		}
		return nil
	})
	if finalResponse == nil {
		httpError(w, http.StatusBadGateway, "missing response.completed event")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if v := upResp.Header.Get("x-codex-turn-state"); v != "" {
		w.Header().Set("x-codex-turn-state", v)
	}
	json.NewEncoder(w).Encode(finalResponse)
}
