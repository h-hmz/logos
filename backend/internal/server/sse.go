// Package server provides the HTTP handlers for the Logos Arena API.
package server

import (
	"encoding/json"
	"fmt"
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
}

func newSSEWriter(w http.ResponseWriter) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return &sseWriter{w: w, flusher: f}, true
}

// SendToken sends an SSE "token" event with a text chunk.
func (s *sseWriter) SendToken(text string) {
	data, _ := json.Marshal(map[string]string{"text": text})
	fmt.Fprintf(s.w, "event: token\ndata: %s\n\n", data)
	s.flusher.Flush()
}

// SendDone sends an SSE "done" event with the final JSON payload.
func (s *sseWriter) SendDone(payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(s.w, "event: done\ndata: %s\n\n", data)
	s.flusher.Flush()
}

// SendError sends an SSE "error" event.
func (s *sseWriter) SendError(msg string) {
	data, _ := json.Marshal(map[string]string{"error": msg})
	fmt.Fprintf(s.w, "event: error\ndata: %s\n\n", data)
	s.flusher.Flush()
}
