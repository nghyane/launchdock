package launchdock

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SSE helpers for reading upstream and writing downstream

// SSEWriter wraps an http.ResponseWriter for streaming SSE events.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return &SSEWriter{w: w, flusher: flusher}, true
}

func (s *SSEWriter) WriteData(data string) {
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.flusher.Flush()
}

func (s *SSEWriter) WriteJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.WriteData(string(b))
	return nil
}

func (s *SSEWriter) WriteDone() {
	fmt.Fprint(s.w, "data: [DONE]\n\n")
	s.flusher.Flush()
}

func (s *SSEWriter) WriteEvent(event, data string) {
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	s.flusher.Flush()
}

// SSEReader reads SSE events from a stream.
// It yields (event_type, data) pairs.
type SSEEvent struct {
	Event string // event type (empty if not specified)
	Data  string // data payload
}

func ReadSSE(r io.Reader, fn func(SSEEvent) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line

	var event string
	var data strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = event boundary
			if data.Len() > 0 {
				d := data.String()
				if strings.HasSuffix(d, "\n") {
					d = d[:len(d)-1]
				}
				if err := fn(SSEEvent{Event: event, Data: d}); err != nil {
					return err
				}
				data.Reset()
				event = ""
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			event = line[7:]
		} else if strings.HasPrefix(line, "data: ") {
			data.WriteString(line[6:])
			data.WriteByte('\n')
		} else if line == "data:" {
			data.WriteByte('\n')
		}
		// Ignore comments (lines starting with :) and unknown fields
	}

	return scanner.Err()
}
