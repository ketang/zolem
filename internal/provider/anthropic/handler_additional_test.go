package anthropic_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/anthropic"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

const anthropicMessagesPath = "/v1/messages"

const anthropicFixtureJSON = `{
  "id": "msg_test",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "Hello from fixture."}],
  "model": "claude-3-5-sonnet-20241022",
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 10, "output_tokens": 5}
}`

const anthropicInvalidFixtureJSON = `{"id":"msg_test",`

var anthropicConstantMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x80, 0x3f, 0x0b,
}

var anthropicLengthMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x07,
	0x01, 0x05, 0x00, 0x20, 0x01, 0xb2, 0x0b,
}

type anthropicErrorResponse struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Error     struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func newAnthropicHandler(t *testing.T, validator *specs.Validator, buildFixtures func(*fixture.Runner) []fixture.Fixture) *anthropic.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, buildFixtures(runner), nil)
	return anthropic.NewHandler(validator, matcher, response.NewLoremGenerator(), nil, nil)
}

func loadAnthropicVendoredSnapshot(t *testing.T, validator *specs.Validator) {
	t.Helper()

	data, ok := specs.VendoredFallbacks()["anthropic:v1"]
	if !ok {
		t.Fatal("missing anthropic vendored snapshot")
	}
	if err := specs.LoadProviderSchema(validator, "anthropic", "v1", data); err != nil {
		t.Fatalf("load anthropic vendored snapshot: %v", err)
	}
}

func compileWASM(t *testing.T, r *fixture.Runner, wasm []byte) fixture.CompiledModule {
	t.Helper()
	mod, err := r.CompileWASM(context.Background(), wasm)
	if err != nil {
		t.Fatalf("compile wasm: %v", err)
	}
	return mod
}

func constScoreWASM(score float32) []byte {
	wasm := append([]byte(nil), anthropicConstantMatchWASM...)
	binary.LittleEndian.PutUint32(wasm[len(wasm)-5:len(wasm)-1], math.Float32bits(score))
	return wasm
}

func newAuthRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, anthropicMessagesPath, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key")
	return req
}

func decodeAnthropicError(t *testing.T, rr *httptest.ResponseRecorder) anthropicErrorResponse {
	t.Helper()
	var resp anthropicErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return resp
}

func assertEventOrder(t *testing.T, body string, events ...string) {
	t.Helper()
	pos := 0
	for _, event := range events {
		marker := "event: " + event
		idx := strings.Index(body[pos:], marker)
		if idx < 0 {
			t.Fatalf("missing %q in stream:\n%s", marker, body)
		}
		pos += idx + len(marker)
	}
}

func TestMessages_InvalidJSON(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(*fixture.Runner) []fixture.Fixture { return nil })

	req := newAuthRequest(`{"model":`)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}
	resp := decodeAnthropicError(t, rr)
	if resp.Type != "error" {
		t.Fatalf("response type: got %q, want error", resp.Type)
	}
	if resp.Error.Type != "invalid_request_error" {
		t.Fatalf("error type: got %q, want invalid_request_error", resp.Error.Type)
	}
	if !strings.Contains(resp.Error.Message, "invalid JSON") {
		t.Fatalf("error message: got %q, want invalid JSON detail", resp.Error.Message)
	}
}

func TestMessages_ValidationFailure(t *testing.T) {
	validator := specs.NewValidator()
	loadAnthropicVendoredSnapshot(t, validator)
	h := newAnthropicHandler(t, validator, func(*fixture.Runner) []fixture.Fixture { return nil })

	req := newAuthRequest(`{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"system","content":"hi"}]}`)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	resp := decodeAnthropicError(t, rr)
	if resp.Error.Type != "invalid_request_error" {
		t.Fatalf("error type: got %q, want invalid_request_error", resp.Error.Type)
	}
	if !strings.Contains(resp.Error.Message, "validation failed") {
		t.Fatalf("error message: got %q, want validation failure", resp.Error.Message)
	}
}

