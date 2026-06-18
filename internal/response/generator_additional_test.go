package response_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ketang/zolem/internal/response"
)

func TestCountNonEmpty(t *testing.T) {
	if got := response.CountNonEmpty([]string{"a", "", "b", " "}); got != 3 {
		t.Fatalf("CountNonEmpty = %d, want 3", got)
	}
}

func TestWriteZolemError(t *testing.T) {
	rr := httptest.NewRecorder()
	response.WriteZolemError(rr, "boom")

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	if rr.Header().Get("X-Zolem-Error") != "true" {
		t.Fatalf("X-Zolem-Error = %q", rr.Header().Get("X-Zolem-Error"))
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", rr.Header().Get("Content-Type"))
	}
	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["zolem_error"] != "boom" {
		t.Fatalf("zolem_error = %q", payload["zolem_error"])
	}
}
