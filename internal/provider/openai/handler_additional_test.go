package openai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/openai"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

var alwaysMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x80, 0x3f, 0x0b,
}

var labelLengthMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x15,
	0x01, 0x13, 0x00, 0x43, 0x00, 0x00, 0x80, 0x3f, 0x43,
	0x00, 0x00, 0x80, 0xbf, 0x20, 0x01, 0x41, 0x85, 0x01,
	0x46, 0x1b, 0x0b,
}

type errorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   any    `json:"param"`
		Code    any    `json:"code"`
	} `json:"error"`
}

type streamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func newRunner(t *testing.T) *fixture.Runner {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return runner
}

func newTestHandler(t *testing.T, validator *specs.Validator, runner *fixture.Runner, fixtures []fixture.Fixture) *openai.Handler {
	t.Helper()
	return openai.NewHandler(validator, fixture.NewMatcher(runner, fixtures, nil), response.NewLoremGenerator(), nil, nil)
}

func compileFixture(t *testing.T, runner *fixture.Runner, wasm []byte) fixture.CompiledModule {
	t.Helper()
	mod, err := runner.CompileWASM(context.Background(), wasm)
	if err != nil {
		t.Fatalf("compile wasm: %v", err)
	}
	return mod
}

func decodeError(t *testing.T, body []byte) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return env
}

func splitSSEDataFrames(t *testing.T, body string) []string {
	t.Helper()
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		t.Fatal("expected SSE body")
	}
	rawFrames := strings.Split(trimmed, "\n\n")
	frames := make([]string, 0, len(rawFrames))
	for _, raw := range rawFrames {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, "data: ") {
			t.Fatalf("unexpected SSE frame: %q", raw)
		}
		frames = append(frames, strings.TrimPrefix(raw, "data: "))
	}
	return frames
}

func decodeStreamChunk(t *testing.T, payload string) streamChunk {
	t.Helper()
	var chunk streamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		t.Fatalf("decode chunk: %v\npayload: %s", err, payload)
	}
	return chunk
}

func TestChatCompletions_InvalidJSON(t *testing.T) {
	runner := newRunner(t)
	h := newTestHandler(t, specs.NewValidator(), runner, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString("{"))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}
	env := decodeError(t, rr.Body.Bytes())
	if env.Error.Type != "invalid_request_error" {
		t.Fatalf("error type: got %q, want invalid_request_error", env.Error.Type)
	}
	if !strings.HasPrefix(env.Error.Message, "invalid JSON:") {
		t.Fatalf("error message: got %q, want invalid JSON prefix", env.Error.Message)
	}
}

func TestChatCompletions_SchemaValidationFailure(t *testing.T) {
	runner := newRunner(t)
	validator := specs.NewValidator()
	if err := validator.LoadRaw("openai", "v1", []byte(`{
		"type": "object",
		"required": ["model"],
		"properties": {
			"model": {"type": "string"}
		}
	}`)); err != nil {
		t.Fatalf("load schema: %v", err)
	}
	h := newTestHandler(t, validator, runner, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rr.Body.Bytes())
	if !strings.HasPrefix(env.Error.Message, "validation failed:") {
		t.Fatalf("error message: got %q, want validation failure", env.Error.Message)
	}
	if !strings.Contains(env.Error.Message, "model") {
		t.Fatalf("error message: got %q, want it to mention model", env.Error.Message)
	}
}

func TestChatCompletions_MissingModel(t *testing.T) {
	runner := newRunner(t)
	h := newTestHandler(t, specs.NewValidator(), runner, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rr.Body.Bytes())
	if env.Error.Message != "model is required" {
		t.Fatalf("error message: got %q, want model is required", env.Error.Message)
	}
}

