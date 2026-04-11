package openai_test

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
	"zolem.dev/zolem/internal/provider/openai"
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

func newHandler(t *testing.T) *openai.Handler {
	return newHandlerWithGenerator(t, nil)
}

func newHandlerWithGenerator(t *testing.T, generator testGenerator) *openai.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return openai.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator(), generator, nil)
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

func TestChatCompletions_OllamaFallback_NonStreaming(t *testing.T) {
	h := newHandlerWithGenerator(t, stubGenerator{text: "hello from ollama"})
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	var resp openai.ChatCompletionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := resp.Choices[0].Message.Content; got != "hello from ollama" {
		t.Fatalf("content: got %q, want hello from ollama", got)
	}
}

func TestChatCompletions_OllamaFallback_Streaming(t *testing.T) {
	h := newHandlerWithGenerator(t, stubGenerator{text: "streamed from ollama"})
	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "streamed") {
		t.Fatalf("expected ollama text in stream, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, "data: [DONE]") {
		t.Fatalf("missing [DONE] terminator, got:\n%s", responseBody)
	}
}

func TestChatCompletions_OllamaError_FallsBackToLorem(t *testing.T) {
	h := newHandlerWithGenerator(t, errorGenerator{})
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	var resp openai.ChatCompletionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	if resp.Choices[0].Message.Content == "" {
		t.Fatal("expected non-empty lorem fallback content")
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

func TestChatCompletions_OllamaBackend_NonStreaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Ollama says hello"}
	h := openai.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp openai.ChatCompletionResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content != "Ollama says hello" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestChatCompletions_OllamaBackend_Error(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{err: errors.New("connection refused")}
	h := openai.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rr.Code)
	}
}

func TestChatCompletions_OllamaBackend_Streaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Hello world"}
	h := openai.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "Hello") {
		t.Fatalf("expected 'Hello' in streaming response, got: %s", responseBody)
	}
	if !strings.Contains(responseBody, "[DONE]") {
		t.Fatalf("expected [DONE] in streaming response, got: %s", responseBody)
	}
}
