package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// wantsSSE returns true if the client accepts text/event-stream.
func wantsSSE(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// sseWriter wraps an http.ResponseWriter for Server-Sent Events.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context
}

func newSSEWriter(ctx context.Context, w http.ResponseWriter) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return &sseWriter{w: w, flusher: f, ctx: ctx}, true
}

// sendEvent marshals payload and writes it as the named SSE event.
// Marshal failures are logged; a best-effort fallback event is still sent so
// the client observes the event boundary rather than silently stalling.
func (s *sseWriter) sendEvent(event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.ErrorContext(s.ctx, "sse marshal failed", "event", event, "error", err)
		data = []byte(`{"error":"marshal failed"}`)
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	s.flusher.Flush()
}

// SendToken sends an SSE "token" event with a text chunk.
func (s *sseWriter) SendToken(text string) {
	s.sendEvent("token", map[string]string{"text": text})
}

// SendDone sends an SSE "done" event with the final JSON payload.
func (s *sseWriter) SendDone(payload any) {
	s.sendEvent("done", payload)
}

// SendError sends an SSE "error" event.
func (s *sseWriter) SendError(msg string) {
	s.sendEvent("error", map[string]string{"error": msg})
}