func TestChatCompletions_MissingAuthUsesHighFidelityEnvelope(t *testing.T) {
	runner := newRunner(t)
	h := newTestHandler(t, specs.NewValidator(), runner, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	env := decodeError(t, rr.Body.Bytes())
	if env.Error.Code != "invalid_api_key" {
		t.Fatalf("error code: got %#v, want invalid_api_key", env.Error.Code)
	}
	if env.Error.Param != nil {
		t.Fatalf("error param: got %#v, want nil", env.Error.Param)
	}
	if !strings.Contains(env.Error.Message, "Authorization header") {
		t.Fatalf("error message: got %q, want Authorization header guidance", env.Error.Message)
	}
}

func TestChatCompletions_InvalidJSONUsesHighFidelityEnvelope(t *testing.T) {
	runner := newRunner(t)
	h := newTestHandler(t, specs.NewValidator(), runner, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString("{"))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
	env := decodeError(t, rr.Body.Bytes())
	if env.Error.Type != "invalid_request_error" {
		t.Fatalf("error type: got %q, want invalid_request_error", env.Error.Type)
	}
	if env.Error.Param != nil {
		t.Fatalf("error param: got %#v, want nil", env.Error.Param)
	}
	if !strings.Contains(env.Error.Message, "invalid JSON") {
		t.Fatalf("error message: got %q, want invalid JSON detail", env.Error.Message)
	}
}

func TestChatCompletions_LocalRuntimeErrorBackendRateLimit(t *testing.T) {
	runner := newRunner(t)
	h := newTestHandler(t, specs.NewValidator(), runner, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			Backend:   runtimecfg.BackendError,
			ErrorType: runtimecfg.ErrorTypeRateLimit,
		},
	}))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusTooManyRequests)
	}
	env := decodeError(t, rr.Body.Bytes())
	if env.Error.Type != "rate_limit_error" {
		t.Fatalf("error type: got %q, want rate_limit_error", env.Error.Type)
	}
	if env.Error.Code != "rate_limit_exceeded" {
		t.Fatalf("error code: got %#v, want rate_limit_exceeded", env.Error.Code)
	}
}

func TestChatCompletions_LocalRuntimeResponseModelForceLiteral(t *testing.T) {
	runner := newRunner(t)
	h := newTestHandler(t, specs.NewValidator(), runner, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			ResponseModelPolicy: runtimecfg.ResponseModelForceLiteral,
			ResponseModel:       "mock-openai-model",
		},
	}))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Model != "mock-openai-model" {
		t.Fatalf("model: got %q, want mock-openai-model", resp.Model)
	}
}

func TestChatCompletions_FixtureResponse_NonStreaming(t *testing.T) {
	runner := newRunner(t)
	mod := compileFixture(t, runner, []byte(alwaysMatchWASM))
	fixtureBody := []byte(`{"id":"chatcmpl-fixture","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"fixture reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}}`)
	h := newTestHandler(t, specs.NewValidator(), runner, []fixture.Fixture{{
		ID:           "fixture-openai",
		Provider:     "openai",
		Version:      "v1",
		Status:       http.StatusAccepted,
		ResponseBody: fixtureBody,
		Module:       &mod,
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusAccepted)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}
	if !bytes.Equal(rr.Body.Bytes(), fixtureBody) {
		t.Fatalf("body mismatch:\n got: %s\nwant: %s", rr.Body.Bytes(), fixtureBody)
	}

	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish reason: got %q, want stop", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 2 || resp.Usage.TotalTokens != 13 {
		t.Fatalf("usage: got %+v", resp.Usage)
	}
}

