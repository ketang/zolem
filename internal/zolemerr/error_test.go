package zolemerr_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zolem.dev/zolem/internal/zolemerr"
)

func TestWrite(t *testing.T) {
	rr := httptest.NewRecorder()
	zolemerr.Write(rr, "failed")

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	if rr.Header().Get("X-Zolem-Error") != "true" {
		t.Fatalf("X-Zolem-Error = %q", rr.Header().Get("X-Zolem-Error"))
	}
	var payload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["zolem_error"] != "failed" {
		t.Fatalf("zolem_error = %q", payload["zolem_error"])
	}
}
