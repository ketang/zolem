package gemini_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/gemini"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

const geminiRequestSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["generationConfig"],
  "properties": {
    "contents": {
      "type": "array"
    },
    "generationConfig": {
      "type": "object",
      "required": ["maxOutputTokens"],
      "properties": {
        "maxOutputTokens": { "type": "integer" }
      },
      "additionalProperties": true
    }
  },
  "additionalProperties": true
}`

const (
	geminiModel             = "gemini-2.0-flash"
	geminiGeneratePath      = "/v1/models/gemini-2.0-flash:generateContent"
	geminiStreamPath        = "/v1/models/gemini-2.0-flash:streamGenerateContent?alt=sse"
	geminiInvalidActionPath = "/v1/models/gemini-2.0-flash:unsupportedAction"
	geminiBetaGeneratePath  = "/v1beta/models/gemini-2.0-flash:generateContent"
)

func newGeminiAdditionalHandler(t *testing.T, runner *fixture.Runner, fixtures []fixture.Fixture, schemas map[string][]byte) *gemini.Handler {
	t.Helper()

	validator := specs.NewValidator()
	for version, schema := range schemas {
		if err := validator.LoadRaw("gemini", version, schema); err != nil {
			t.Fatalf("load schema %q: %v", version, err)
		}
	}

	return gemini.NewHandler(validator, fixture.NewMatcher(runner, fixtures, nil), response.NewLoremGenerator(), nil, nil)
}

func compileGeminiFixture(t *testing.T, runner *fixture.Runner, id, version string, status int, responseBody []byte, threshold uint32) fixture.Fixture {
	t.Helper()

	mod, err := runner.CompileWASM(context.Background(), lengthMatchWASM(threshold))
	if err != nil {
		t.Fatalf("compile wasm for %s: %v", id, err)
	}

	return fixture.Fixture{
		ID:           id,
		Provider:     "gemini",
		Version:      version,
		Status:       status,
		ResponseBody: responseBody,
		Module:       &mod,
	}
}

func fixtureGenerateContentBody(t *testing.T, text string, promptTokens int, modelVersion string) []byte {
	t.Helper()

	tokens := strings.Fields(text)
	resp := gemini.GenerateContentResponse{
		Candidates: []gemini.Candidate{{
			Content: gemini.Content{
				Role:  "model",
				Parts: []gemini.Part{{Text: text}},
			},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: gemini.UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: len(tokens),
			TotalTokenCount:      promptTokens + len(tokens),
		},
		ModelVersion: modelVersion,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return body
}

func lengthMatchWASM(threshold uint32) []byte {
	body := []byte{
		0x00,
		0x43, 0x00, 0x00, 0x80, 0x3f,
		0x43, 0x00, 0x00, 0x80, 0xbf,
		0x20, 0x01,
		0x41,
	}
	body = appendULEB128(body, threshold)
	body = append(body,
		0x4f,
		0x1b,
		0x0b,
	)

	payload := []byte{0x01}
	payload = appendULEB128(payload, uint32(len(body)))
	payload = append(payload, body...)

	codeSection := []byte{0x0a}
	codeSection = appendULEB128(codeSection, uint32(len(payload)))
	codeSection = append(codeSection, payload...)

	return append([]byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7d,
		0x03, 0x02, 0x01, 0x00,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x12, 0x02, 0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00,
		0x05, 0x6d, 0x61, 0x74, 0x63, 0x68, 0x00, 0x00,
	}, codeSection...)
}

func appendULEB128(dst []byte, v uint32) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			dst = append(dst, b|0x80)
			continue
		}
		return append(dst, b)
	}
}

func matchRequestJSONLen(t *testing.T, body string, labels map[string]string) int {
	t.Helper()

	data, err := json.Marshal(fixture.MatchRequest{
		Provider: "gemini",
		Version:  "v1",
		Labels:   labels,
		Body:     json.RawMessage(body),
	})
	if err != nil {
		t.Fatalf("marshal matcher input: %v", err)
	}
	return len(data)
}

func assertGeminiError(t *testing.T, rr *httptest.ResponseRecorder, wantStatus int, wantStatusText, wantMessageFragment string) {
	t.Helper()

	if rr.Code != wantStatus {
		t.Fatalf("status: got %d, want %d", rr.Code, wantStatus)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}

	var envelope struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Error.Code != wantStatus {
		t.Fatalf("error code: got %d, want %d", envelope.Error.Code, wantStatus)
	}
	if envelope.Error.Status != wantStatusText {
		t.Fatalf("error status: got %q, want %q", envelope.Error.Status, wantStatusText)
	}
	if wantMessageFragment != "" && !strings.Contains(envelope.Error.Message, wantMessageFragment) {
		t.Fatalf("error message: got %q, want fragment %q", envelope.Error.Message, wantMessageFragment)
	}
}

func assertGeminiHighFidelityError(t *testing.T, rr *httptest.ResponseRecorder, wantStatus int, wantStatusText, wantReason string) {
	t.Helper()

	if rr.Code != wantStatus {
		t.Fatalf("status: got %d, want %d", rr.Code, wantStatus)
	}

	var envelope struct {
		Error struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Details []struct {
				Type   string `json:"@type"`
				Reason string `json:"reason"`
				Domain string `json:"domain"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Error.Code != wantStatus {
		t.Fatalf("error code: got %d, want %d", envelope.Error.Code, wantStatus)
	}
	if envelope.Error.Status != wantStatusText {
		t.Fatalf("error status: got %q, want %q", envelope.Error.Status, wantStatusText)
	}
	if len(envelope.Error.Details) == 0 {
		t.Fatal("expected error details in high fidelity error")
	}
	if envelope.Error.Details[0].Reason != wantReason {
		t.Fatalf("detail reason: got %q, want %q", envelope.Error.Details[0].Reason, wantReason)
	}
}

