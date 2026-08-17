package ollama_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/ollama"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

// firstMatchSelector picks the first candidate. The default LegacySelector
// only ever selects fixtures carrying a CEL or WASM matcher, which is not what
// these tests are exercising.
type firstMatchSelector struct{}

func (firstMatchSelector) Select(_ context.Context, _ fixture.MatchRequest, candidates []fixture.Fixture) (*fixture.Fixture, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	return &candidates[0], nil
}

// chatWithFixture drives /api/chat against a listener whose profile uses the
// fixture backend, with the supplied fixture pre-loaded.
func chatWithFixture(t *testing.T, f fixture.Fixture, body string) *httptest.ResponseRecorder {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, []fixture.Fixture{f}, firstMatchSelector{})
	h := ollama.NewHandler(specs.NewValidator(), matcher, response.NewLoremGenerator(), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rt := runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "fx", Backend: runtimecfg.BackendFixture},
	}
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), rt))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func baseFixture(body string) fixture.Fixture {
	return fixture.Fixture{
		ID: "fx", Provider: "ollama", Version: "v1",
		Status: http.StatusOK, ResponseBody: []byte(body),
	}
}

func TestFixture_NonStreamingIsServed(t *testing.T) {
	rr := chatWithFixture(t, baseFixture(
		`{"model":"ignored","message":{"role":"assistant","content":"canned reply"},"done":true,"done_reason":"stop"}`),
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Message.Content != "canned reply" {
		t.Errorf("content: got %q, want %q", resp.Message.Content, "canned reply")
	}
	// The fixture's model is overridden by the response-model policy.
	if resp.Model != "llama3.2" {
		t.Errorf("model: got %q, want llama3.2", resp.Model)
	}
}

func TestFixture_StreamingIsTokenized(t *testing.T) {
	rr := chatWithFixture(t, baseFixture(
		`{"message":{"role":"assistant","content":"one two three"},"done":true,"done_reason":"stop"}`),
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type: got %q, want application/x-ndjson", ct)
	}
	lines := strings.Split(strings.TrimSuffix(rr.Body.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 3 deltas plus terminal, got %d:\n%s", len(lines), rr.Body.String())
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
		t.Errorf("joined: got %q, want %q", got, "one two three")
	}
}

// Ollama returns tool-call arguments as a real JSON object. Nothing in this
// provider synthesizes tool calls, so a fixture is the only path that produces
// one — and therefore the only place a regression to OpenAI's string-encoded
// form would show up.
func TestFixture_ToolCallArgumentsStayAnObject(t *testing.T) {
	rr := chatWithFixture(t, baseFixture(
		`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Tokyo","units":"c"}}}]},"done":true,"done_reason":"stop"}`),
		`{"model":"llama3.2","messages":[{"role":"user","content":"weather?"}],"stream":false}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}

	var raw struct {
		Message struct {
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if len(raw.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d: %s", len(raw.Message.ToolCalls), rr.Body.String())
	}
	tc := raw.Message.ToolCalls[0].Function
	if tc.Name != "get_weather" {
		t.Errorf("name: got %q, want get_weather", tc.Name)
	}

	// A JSON-encoded string would decode into a Go string; an object must not.
	var asString string
	if err := json.Unmarshal(tc.Arguments, &asString); err == nil {
		t.Fatalf("arguments must be a JSON object, not a JSON-encoded string: %s", tc.Arguments)
	}
	var asObject map[string]any
	if err := json.Unmarshal(tc.Arguments, &asObject); err != nil {
		t.Fatalf("arguments must decode as an object: %v (%s)", err, tc.Arguments)
	}
	if asObject["city"] != "Tokyo" {
		t.Errorf("arguments.city: got %v, want Tokyo", asObject["city"])
	}
}

// A fixture body that is valid JSON but not a chat envelope must be served
// verbatim. Decoding it into ChatResponse succeeds with every field zero, so
// without an explicit emptiness check the fixture's content would be silently
// discarded and done would be false — hanging a client waiting for done=true.
func TestFixture_NonChatEnvelopeIsServedVerbatim(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"openai shaped"}}]}`
	rr := chatWithFixture(t, baseFixture(body),
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "openai shaped") {
		t.Fatalf("non-envelope fixture must be served verbatim, got: %s", rr.Body.String())
	}
}

func TestFixture_TemplatedBodyIsRendered(t *testing.T) {
	f := baseFixture("")
	if err := f.SetResponseTemplate([]byte(
		`{"message":{"role":"assistant","content":"profile {{ .Runtime.ProfileName }}"},"done":true,"done_reason":"stop"}`)); err != nil {
		t.Fatalf("set template: %v", err)
	}

	rr := chatWithFixture(t, f,
		`{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "{{") {
		t.Fatalf("template was not rendered: %s", rr.Body.String())
	}
	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Message.Content != "profile fx" {
		t.Errorf("content: got %q, want %q", resp.Message.Content, "profile fx")
	}
}