func TestMessages_MissingModel(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(*fixture.Runner) []fixture.Fixture { return nil })

	req := newAuthRequest(`{"max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	resp := decodeAnthropicError(t, rr)
	if resp.Error.Message != "model is required" {
		t.Fatalf("error message: got %q, want model is required", resp.Error.Message)
	}
}

func TestMessages_MissingMaxTokens(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(*fixture.Runner) []fixture.Fixture { return nil })

	req := newAuthRequest(`{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	resp := decodeAnthropicError(t, rr)
	if resp.Error.Message != "max_tokens is required" {
		t.Fatalf("error message: got %q, want max_tokens is required", resp.Error.Message)
	}
}

func TestMessages_MissingAuthUsesHighFidelityEnvelope(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(*fixture.Runner) []fixture.Fixture { return nil })

	req := httptest.NewRequest(http.MethodPost, anthropicMessagesPath, bytes.NewBufferString(`{"model":"claude-3-5-sonnet-20241022","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	resp := decodeAnthropicError(t, rr)
	if resp.RequestID == "" {
		t.Fatal("expected request_id in high fidelity error")
	}
	if resp.Error.Message != "invalid x-api-key" {
		t.Fatalf("error message: got %q, want invalid x-api-key", resp.Error.Message)
	}
}

func TestMessages_InvalidJSONUsesHighFidelityEnvelope(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(*fixture.Runner) []fixture.Fixture { return nil })

	req := httptest.NewRequest(http.MethodPost, anthropicMessagesPath, bytes.NewBufferString(`{"model":`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	resp := decodeAnthropicError(t, rr)
	if resp.RequestID == "" {
		t.Fatal("expected request_id in high fidelity error")
	}
	if !strings.Contains(resp.Error.Message, "invalid JSON") {
		t.Fatalf("error message: got %q, want invalid JSON detail", resp.Error.Message)
	}
}

func TestMessages_LocalRuntimeErrorBackendRateLimit(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(*fixture.Runner) []fixture.Fixture { return nil })

	req := newAuthRequest(`{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			Backend:   runtimecfg.BackendError,
			ErrorType: runtimecfg.ErrorTypeRateLimit,
		},
	}))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusTooManyRequests)
	}
	resp := decodeAnthropicError(t, rr)
	if resp.Error.Type != "rate_limit_error" {
		t.Fatalf("error type: got %q, want rate_limit_error", resp.Error.Type)
	}
}

func TestMessages_FixtureNonStreaming(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(r *fixture.Runner) []fixture.Fixture {
		mod := compileWASM(t, r, constScoreWASM(1))
		return []fixture.Fixture{{
			ID:           "anthropic-fixture",
			Provider:     "anthropic",
			Version:      "v1",
			Status:       207,
			ResponseBody: []byte(anthropicFixtureJSON),
			Module:       &mod,
		}}
	})

	req := newAuthRequest(`{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != 207 {
		t.Fatalf("status: got %d, want 207", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}
	var resp anthropic.MessagesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Type != "message" {
		t.Fatalf("type: got %q, want message", resp.Type)
	}
	if resp.Role != "assistant" {
		t.Fatalf("role: got %q, want assistant", resp.Role)
	}
	if resp.Model != "claude-3-5-sonnet-20241022" {
		t.Fatalf("model: got %q, want claude-3-5-sonnet-20241022", resp.Model)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("stop reason: got %q, want end_turn", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("content length: got %d, want 1", len(resp.Content))
	}
	if resp.Content[0].Text != "Hello from fixture." {
		t.Fatalf("content text: got %q, want Hello from fixture.", resp.Content[0].Text)
	}
	if resp.Usage.InputTokens != 10 {
		t.Fatalf("input tokens: got %d, want 10", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Fatalf("output tokens: got %d, want 5", resp.Usage.OutputTokens)
	}
}

func TestMessages_LocalRuntimeFixtureResponseModelForceBackend(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(r *fixture.Runner) []fixture.Fixture {
		mod := compileWASM(t, r, constScoreWASM(1))
		return []fixture.Fixture{{
			ID:           "anthropic-fixture",
			Provider:     "anthropic",
			Version:      "v1",
			Status:       http.StatusOK,
			ResponseBody: []byte(anthropicFixtureJSON),
			Module:       &mod,
		}}
	})

	req := newAuthRequest(`{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			ResponseModelPolicy: runtimecfg.ResponseModelForceBackend,
			BackendModel:        "anthropic-backend-model",
		},
	}))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var resp anthropic.MessagesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model != "anthropic-backend-model" {
		t.Fatalf("model: got %q, want anthropic-backend-model", resp.Model)
	}
}

