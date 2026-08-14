package response_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/response"
)

func TestNDJSONWriter_SetHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	n := response.NewNDJSONWriter(rr)
	n.SetHeaders()

	if got := rr.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("content-type: got %q, want application/x-ndjson", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("cache-control: got %q, want no-cache", got)
	}
	if got := rr.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("x-accel-buffering: got %q, want no", got)
	}
}

// TestNDJSONWriter_OneObjectPerLine pins the framing that distinguishes NDJSON
// from SSE: bare JSON objects separated by newlines, with no "data: " prefix
// and no terminating [DONE] sentinel.
func TestNDJSONWriter_OneObjectPerLine(t *testing.T) {
	rr := httptest.NewRecorder()
	n := response.NewNDJSONWriter(rr)

	for _, v := range []map[string]any{
		{"content": "a", "done": false},
		{"content": "b", "done": false},
		{"content": "", "done": true},
	} {
		if err := n.WriteObject(v); err != nil {
			t.Fatalf("WriteObject: %v", err)
		}
	}

	body := rr.Body.String()
	if strings.Contains(body, "data: ") {
		t.Errorf("NDJSON must not use the SSE 'data: ' prefix; got:\n%s", body)
	}
	if strings.Contains(body, "[DONE]") {
		t.Errorf("NDJSON must not emit an SSE [DONE] sentinel; got:\n%s", body)
	}
	if !strings.HasSuffix(body, "\n") {
		t.Errorf("NDJSON must end with a newline; got %q", body)
	}

	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("line count: got %d, want 3. body:\n%s", len(lines), body)
	}
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d is not a bare JSON object: %v (%q)", i, err, line)
		}
	}

	var last map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil {
		t.Fatalf("decode final line: %v", err)
	}
	if last["done"] != true {
		t.Errorf("final line must carry done=true, got %v", last["done"])
	}
}

// flushRecorder counts Flush calls so we can assert each object is pushed to
// the client immediately rather than buffered until the handler returns.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++; f.ResponseRecorder.Flush() }

func TestNDJSONWriter_FlushesEachObject(t *testing.T) {
	fr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	var w http.ResponseWriter = fr
	n := response.NewNDJSONWriter(w)

	for i := range 3 {
		if err := n.WriteObject(map[string]any{"i": i}); err != nil {
			t.Fatalf("WriteObject: %v", err)
		}
	}

	if fr.flushes != 3 {
		t.Errorf("flushes: got %d, want 3 (one per object)", fr.flushes)
	}
}

func TestNDJSONWriter_WriteObjectReportsMarshalError(t *testing.T) {
	rr := httptest.NewRecorder()
	n := response.NewNDJSONWriter(rr)

	// Channels are not JSON-marshalable.
	if err := n.WriteObject(map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("expected a marshal error, got nil")
	}
	if rr.Body.Len() != 0 {
		t.Errorf("nothing should be written on marshal failure, got %q", rr.Body.String())
	}
}
