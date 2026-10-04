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

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/provider/gemini"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

const streamBody = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`

func postStream(h *gemini.Handler, query string, body string, rt *runtimecfg.ListenerRuntime) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:streamGenerateContent"+query, bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "k")
	if rt != nil {
		req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), *rt))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func assertJSONArrayStream(t *testing.T, rr *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", ct)
	}
	var arr []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &arr); err != nil {
		t.Fatalf("body is not a JSON array: %v\n%s", err, rr.Body.String())
	}
	if len(arr) == 0 {
		t.Fatal("empty array")
	}
	return arr
}

func lastFinishReason(t *testing.T, arr []map[string]any) string {
	t.Helper()
	cands, _ := arr[len(arr)-1]["candidates"].([]any)
	if len(cands) == 0 {
		t.Fatalf("last element has no candidates: %v", arr[len(arr)-1])
	}
	fr, _ := cands[0].(map[string]any)["finishReason"].(string)
	return fr
}

func TestStream_AltSSE_IsEventStream(t *testing.T) {
	rr := postStream(newHandler(t), "?alt=sse", streamBody, nil)
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type: got %q", ct)
	}
	if !strings.HasPrefix(rr.Body.String(), "data: ") {
		t.Fatalf("expected SSE frames: %s", rr.Body.String())
	}
}

func TestStream_NoAlt_And_AltJSON_ReturnJSONArray(t *testing.T) {
	for _, q := range []string{"", "?alt=json", "?key=k"} {
		arr := assertJSONArrayStream(t, postStream(newHandler(t), q, streamBody, nil))
		if lastFinishReason(t, arr) != "STOP" {
			t.Errorf("query %q: final finishReason not STOP", q)
		}
	}
}

func TestStream_NoAlt_ToolCall(t *testing.T) {
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[` + geminiFuncDecl + `],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`
	arr := assertJSONArrayStream(t, postStream(newHandler(t), "", body, nil))
	if !strings.Contains(string(mustMarshal(t, arr)), "get_weather") {
		t.Errorf("expected functionCall in %v", arr)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// midStreamChat emits one delta, then fails.
type midStreamChat struct{ failFirst bool }

func (g *midStreamChat) NonStreaming(context.Context, string, []ollama.ChatMessage, string) (string, error) {
	return "", errors.New("boom")
}

func (g *midStreamChat) Streaming(_ context.Context, _ string, _ []ollama.ChatMessage, _ string, fn func(string) error) error {
	if !g.failFirst {
		if err := fn("partial "); err != nil {
			return err
		}
	}
	return errors.New("upstream exploded")
}

func ollamaHandler(t *testing.T, chat *midStreamChat) *gemini.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return gemini.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil, nil), response.NewLoremGenerator(), chat)
}

var ollamaRT = &runtimecfg.ListenerRuntime{Profile: runtimecfg.RuntimeProfile{Name: "t", Backend: "ollama"}}

func TestStream_AltJSON_MidStreamErrorClosesArray(t *testing.T) {
	rr := postStream(ollamaHandler(t, &midStreamChat{}), "?alt=json", streamBody, ollamaRT)
	arr := assertJSONArrayStream(t, rr)
	if len(arr) < 2 {
		t.Fatalf("want delta + error elements, got %v", arr)
	}
	last := arr[len(arr)-1]
	errObj, ok := last["error"].(map[string]any)
	if !ok {
		t.Fatalf("last element is not an error object: %v", last)
	}
	if errObj["message"] == "" {
		t.Errorf("error missing message: %v", errObj)
	}
}

func TestStream_AltJSON_PreOutputFailureIsHTTPError(t *testing.T) {
	rr := postStream(ollamaHandler(t, &midStreamChat{failFirst: true}), "", streamBody, ollamaRT)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502 (same as non-stream backend error)", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	if strings.HasPrefix(strings.TrimSpace(rr.Body.String()), "[") {
		t.Fatalf("body must not be array-prefixed: %s", rr.Body.String())
	}
	var env struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Error == nil {
		t.Fatalf("not a single error object: %v %s", err, rr.Body.String())
	}
}