func TestChatCompletions_FixtureStreamingResponse(t *testing.T) {
	runner := newRunner(t)
	mod := compileFixture(t, runner, []byte(alwaysMatchWASM))
	fixtureBody := []byte(`{"id":"chatcmpl-fixture","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"fixture stream"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}}`)
	h := newTestHandler(t, specs.NewValidator(), runner, []fixture.Fixture{{
		ID:           "fixture-openai",
		Provider:     "openai",
		Version:      "v1",
		Stream:       true,
		Status:       http.StatusAccepted,
		ResponseBody: fixtureBody,
		Module:       &mod,
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
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

	frames := splitSSEDataFrames(t, rr.Body.String())
	if len(frames) != 6 {
		t.Fatalf("frame count: got %d, want 6", len(frames))
	}
	if frames[5] != "[DONE]" {
		t.Fatalf("terminal frame: got %q, want [DONE]", frames[5])
	}

	first := decodeStreamChunk(t, frames[0])
	if first.Object != "chat.completion.chunk" {
		t.Fatalf("first chunk object: got %q", first.Object)
	}
	if first.Model != "gpt-4o" {
		t.Fatalf("first chunk model: got %q", first.Model)
	}
	if first.Choices[0].Delta.Role != "assistant" || first.Choices[0].Delta.Content != "" {
		t.Fatalf("first chunk delta: got %+v", first.Choices[0].Delta)
	}
	if first.Choices[0].FinishReason != nil {
		t.Fatalf("first chunk finish reason: got %v, want nil", first.Choices[0].FinishReason)
	}

	second := decodeStreamChunk(t, frames[1])
	if second.ID != first.ID || second.Created != first.Created {
		t.Fatalf("second chunk metadata mismatch: first=%s/%d second=%s/%d", first.ID, first.Created, second.ID, second.Created)
	}
	if second.Choices[0].Delta.Content != "fixture " {
		t.Fatalf("second chunk content: got %q, want %q", second.Choices[0].Delta.Content, "fixture ")
	}

	third := decodeStreamChunk(t, frames[2])
	if third.Choices[0].Delta.Content != "stream" {
		t.Fatalf("third chunk content: got %q, want %q", third.Choices[0].Delta.Content, "stream")
	}

	final := decodeStreamChunk(t, frames[3])
	if final.Choices[0].FinishReason == nil || *final.Choices[0].FinishReason != "stop" {
		t.Fatalf("final chunk finish reason: got %v, want stop", final.Choices[0].FinishReason)
	}
	if final.Choices[0].Delta.Role != "" || final.Choices[0].Delta.Content != "" {
		t.Fatalf("final chunk delta: got %+v, want empty", final.Choices[0].Delta)
	}

	usage := decodeStreamChunk(t, frames[4])
	// The synthetic usage summary is emitted as its own chunk before [DONE].
	if len(usage.Choices) != 0 {
		t.Fatalf("usage chunk choices: got %d, want 0", len(usage.Choices))
	}
	if usage.Usage == nil {
		t.Fatal("usage chunk missing usage")
	}
	if usage.Usage.PromptTokens != 11 || usage.Usage.CompletionTokens != 2 || usage.Usage.TotalTokens != 13 {
		t.Fatalf("usage chunk: got %+v", usage.Usage)
	}
}

func TestChatCompletions_StreamUsageChunkRequiresIncludeUsage(t *testing.T) {
	runner := newRunner(t)
	h := newTestHandler(t, specs.NewValidator(), runner, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status without include_usage: got %d, want %d", rr.Code, http.StatusOK)
	}
	frames := splitSSEDataFrames(t, rr.Body.String())
	if frames[len(frames)-1] != "[DONE]" {
		t.Fatalf("terminal frame without include_usage: got %q, want [DONE]", frames[len(frames)-1])
	}
	for _, frame := range frames[:len(frames)-1] {
		chunk := decodeStreamChunk(t, frame)
		if chunk.Usage != nil {
			t.Fatalf("unexpected usage chunk without include_usage: %s", frame)
		}
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status with include_usage: got %d, want %d", rr.Code, http.StatusOK)
	}
	frames = splitSSEDataFrames(t, rr.Body.String())
	if len(frames) < 2 {
		t.Fatalf("frames with include_usage: got %d, want at least 2", len(frames))
	}
	usage := decodeStreamChunk(t, frames[len(frames)-2])
	if usage.Usage == nil {
		t.Fatalf("missing usage chunk with include_usage; penultimate frame: %s", frames[len(frames)-2])
	}
	if len(usage.Choices) != 0 {
		t.Fatalf("usage chunk choices: got %d, want 0", len(usage.Choices))
	}
}

func TestChatCompletions_FixtureStreamingFallbackOnBadPayload(t *testing.T) {
	runner := newRunner(t)
	mod := compileFixture(t, runner, []byte(alwaysMatchWASM))
	fixtureBody := []byte(`{"id":"broken","object":"chat.completion","created":1,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":0,"total_tokens":11}}`)
	h := newTestHandler(t, specs.NewValidator(), runner, []fixture.Fixture{{
		ID:           "fixture-openai",
		Provider:     "openai",
		Version:      "v1",
		Stream:       true,
		Status:       http.StatusBadGateway,
		ResponseBody: fixtureBody,
		Module:       &mod,
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadGateway)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}
	if !bytes.Equal(rr.Body.Bytes(), fixtureBody) {
		t.Fatalf("body mismatch:\n got: %s\nwant: %s", rr.Body.Bytes(), fixtureBody)
	}
}

func TestChatCompletions_LabelPropagationFromRoutingContext(t *testing.T) {
	runner := newRunner(t)
	mod := compileFixture(t, runner, []byte(labelLengthMatchWASM))
	fixtureBody := []byte(`{"id":"label-matched","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"matched by labels"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
	h := newTestHandler(t, specs.NewValidator(), runner, []fixture.Fixture{{
		ID:           "label-sensitive",
		Provider:     "openai",
		Version:      "v1",
		Status:       http.StatusAccepted,
		ResponseBody: fixtureBody,
		Module:       &mod,
	}})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	withLabels := func(req *http.Request) *http.Request {
		ctx := context.WithValue(req.Context(), router.LabelsKey{}, map[string]string{"tenant": "acme"})
		return req.WithContext(ctx)
	}

	reqWithLabels := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	reqWithLabels = withLabels(reqWithLabels)
	reqWithLabels.Header.Set("Authorization", "Bearer sk-test")
	reqWithLabels.Header.Set("Content-Type", "application/json")
	rrWithLabels := httptest.NewRecorder()
	h.ServeHTTP(rrWithLabels, reqWithLabels)

	if rrWithLabels.Code != http.StatusAccepted {
		t.Fatalf("labeled status: got %d, want %d", rrWithLabels.Code, http.StatusAccepted)
	}
	if !bytes.Equal(rrWithLabels.Body.Bytes(), fixtureBody) {
		t.Fatalf("labeled body mismatch:\n got: %s\nwant: %s", rrWithLabels.Body.Bytes(), fixtureBody)
	}

	reqWithoutLabels := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	reqWithoutLabels.Header.Set("Authorization", "Bearer sk-test")
	reqWithoutLabels.Header.Set("Content-Type", "application/json")
	rrWithoutLabels := httptest.NewRecorder()
	h.ServeHTTP(rrWithoutLabels, reqWithoutLabels)

	if rrWithoutLabels.Code != http.StatusOK {
		t.Fatalf("unlabeled status: got %d, want %d", rrWithoutLabels.Code, http.StatusOK)
	}
	if bytes.Equal(rrWithoutLabels.Body.Bytes(), fixtureBody) {
		t.Fatalf("expected unlabeled request to miss the fixture")
	}

	marshaledWithLabels, err := json.Marshal(fixture.MatchRequest{
		Provider: "openai",
		Version:  "v1",
		Labels:   map[string]string{"tenant": "acme"},
		Body:     json.RawMessage(body),
	})
	if err != nil {
		t.Fatalf("marshal match request: %v", err)
	}
	if got := len(marshaledWithLabels); got != 133 {
		t.Fatalf("match request length: got %d, want 133", got)
	}
}
