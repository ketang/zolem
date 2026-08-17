package response

import (
	"encoding/json"
	"net/http"
)

// NDJSONWriter writes newline-delimited JSON to an http.ResponseWriter.
//
// It is the streaming transport for providers that do not use SSE. Ollama's
// native API streams bare JSON objects separated by newlines: there is no
// "data: " prefix and no [DONE] sentinel, and the stream ends when an object
// carries "done": true.
type NDJSONWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewNDJSONWriter(w http.ResponseWriter) *NDJSONWriter {
	f, _ := w.(http.Flusher)
	return &NDJSONWriter{w: w, flusher: f}
}

// SetHeaders writes the required NDJSON streaming response headers.
func (n *NDJSONWriter) SetHeaders() {
	n.w.Header().Set("Content-Type", "application/x-ndjson")
	n.w.Header().Set("Cache-Control", "no-cache")
	n.w.Header().Set("X-Accel-Buffering", "no")
}

// WriteObject marshals v, writes it as a single newline-terminated line, and
// flushes. A marshal failure writes nothing, so a partial object can never
// corrupt the stream framing.
func (n *NDJSONWriter) WriteObject(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := n.w.Write(append(data, '\n')); err != nil {
		return err
	}
	n.Flush()
	return nil
}

// Flush flushes the underlying ResponseWriter if it supports flushing.
func (n *NDJSONWriter) Flush() {
	if n.flusher != nil {
		n.flusher.Flush()
	}
}