func TestMessages_FixtureStreaming(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(r *fixture.Runner) []fixture.Fixture {
		mod := compileWASM(t, r, constScoreWASM(1))
		return []fixture.Fixture{{
			ID:           "anthropic-fixture",
			Provider:     "anthropic",
			Version:      "v1",
			Stream:       true,
			Status:       200,
			ResponseBody: []byte(anthropicFixtureJSON),
			Module:       &mod,
		}}
	})

	req := newAuthRequest(`{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	for key, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	} {
		if got := rr.Header().Get(key); got != want {
			t.Fatalf("%s: got %q, want %q", key, got, want)
		}
	}
	body := rr.Body.String()
	assertEventOrder(t, body,
		"message_start",
		"content_block_start",
		"ping",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	)
	if !strings.Contains(body, `"input_tokens":10`) {
		t.Fatalf("missing input token usage in stream:\n%s", body)
	}
	if !strings.Contains(body, `"output_tokens":1`) {
		t.Fatalf("missing message_start output token usage in stream:\n%s", body)
	}
	if !strings.Contains(body, `"output_tokens":3`) {
		t.Fatalf("missing final output token usage in stream:\n%s", body)
	}
}

func TestMessages_MalformedFixtureFallsBackToRawJSON(t *testing.T) {
	h := newAnthropicHandler(t, specs.NewValidator(), func(r *fixture.Runner) []fixture.Fixture {
		mod := compileWASM(t, r, constScoreWASM(1))
		return []fixture.Fixture{{
			ID:           "anthropic-broken",
			Provider:     "anthropic",
			Version:      "v1",
			Stream:       true,
			Status:       502,
			ResponseBody: []byte(anthropicInvalidFixtureJSON),
			Module:       &mod,
		}}
	})

	req := newAuthRequest(`{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != 502 {
		t.Fatalf("status: got %d, want 502", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}
	if got := rr.Body.String(); got != anthropicInvalidFixtureJSON {
		t.Fatalf("body: got %q, want raw fixture JSON", got)
	}
	if strings.Contains(rr.Body.String(), "event:") {
		t.Fatalf("expected raw JSON fallback, got stream:\n%s", rr.Body.String())
	}
}

func TestMessages_LabelPropagationToMatcher(t *testing.T) {
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	baseReq := fixture.MatchRequest{
		Provider: "anthropic",
		Version:  "v1",
		Labels:   map[string]string{},
		Body:     json.RawMessage(body),
	}
	labelledReq := baseReq
	labelledReq.Labels = map[string]string{"tenant": "acme"}

	baseBytes, err := json.Marshal(baseReq)
	if err != nil {
		t.Fatalf("marshal unlabeled request: %v", err)
	}
	labelledBytes, err := json.Marshal(labelledReq)
	if err != nil {
		t.Fatalf("marshal labelled request: %v", err)
	}
	if len(labelledBytes) <= len(baseBytes)+1 {
		t.Fatalf("expected labels to materially increase matcher input size: unlabeled=%d labelled=%d", len(baseBytes), len(labelledBytes))
	}
	threshold := float32(len(baseBytes) + 1)

	h := newAnthropicHandler(t, specs.NewValidator(), func(r *fixture.Runner) []fixture.Fixture {
		lengthMod := compileWASM(t, r, anthropicLengthMatchWASM)
		thresholdMod := compileWASM(t, r, constScoreWASM(threshold))
		return []fixture.Fixture{
			{
				ID:           "threshold",
				Provider:     "anthropic",
				Version:      "v1",
				Status:       200,
				ResponseBody: []byte(`{"id":"threshold","type":"message","role":"assistant","content":[{"type":"text","text":"threshold"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`),
				Module:       &thresholdMod,
			},
			{
				ID:           "labels-win",
				Provider:     "anthropic",
				Version:      "v1",
				Status:       200,
				ResponseBody: []byte(`{"id":"labels-win","type":"message","role":"assistant","content":[{"type":"text","text":"labels"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`),
				Module:       &lengthMod,
			},
		}
	})

	t.Run("without labels", func(t *testing.T) {
		req := newAuthRequest(body)
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		var resp anthropic.MessagesResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Content[0].Text != "threshold" {
			t.Fatalf("fixture selected without labels: got %q, want threshold", resp.Content[0].Text)
		}
	})

	t.Run("with labels", func(t *testing.T) {
		req := newAuthRequest(body)
		req = req.WithContext(context.WithValue(req.Context(), router.LabelsKey{}, map[string]string{"tenant": "acme"}))
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		var resp anthropic.MessagesResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Content[0].Text != "labels" {
			t.Fatalf("fixture selected with labels: got %q, want labels", resp.Content[0].Text)
		}
	})
}
