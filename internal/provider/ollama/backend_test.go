package ollama_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	ollamaclient "github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/provider/ollama"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

type stubChatGenerator struct {
	text string
	err  error
}

func (g *stubChatGenerator) NonStreaming(_ context.Context, _ string, _ []ollamaclient.ChatMessage, _ string) (string, error) {
	return g.text, g.err
}

func (g *stubChatGenerator) Streaming(_ context.Context, _ string, _ []ollamaclient.ChatMessage, _ string, fn func(string) error) error {
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

// chatWithProfile drives /api/chat with an explicit listener runtime attached,
// which is how backend selection and forced errors are configured.
func chatWithProfile(t *testing.T, chat *stubChatGenerator, profile runtimecfg.RuntimeProfile, body string) *httptest.ResponseRecorder {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := ollama.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil, nil), response.NewLoremGenerator(), chat)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rt := runtimecfg.ListenerRuntime{Profile: profile}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestChat_OllamaBackend_NonStreaming(t *testing.T) {
	rr := chatWithProfile(t,
		&stubChatGenerator{text: "Ollama says hello"},
		runtimecfg.RuntimeProfile{Name: "test", Backend: runtimecfg.BackendOllama},
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Message.Content != "Ollama says hello" {
		t.Fatalf("content: got %q, want %q", resp.Message.Content, "Ollama says hello")
	}
}

func TestChat_OllamaBackend_Streaming(t *testing.T) {
	rr := chatWithProfile(t,
		&stubChatGenerator{text: "one two three"},
		runtimecfg.RuntimeProfile{Name: "test", Backend: runtimecfg.BackendOllama},
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	lines := strings.Split(strings.TrimSuffix(rr.Body.String(), "\n"), "\n")
	// Three content deltas plus the final done object.
	if len(lines) != 4 {
		t.Fatalf("line count: got %d, want 4. body:\n%s", len(lines), rr.Body.String())
	}

	var joined strings.Builder
	for _, line := range lines[:3] {
		var obj chatResponse
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		joined.WriteString(obj.Message.Content)
	}
	if got := strings.TrimSpace(joined.String()); got != "one two three" {
		t.Errorf("joined deltas: got %q, want %q", got, "one two three")
	}

	var final chatResponse
	if err := json.Unmarshal([]byte(lines[3]), &final); err != nil {
		t.Fatalf("decode final: %v", err)
	}
	if !final.Done || final.EvalCount != 3 {
		t.Errorf("final object: done=%v eval_count=%d, want done=true eval_count=3", final.Done, final.EvalCount)
	}
}

func TestChat_OllamaBackend_ErrorIsFlatEnvelope(t *testing.T) {
	rr := chatWithProfile(t,
		&stubChatGenerator{err: errors.New("connection refused")},
		runtimecfg.RuntimeProfile{Name: "test", Backend: runtimecfg.BackendOllama},
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500. body: %s", rr.Code, rr.Body.String())
	}
	assertFlatError(t, rr.Body.Bytes())
}

// A mid-stream failure cannot change the status line, so Ollama reports it as
// an {"error": ...} object inside an already-200 NDJSON body.
func TestChat_OllamaBackend_StreamErrorIsInBand(t *testing.T) {
	rr := chatWithProfile(t,
		&stubChatGenerator{err: errors.New("connection refused")},
		runtimecfg.RuntimeProfile{Name: "test", Backend: runtimecfg.BackendOllama},
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (headers already sent)", rr.Code)
	}
	lines := strings.Split(strings.TrimSuffix(rr.Body.String(), "\n"), "\n")
	assertFlatError(t, []byte(lines[len(lines)-1]))
}

// Every forced error type maps to a status with the flat envelope. Ollama is
// unauthenticated, so 401/403 are not states a real Ollama reaches on
// /api/chat; they are retained so the `error` backend behaves uniformly across
// all four providers.
func TestChat_ErrorBackend_StatusMapping(t *testing.T) {
	for _, tc := range []struct {
		errorType string
		want      int
	}{
		{runtimecfg.ErrorTypeAuthentication, http.StatusUnauthorized},
		{runtimecfg.ErrorTypePermission, http.StatusForbidden},
		{runtimecfg.ErrorTypeInvalidRequest, http.StatusBadRequest},
		{runtimecfg.ErrorTypeRateLimit, http.StatusTooManyRequests},
		{runtimecfg.ErrorTypeServerError, http.StatusInternalServerError},
	} {
		t.Run(tc.errorType, func(t *testing.T) {
			rr := chatWithProfile(t, nil,
				runtimecfg.RuntimeProfile{Name: "e", Backend: runtimecfg.BackendError, ErrorType: tc.errorType},
				`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)

			if rr.Code != tc.want {
				t.Fatalf("status: got %d, want %d. body: %s", rr.Code, tc.want, rr.Body.String())
			}
			assertFlatError(t, rr.Body.Bytes())
		})
	}
}

// The lorem backend must still work for this provider — backend selection is
// generic, so nothing is provider-specific.
func TestChat_LoremBackendWorks(t *testing.T) {
	rr := chatWithProfile(t, nil,
		runtimecfg.RuntimeProfile{Name: "l", Backend: runtimecfg.BackendLorem},
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Message.Content == "" {
		t.Error("expected lorem content")
	}
}

// A profile pinning a response model must have it reflected in both the chat
// envelope and the /api/tags catalogue.
func TestResponseModelPolicy_IsReflected(t *testing.T) {
	profile := runtimecfg.RuntimeProfile{
		Name:                "pinned",
		Backend:             runtimecfg.BackendLorem,
		ResponseModelPolicy: "force_literal",
		ResponseModel:       "pinned-model:latest",
	}

	rr := chatWithProfile(t, nil, profile,
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Model != "pinned-model:latest" {
		t.Errorf("chat model: got %q, want pinned-model:latest", resp.Model)
	}

	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := ollama.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil, nil), response.NewLoremGenerator(), nil)
	tagsReq := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	tagsReq = tagsReq.WithContext(runtimecfg.WithListenerRuntime(tagsReq.Context(), runtimecfg.ListenerRuntime{Profile: profile}))
	tagsRR := httptest.NewRecorder()
	h.ServeHTTP(tagsRR, tagsReq)

	if !strings.Contains(tagsRR.Body.String(), "pinned-model:latest") {
		t.Errorf("pinned model must appear in the catalogue: %s", tagsRR.Body.String())
	}
}
