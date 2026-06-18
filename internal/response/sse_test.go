package response_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/response"
)

func TestSSEWriter_WriteEvent(t *testing.T) {
	rr := httptest.NewRecorder()
	w := response.NewSSEWriter(rr)

	w.WriteEvent("content_block_delta", []byte(`{"type":"content_block_delta"}`))
	w.Flush()

	body := rr.Body.String()
	if !strings.Contains(body, "event: content_block_delta\n") {
		t.Errorf("missing event line, got: %q", body)
	}
	if !strings.Contains(body, `data: {"type":"content_block_delta"}`) {
		t.Errorf("missing data line, got: %q", body)
	}
	if !strings.Contains(body, "\n\n") {
		t.Errorf("missing event terminator, got: %q", body)
	}
}

func TestSSEWriter_WriteData(t *testing.T) {
	rr := httptest.NewRecorder()
	w := response.NewSSEWriter(rr)

	w.WriteData([]byte(`[DONE]`))
	w.Flush()

	body := rr.Body.String()
	if !strings.Contains(body, "data: [DONE]\n\n") {
		t.Errorf("unexpected body: %q", body)
	}
	if strings.Contains(body, "event:") {
		t.Errorf("unexpected event line in data-only write: %q", body)
	}
}

func TestSSEWriter_SetHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	w := response.NewSSEWriter(rr)
	w.SetHeaders()

	ct := rr.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want text/event-stream", ct)
	}
}
