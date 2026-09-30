package fixture

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// WriteVerbatim writes a non-streamed fixture body exactly as authored, with
// the fixture's status and a JSON content type. The only rewrite is the model
// field: when status is 2xx and body is a JSON object that already carries
// modelKey, that key's value is replaced with model. Every other key (including
// ones no response struct models) survives, and error bodies are never touched.
func WriteVerbatim(w http.ResponseWriter, status int, body []byte, modelKey, model string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(PatchModel(status, body, modelKey, model))
}

// PatchModel returns body with modelKey's value replaced by model when status
// is 2xx and body is a JSON object containing that key; otherwise body is
// returned unchanged.
func PatchModel(status int, body []byte, modelKey, model string) []byte {
	if status < 200 || status > 299 {
		return body
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return body
	}
	if _, ok := obj[modelKey]; !ok {
		return body
	}
	var mb bytes.Buffer
	menc := json.NewEncoder(&mb)
	menc.SetEscapeHTML(false)
	if err := menc.Encode(model); err != nil {
		return body
	}
	obj[modelKey] = bytes.TrimSuffix(mb.Bytes(), []byte("\n"))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return body
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}