func sseDataPayloads(t *testing.T, body string) []string {
	t.Helper()

	blocks := strings.Split(strings.TrimSpace(body), "\n\n")
	payloads := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block == "" {
			continue
		}
		if !strings.HasPrefix(block, "data: ") {
			t.Fatalf("expected data-only SSE block, got %q", block)
		}
		payloads = append(payloads, strings.TrimPrefix(block, "data: "))
	}
	return payloads
}

func TestGenerateContent_InvalidJSON(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := newGeminiAdditionalHandler(t, runner, nil, nil)

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assertGeminiError(t, rr, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON")
}

func TestGenerateContent_SchemaValidationFailure(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := newGeminiAdditionalHandler(t, runner, nil, map[string][]byte{"v1": []byte(geminiRequestSchema)})

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assertGeminiError(t, rr, http.StatusBadRequest, "INVALID_ARGUMENT", "generationConfig")
}

func TestGenerateContent_EmptyContents(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := newGeminiAdditionalHandler(t, runner, nil, map[string][]byte{"v1": []byte(geminiRequestSchema)})

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader(`{"contents":[],"generationConfig":{"maxOutputTokens":4}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assertGeminiError(t, rr, http.StatusBadRequest, "INVALID_ARGUMENT", "contents is required")
}

func TestGenerateContent_MissingAuthUsesHighFidelityEnvelope(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := newGeminiAdditionalHandler(t, runner, nil, nil)

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assertGeminiHighFidelityError(t, rr, http.StatusForbidden, "PERMISSION_DENIED", "API_KEY_INVALID")
}

func TestGenerateContent_QueryKeyAuth(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := newGeminiAdditionalHandler(t, runner, nil, nil)

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath+"?key=test-key", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body: %s", rr.Code, rr.Body.String())
	}
}

func TestGenerateContent_InvalidJSONUsesHighFidelityEnvelope(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := newGeminiAdditionalHandler(t, runner, nil, nil)

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assertGeminiHighFidelityError(t, rr, http.StatusBadRequest, "INVALID_ARGUMENT", "REQUEST_VALIDATION_FAILED")
}

func TestGenerateContent_LocalRuntimeErrorBackendRateLimit(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := newGeminiAdditionalHandler(t, runner, nil, nil)

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			Backend:   runtimecfg.BackendError,
			ErrorType: runtimecfg.ErrorTypeRateLimit,
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assertGeminiHighFidelityError(t, rr, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED")
}

func TestGenerateContent_InvalidActionSuffix(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	h := newGeminiAdditionalHandler(t, runner, nil, nil)

	req := httptest.NewRequest(http.MethodPost, geminiInvalidActionPath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rr.Code)
	}
}

func TestGenerateContent_V1BetaRoutingUsesVersion(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	fixtureBody := fixtureGenerateContentBody(t, "beta fixture", 9, "fixture-beta")
	fixtures := []fixture.Fixture{
		compileGeminiFixture(t, runner, "beta-route", "v1beta", http.StatusCreated, fixtureBody, 0),
	}

	h := newGeminiAdditionalHandler(t, runner, fixtures, nil)

	req := httptest.NewRequest(http.MethodPost, geminiBetaGeneratePath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":4}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}
	if rr.Body.String() != string(fixtureBody) {
		t.Fatalf("body mismatch:\ngot  %s\nwant %s", rr.Body.String(), string(fixtureBody))
	}
}

func TestGenerateContent_FixtureResponse(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	fixtureBody := fixtureGenerateContentBody(t, "fixture text", 7, "fixture-model")
	fixtures := []fixture.Fixture{
		compileGeminiFixture(t, runner, "generate-match", "v1", http.StatusCreated, fixtureBody, 0),
	}
	h := newGeminiAdditionalHandler(t, runner, fixtures, nil)

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":4}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}

	var resp gemini.GenerateContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ModelVersion != "fixture-model" {
		t.Fatalf("modelVersion: got %q, want fixture-model", resp.ModelVersion)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("candidates: got %d, want 1", len(resp.Candidates))
	}
	if got := resp.Candidates[0].Content.Parts[0].Text; got != "fixture text" {
		t.Fatalf("text: got %q, want fixture text", got)
	}
	if resp.UsageMetadata.TotalTokenCount != 9 {
		t.Fatalf("totalTokenCount: got %d, want 9", resp.UsageMetadata.TotalTokenCount)
	}
}

func TestGenerateContent_LocalRuntimeFixtureResponseModelForceLiteral(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	fixtureBody := fixtureGenerateContentBody(t, "fixture text", 7, "fixture-model")
	fixtures := []fixture.Fixture{
		compileGeminiFixture(t, runner, "generate-match", "v1", http.StatusOK, fixtureBody, 0),
	}
	h := newGeminiAdditionalHandler(t, runner, fixtures, nil)

	req := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":4}}`))
	req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{
			ResponseModelPolicy: runtimecfg.ResponseModelForceLiteral,
			ResponseModel:       "gemini-local-model",
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp gemini.GenerateContentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ModelVersion != "gemini-local-model" {
		t.Fatalf("modelVersion: got %q, want gemini-local-model", resp.ModelVersion)
	}
}

func TestGenerateContent_StreamFixtureResponse(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	fixtureBody := fixtureGenerateContentBody(t, "alpha beta", 7, "fixture-model")
	fixtures := []fixture.Fixture{
		compileGeminiFixture(t, runner, "stream-match", "v1", http.StatusOK, fixtureBody, 0),
	}
	h := newGeminiAdditionalHandler(t, runner, fixtures, nil)

	req := httptest.NewRequest(http.MethodPost, geminiStreamPath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi there"}]}],"generationConfig":{"maxOutputTokens":4}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type: got %q, want text/event-stream", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control: got %q, want no-cache", got)
	}
	if got := rr.Header().Get("Connection"); got != "keep-alive" {
		t.Fatalf("connection: got %q, want keep-alive", got)
	}
	if got := rr.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("x-accel-buffering: got %q, want no", got)
	}

	payloads := sseDataPayloads(t, rr.Body.String())
	if len(payloads) != 2 {
		t.Fatalf("payloads: got %d, want 2\nbody:\n%s", len(payloads), rr.Body.String())
	}

	var first, second gemini.GenerateContentResponse
	if err := json.Unmarshal([]byte(payloads[0]), &first); err != nil {
		t.Fatalf("decode first payload: %v", err)
	}
	if err := json.Unmarshal([]byte(payloads[1]), &second); err != nil {
		t.Fatalf("decode second payload: %v", err)
	}

	if got := first.Candidates[0].Content.Parts[0].Text; got != "alpha " {
		t.Fatalf("first token: got %q, want %q", got, "alpha ")
	}
	var firstRaw map[string]any
	if err := json.Unmarshal([]byte(payloads[0]), &firstRaw); err != nil {
		t.Fatalf("decode first raw payload: %v", err)
	}
	firstCandidate := firstRaw["candidates"].([]any)[0].(map[string]any)
	if _, ok := firstCandidate["finishReason"]; ok {
		t.Fatalf("first chunk included finishReason, want omitted: %s", payloads[0])
	}
	if first.UsageMetadata.PromptTokenCount != 7 {
		t.Fatalf("first promptTokenCount: got %d, want 7", first.UsageMetadata.PromptTokenCount)
	}

	if got := second.Candidates[0].Content.Parts[0].Text; got != "beta" {
		t.Fatalf("second token: got %q, want beta", got)
	}
	if got := second.Candidates[0].FinishReason; got != "STOP" {
		t.Fatalf("second finishReason: got %q, want STOP", got)
	}
	if second.UsageMetadata.CandidatesTokenCount != 2 {
		t.Fatalf("second candidatesTokenCount: got %d, want 2", second.UsageMetadata.CandidatesTokenCount)
	}
	if second.UsageMetadata.TotalTokenCount != 9 {
		t.Fatalf("second totalTokenCount: got %d, want 9", second.UsageMetadata.TotalTokenCount)
	}
	if got := second.ModelVersion; got != geminiModel {
		t.Fatalf("second modelVersion: got %q, want %q", got, geminiModel)
	}
}

