package openai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/provider/openai"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

type recordingChatGenerator struct {
	got []ollama.ChatMessage
}

func (g *recordingChatGenerator) NonStreaming(_ context.Context, _ string, msgs []ollama.ChatMessage, _ string) (string, error) {
	g.got = msgs
	return "ok", nil
}

func (g *recordingChatGenerator) Streaming(_ context.Context, _ string, msgs []ollama.ChatMessage, _ string, fn func(string) error) error {
	g.got = msgs
	return fn("ok")
}

func TestChatCompletions_ToolRoundTrip_ReachesBackendWithoutToolCallTurn(t *testing.T) {
	runner := newRunner(t)
	chat := &recordingChatGenerator{}
	h := openai.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil, nil), response.NewLoremGenerator(), chat)

	body := `{"model":"gpt-4o","messages":[` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},` +
		`{"role":"tool","tool_call_id":"call_1","content":"42"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: "ollama"},
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	want := []ollama.ChatMessage{{Role: "user", Content: "hi"}, {Role: "tool", Content: "42"}}
	if !reflect.DeepEqual(chat.got, want) {
		t.Fatalf("backend messages: got %+v, want %+v", chat.got, want)
	}
}

func TestToolCallRequired_NonStreaming_ContentIsNull(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":` + toolsPayload + `,"tool_choice":"required"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var resp struct {
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || len(resp.Choices) == 0 {
		t.Fatalf("decode: %v; body: %s", err, rr.Body.String())
	}
	content, ok := resp.Choices[0].Message["content"]
	if !ok || string(content) != "null" {
		t.Fatalf(`message.content: got %q (present=%v), want null; body: %s`, content, ok, rr.Body.String())
	}
}

func TestChatCompletions_LoremNonStreaming_ContentIsNonEmptyString(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if strings.Contains(rr.Body.String(), `"content":null`) {
		t.Fatalf("unexpected null content: %s", rr.Body.String())
	}
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		t.Fatalf("want non-empty string content; err=%v body: %s", err, rr.Body.String())
	}
}

func TestChatCompletions_FixtureWithoutContent_NotGivenContentKey(t *testing.T) {
	runner := newRunner(t)
	mod := compileFixture(t, runner, alwaysMatchWASM)
	fixtureBody := []byte(`{"id":"chatcmpl-fixture","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	h := newTestHandler(t, specs.NewValidator(), runner, []fixture.Fixture{{
		ID: "fx", Provider: "openai", Version: "v1", Status: http.StatusOK,
		ResponseBody: fixtureBody, Module: &mod,
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "test", Backend: runtimecfg.BackendFixture},
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "fixture") && !strings.Contains(rr.Body.String(), "tool_calls") {
		t.Fatalf("fixture not served: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"content"`) {
		t.Fatalf("fixture response gained a content key: %s", rr.Body.String())
	}
}
