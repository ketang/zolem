package anthropic_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/provider/anthropic"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/specs"
)

func newHandler(t *testing.T) *anthropic.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	return anthropic.NewHandler(validator, matcher, lorem)
}

func TestMessages_MissingAuthHeader(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["type"] != "error" {
		t.Errorf("response type: got %v, want error", resp["type"])
	}
}

func TestMessages_LoremResponse_NonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["type"] != "message" {
		t.Errorf("response type: got %v, want message", resp["type"])
	}
	if resp["role"] != "assistant" {
		t.Errorf("role: got %v, want assistant", resp["role"])
	}
}

func TestMessages_LoremResponse_Streaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "event: message_start") {
		t.Errorf("missing message_start event, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, "event: message_stop") {
		t.Errorf("missing message_stop event, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, "event: content_block_delta") {
		t.Errorf("missing content_block_delta, got:\n%s", responseBody)
	}
}
