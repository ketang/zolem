package anthropic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/ollama"
	"zolem.dev/zolem/internal/provider/anthropic"
	"zolem.dev/zolem/internal/response"
	runtimecfg "zolem.dev/zolem/internal/runtime"
	"zolem.dev/zolem/internal/specs"
)

type stubGenerator struct {
	text string
}

func (g stubGenerator) Generate(context.Context, string) (string, error) {
	return g.text, nil
}

type errorGenerator struct{}

func (g errorGenerator) Generate(context.Context, string) (string, error) {
	return "", errors.New("ollama generation failed")
}

type testGenerator interface {
	Generate(context.Context, string) (string, error)
}

func newHandler(t *testing.T) *anthropic.Handler {
	return newHandlerWithGenerator(t, nil)
}

func newHandlerWithGenerator(t *testing.T, generator testGenerator) *anthropic.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	return anthropic.NewHandler(validator, matcher, lorem, generator, nil)
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

func TestMessages_OllamaFallback_NonStreaming(t *testing.T) {
	h := newHandlerWithGenerator(t, stubGenerator{text: "hello from ollama"})
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	var resp anthropic.MessagesResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "hello from ollama" {
		t.Fatalf("content: got %#v", resp.Content)
	}
}

func TestMessages_OllamaFallback_Streaming(t *testing.T) {
	h := newHandlerWithGenerator(t, stubGenerator{text: "streamed from ollama"})
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "streamed") {
		t.Fatalf("expected ollama text in stream, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, "event: message_stop") {
		t.Fatalf("missing message_stop event, got:\n%s", responseBody)
	}
}

func TestMessages_OllamaError_FallsBackToLorem(t *testing.T) {
	h := newHandlerWithGenerator(t, errorGenerator{})
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	var resp anthropic.MessagesResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != "msg_zolem_generated" {
		t.Fatalf("expected lorem fallback (msg_zolem_generated), got ID %q", resp.ID)
	}
}

type stubChatGenerator struct {
	text string
	err  error
}

func (g *stubChatGenerator) NonStreaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string) (string, error) {
	return g.text, g.err
}

func (g *stubChatGenerator) Streaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string, fn func(string) error) error {
	if g.err != nil {
		return g.err
	}
	for _, word := range strings.Fields(g.text) {
		if err := fn(word + " "); err != nil {
			return err
		}
	}
	return nil
}

func TestMessages_OllamaBackend_NonStreaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Ollama says hello"}
	h := anthropic.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			Name:           "test",
			Backend:        "ollama",
			OllamaUpstream: "http://localhost:11434",
		},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp anthropic.MessagesResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Content) == 0 || resp.Content[0].Text != "Ollama says hello" {
		t.Fatalf("unexpected response content: %+v", resp.Content)
	}
}

func TestMessages_OllamaBackend_Error(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{err: errors.New("connection refused")}
	h := anthropic.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502. body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["type"] != "error" {
		t.Fatalf("expected error response, got: %+v", resp)
	}
}

func TestMessages_OllamaBackend_Streaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Hello world"}
	h := anthropic.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "content_block_delta") {
		t.Fatalf("expected SSE content_block_delta events, got: %s", responseBody)
	}
	if !strings.Contains(responseBody, "Hello") {
		t.Fatalf("expected 'Hello' in streaming response, got: %s", responseBody)
	}
	if !strings.Contains(responseBody, "message_stop") {
		t.Fatalf("expected message_stop event, got: %s", responseBody)
	}
}
