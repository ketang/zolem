package ollama_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/ollama"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/specs"
)

func newHandler(t *testing.T) *ollama.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return ollama.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil, nil), response.NewLoremGenerator(), nil)
}

func postChat(t *testing.T, h *ollama.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// chatResponse mirrors the native non-streaming /api/chat envelope.
type chatResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role      string          `json:"role"`
		Content   string          `json:"content"`
		ToolCalls json.RawMessage `json:"tool_calls"`
	} `json:"message"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason"`

	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
	PromptEvalCount    int   `json:"prompt_eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalCount          int   `json:"eval_count"`
	EvalDuration       int64 `json:"eval_duration"`
}

// Ollama is unauthenticated. This guards against copying the Bearer /
// x-api-key gate that every other zolem provider begins with.
func TestChat_RequiresNoAuthHeader(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
}

func TestChat_NonStreamingEnvelope(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"llama3.2","messages":[{"role":"user","content":"hi there"}],"stream":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q, want application/json", ct)
	}

	// Non-streaming must be exactly one JSON object, not NDJSON.
	if n := strings.Count(strings.TrimSpace(rr.Body.String()), "\n"); n != 0 {
		t.Errorf("non-streaming body must be a single object, found %d newlines: %s", n, rr.Body.String())
	}

	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rr.Body.String())
	}
	if !resp.Done {
		t.Error("done: got false, want true")
	}
	if resp.DoneReason != "stop" {
		t.Errorf("done_reason: got %q, want stop", resp.DoneReason)
	}
	if resp.Message.Role != "assistant" {
		t.Errorf("role: got %q, want assistant", resp.Message.Role)
	}
	if resp.Message.Content == "" {
		t.Error("expected generated content")
	}
	if resp.CreatedAt == "" {
		t.Error("expected created_at")
	}
	if resp.PromptEvalCount <= 0 {
		t.Errorf("prompt_eval_count: got %d, want > 0", resp.PromptEvalCount)
	}
	if resp.EvalCount <= 0 {
		t.Errorf("eval_count: got %d, want > 0", resp.EvalCount)
	}
}

// Ollama reports no usage object and no total-token field; it reports two
// counters. Guard against an OpenAI-shaped usage block leaking in.
func TestChat_HasNoUsageObject(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["usage"]; ok {
		t.Error("native /api/chat must not emit a usage object")
	}
	if _, ok := raw["total_tokens"]; ok {
		t.Error("native /api/chat has no total-tokens field")
	}
}

// Durations are nanoseconds and the parts must stay consistent with the whole.
// Asserted as an invariant, never as exact values, so the test cannot flake.
func TestChat_DurationInvariants(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	var resp chatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalDuration <= 0 {
		t.Errorf("total_duration: got %d, want > 0", resp.TotalDuration)
	}
	for name, v := range map[string]int64{
		"load_duration":        resp.LoadDuration,
		"prompt_eval_duration": resp.PromptEvalDuration,
		"eval_duration":        resp.EvalDuration,
	} {
		if v < 0 {
			t.Errorf("%s: got %d, want >= 0", name, v)
		}
	}
	if sum := resp.LoadDuration + resp.PromptEvalDuration + resp.EvalDuration; sum > resp.TotalDuration {
		t.Errorf("parts (%d) exceed total_duration (%d)", sum, resp.TotalDuration)
	}
}

// THE critical case: Ollama's `stream` defaults to TRUE when absent, the
// opposite of OpenAI and the opposite of Go's bool zero value.
func TestChat_StreamDefaultsToTrueWhenAbsent(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("omitting `stream` must stream NDJSON; content-type was %q. body:\n%s", ct, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, "data: ") || strings.Contains(body, "[DONE]") {
		t.Errorf("NDJSON must not use SSE framing; got:\n%s", body)
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple NDJSON objects, got:\n%s", body)
	}
	var last chatResponse
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("decode final line: %v (%q)", err, lines[len(lines)-1])
	}
	if !last.Done {
		t.Error("final NDJSON object must carry done=true")
	}
	if last.DoneReason != "stop" {
		t.Errorf("final done_reason: got %q, want stop", last.DoneReason)
	}
}

func TestChat_StreamExplicitTrueStreams(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type: got %q, want application/x-ndjson", ct)
	}
}

func TestChat_StreamExplicitFalseDoesNotStream(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", ct)
	}
}

// Only the final streamed object carries the metrics block.
func TestChat_StreamMetricsOnlyOnFinalObject(t *testing.T) {
	rr := postChat(t, newHandler(t), `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	lines := strings.Split(strings.TrimSuffix(rr.Body.String(), "\n"), "\n")
	for i, line := range lines[:len(lines)-1] {
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if _, ok := raw["eval_count"]; ok {
			t.Errorf("intermediate line %d must not carry metrics: %s", i, line)
		}
		if raw["done"] != false {
			t.Errorf("intermediate line %d must have done=false: %s", i, line)
		}
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("final line: %v", err)
	}
	if _, ok := last["eval_count"]; !ok {
		t.Error("final line must carry eval_count")
	}
}

func TestChat_RejectsMalformedRequests(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"invalid json", `{"model":`},
		{"missing model", `{"messages":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := postChat(t, newHandler(t), tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400. body: %s", rr.Code, rr.Body.String())
			}
			assertFlatError(t, rr.Body.Bytes())
		})
	}
}

// Ollama's error envelope is flat {"error": "..."} — never OpenAI's nested
// {"error": {"message": ...}} shape.
func assertFlatError(t *testing.T, body []byte) {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (%s)", err, body)
	}
	v, ok := env["error"]
	if !ok {
		t.Fatalf("missing error field: %s", body)
	}
	if _, isString := v.(string); !isString {
		t.Fatalf("error must be a flat string, got %T: %s", v, body)
	}
}

func TestNotFound_ReturnsFlatError(t *testing.T) {
	h := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404. body: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q, want application/json", ct)
	}
	assertFlatError(t, rr.Body.Bytes())
}
