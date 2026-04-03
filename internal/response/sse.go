package response

import (
	"fmt"
	"net/http"
)

// SSEWriter writes Server-Sent Events to an http.ResponseWriter.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	f, _ := w.(http.Flusher)
	return &SSEWriter{w: w, flusher: f}
}

// SetHeaders writes the required SSE response headers.
func (s *SSEWriter) SetHeaders() {
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("Connection", "keep-alive")
	s.w.Header().Set("X-Accel-Buffering", "no")
}

// WriteEvent writes a named SSE event with a data payload.
func (s *SSEWriter) WriteEvent(name string, data []byte) {
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data)
}

// WriteData writes a data-only SSE event (no event: line).
func (s *SSEWriter) WriteData(data []byte) {
	fmt.Fprintf(s.w, "data: %s\n\n", data)
}

// Flush flushes the underlying ResponseWriter if it supports flushing.
func (s *SSEWriter) Flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}
