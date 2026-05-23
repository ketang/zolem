package gemini_test

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
	"zolem.dev/zolem/internal/provider/gemini"
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

func newHandler(t *testing.T) *gemini.Handler {
	return newHandlerWithGenerator(t, nil)
}

func newHandlerWithGenerator(t *testing.T, generator testGenerator) *gemini.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return gemini.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil, nil), response.NewLoremGenerator(), generator, nil)
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

func TestGenerateContent_OllamaFallback_NonStreaming(t *testing.T) {
	h := newHandlerWithGenerator(t, stubGenerator{text: "hello from ollama"})
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	var resp gemini.GenerateContentResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Candidates) != 1 || len(resp.Candidates[0].Content.Parts) != 1 || resp.Candidates[0].Content.Parts[0].Text != "hello from ollama" {
		t.Fatalf("response: %#v", resp)
	}
}

func TestGenerateContent_OllamaFallback_Streaming(t *testing.T) {
	h := newHandlerWithGenerator(t, stubGenerator{text: "streamed from ollama"})
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:streamGenerateContent?alt=sse", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "streamed") {
		t.Fatalf("expected ollama text in stream, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, `"finishReason":"STOP"`) {
		t.Fatalf("expected STOP in final chunk, got:\n%s", responseBody)
	}
}

func TestGenerateContent_OllamaError_FallsBackToLorem(t *testing.T) {
	h := newHandlerWithGenerator(t, errorGenerator{})
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	var resp gemini.GenerateContentResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		t.Fatal("expected at least one candidate with parts")
	}
	if resp.Candidates[0].Content.Parts[0].Text == "" {
		t.Fatal("expected non-empty lorem fallback text")
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

func TestGenerateContent_OllamaBackend_NonStreaming(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Ollama says hello"}
	h := gemini.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"contents":[{"parts":[{"text":"hi"}],"role":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "any-key")

	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp gemini.GenerateContentResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		t.Fatalf("unexpected empty response: %+v", resp)
	}
	if resp.Candidates[0].Content.Parts[0].Text != "Ollama says hello" {
		t.Fatalf("unexpected text: %q", resp.Candidates[0].Content.Parts[0].Text)
	}
}

func TestGenerateContent_OllamaBackend_Error(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{err: errors.New("connection refused")}
	h := gemini.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"contents":[{"parts":[{"text":"hi"}],"role":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "any-key")

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

func TestStreamGenerateContent_OllamaBackend(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	chat := &stubChatGenerator{text: "Hello world"}
	h := gemini.NewHandler(validator, matcher, lorem, nil, chat)

	body := `{"contents":[{"parts":[{"text":"hi"}],"role":"user"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:streamGenerateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "any-key")

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
	if !strings.Contains(responseBody, "STOP") {
		t.Fatalf("expected STOP in final chunk, got: %s", responseBody)
	}
}