func TestGenerateContent_StreamFixtureFallbackWhenMalformed(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	fixtures := []fixture.Fixture{
		compileGeminiFixture(t, runner, "stream-fallback", "v1", http.StatusServiceUnavailable, []byte("{not json"), 0),
	}
	h := newGeminiAdditionalHandler(t, runner, fixtures, nil)

	req := httptest.NewRequest(http.MethodPost, geminiStreamPath, strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi there"}]}],"generationConfig":{"maxOutputTokens":4}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type: got %q, want application/json", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("cache-control: got %q, want empty", got)
	}
	if !strings.Contains(rr.Body.String(), "{not json") {
		t.Fatalf("body mismatch: got %q", rr.Body.String())
	}
}

func TestGenerateContent_LabelPropagationFromRoutingContext(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":4}}`
	routeLabels := map[string]string{"tenant": strings.Repeat("route-only-", 8)}

	baseLen := matchRequestJSONLen(t, body, map[string]string{})
	labeledLen := matchRequestJSONLen(t, body, routeLabels)
	if labeledLen <= baseLen {
		t.Fatalf("expected labels to increase matcher payload length: unlabeled=%d labeled=%d", baseLen, labeledLen)
	}

	fixtureBody := fixtureGenerateContentBody(t, "matched by labels", 11, "fixture-labels")
	fixtures := []fixture.Fixture{
		compileGeminiFixture(t, runner, "label-match", "v1", http.StatusAccepted, fixtureBody, uint32(baseLen+1)),
	}
	h := newGeminiAdditionalHandler(t, runner, fixtures, nil)

	labeledReq := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader(body))
	labeledReq.Header.Set("Content-Type", "application/json")
	labeledReq.Header.Set("x-goog-api-key", "test-key")
	labeledReq = labeledReq.WithContext(context.WithValue(labeledReq.Context(), router.LabelsKey{}, routeLabels))

	labeledRR := httptest.NewRecorder()
	h.ServeHTTP(labeledRR, labeledReq)

	if labeledRR.Code != http.StatusAccepted {
		t.Fatalf("labeled status: got %d, want 202", labeledRR.Code)
	}
	if labeledRR.Body.String() != string(fixtureBody) {
		t.Fatalf("labeled body mismatch:\ngot  %s\nwant %s", labeledRR.Body.String(), string(fixtureBody))
	}

	unlabeledReq := httptest.NewRequest(http.MethodPost, geminiGeneratePath, strings.NewReader(body))
	unlabeledReq.Header.Set("Content-Type", "application/json")
	unlabeledReq.Header.Set("x-goog-api-key", "test-key")

	unlabeledRR := httptest.NewRecorder()
	h.ServeHTTP(unlabeledRR, unlabeledReq)

	if unlabeledRR.Code != http.StatusOK {
		t.Fatalf("unlabeled status: got %d, want 200", unlabeledRR.Code)
	}
	if unlabeledRR.Body.String() == string(fixtureBody) {
		t.Fatalf("expected unlabeled request to fall back to lorem response")
	}
}
