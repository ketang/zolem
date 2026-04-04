package gemini_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/provider/gemini"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/specs"
)

func newHandler(t *testing.T) *gemini.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return gemini.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator())
}

func TestGenerateContent_MissingAuth(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error field")
	}
}

func TestGenerateContent_LoremNonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["candidates"]; !ok {
		t.Error("expected candidates field")
	}
}

func TestStreamGenerateContent_SSE(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:streamGenerateContent?alt=sse", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "candidates") {
		t.Errorf("expected candidates in SSE stream, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, `"finishReason":"STOP"`) {
		t.Errorf("expected STOP in final chunk, got:\n%s", responseBody)
	}
}
