package openai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/provider/openai"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/specs"
)

func newHandler(t *testing.T) *openai.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return openai.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator())
}

func TestChatCompletions_MissingAuth(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestChatCompletions_LoremNonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["object"] != "chat.completion" {
		t.Errorf("object: got %v, want chat.completion", resp["object"])
	}
}

func TestChatCompletions_LoremStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	body2 := rr.Body.String()
	if !strings.Contains(body2, "chat.completion.chunk") {
		t.Errorf("expected chunk objects, got:\n%s", body2)
	}
	if !strings.Contains(body2, "data: [DONE]") {
		t.Errorf("missing [DONE] terminator, got:\n%s", body2)
	}
}
