package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ketang/zolem/internal/fixture"
	ollamaclient "github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/provider/backend"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/zolemerr"
)

// generateVersion is the synthetic schema/fixture version for /api/generate,
// kept separate from the chat "v1" so the two endpoints' fixtures never collide.
const generateVersion = "v1-generate"

// generateContext is the deterministic context array returned in the final
// object. Clients only pass it back.
var generateContext = []int{1, 2, 3}

const (
	doneReasonLoad   = "load"
	doneReasonUnload = "unload"
)

func (h *Handler) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(w, "failed to read request body")
		return
	}
	if err := h.validator.Validate("ollama", generateVersion, body); err != nil {
		writeInvalidRequest(w, err.Error())
		return
	}
	var req GenerateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(w, "model is required")
		return
	}
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), req.Model)

	// An empty prompt is a model load (or unload) request: one non-streamed
	// object, no metrics, no context.
	if req.Prompt == "" {
		reason := doneReasonLoad
		if keepAliveIsZero(req.KeepAlive) {
			reason = doneReasonUnload
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(GenerateResponse{
			Model: responseModel, CreatedAt: nowRFC3339Nano(), Done: true, DoneReason: reason,
		})
		return
	}

	matchReq := fixture.MatchRequest{
		Provider: "ollama", Version: generateVersion,
		Labels: map[string]string{},
		Body:   json.RawMessage(body),
	}
	if runtimecfg.UsesFixtures(r.Context()) {
		matched, err := h.matcher.Match(r.Context(), matchReq)
		if err != nil {
			writeFixtureSelectionError(w, err)
			return
		}
		if matched != nil {
			serveGenerateFixture(w, r.Context(), matched, req, responseModel)
			return
		}
	}

	messages := generateToChatMessages(req)
	promptTokens := countMessageWords(messages)

	cb := backend.Resolve(r.Context(), h.generator, h.ollamaHTTP, h.wasmGenerator)
	genReq := backend.GenerateRequest{Messages: messages, Model: req.Model, FixtureMatch: &matchReq}

	if req.streamRequested() {
		streamGenerate(r.Context(), w, cb, genReq, responseModel, promptTokens)
		return
	}

	start := time.Now()
	tokens, err := cb.Tokens(r.Context(), genReq)
	if err != nil {
		writeBackendError(w, err)
		return
	}
	m := computeMetrics(time.Since(start), promptTokens, response.CountNonEmpty(tokens))
	resp := GenerateResponse{
		Model: responseModel, CreatedAt: nowRFC3339Nano(),
		Response: strings.Join(tokens, ""), Done: true, DoneReason: doneReasonStop,
		Context:       generateContext,
		TotalDuration: m.total, LoadDuration: m.load,
		PromptEvalCount: promptTokens, PromptEvalDuration: m.promptEval,
		EvalCount: response.CountNonEmpty(tokens), EvalDuration: m.eval,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// keepAliveIsZero reports whether keep_alive asks for an immediate unload:
// the number 0 or the strings "0" / "0s".
func keepAliveIsZero(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n == 0
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		s = strings.TrimSpace(s)
		return s == "0" || s == "0s"
	}
	return false
}

// generateToChatMessages lowers system and prompt onto chat messages.
func generateToChatMessages(req GenerateRequest) []ollamaclient.ChatMessage {
	var msgs []ollamaclient.ChatMessage
	if req.System != "" {
		msgs = append(msgs, ollamaclient.ChatMessage{Role: "system", Content: req.System})
	}
	return append(msgs, ollamaclient.ChatMessage{Role: "user", Content: req.Prompt})
}

// countMessageWords mirrors estimatePromptTokens for chat requests.
func countMessageWords(msgs []ollamaclient.ChatMessage) int {
	total := 0
	for _, m := range msgs {
		total += len(strings.Fields(m.Content)) + 4
	}
	return total
}

func serveGenerateFixture(w http.ResponseWriter, ctx context.Context, f *fixture.Fixture, req GenerateRequest, responseModel string) {
	body, err := renderFixtureBodyBytes(ctx, f)
	if err != nil {
		zolemerr.Write(w, err.Error())
		return
	}
	if !req.streamRequested() {
		fixture.WriteVerbatim(w, f.Status, body, "model", responseModel)
		return
	}
	resp, ok := decodeGenerateEnvelope(body)
	if !ok || f.Status < 200 || f.Status > 299 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		_, _ = w.Write(body)
		return
	}
	resp.Model = responseModel
	streamGenerateFixture(ctx, w, resp)
}

// decodeGenerateEnvelope reports whether body is a generate response, i.e. a
// JSON object carrying a "response" field.
func decodeGenerateEnvelope(body []byte) (GenerateResponse, bool) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return GenerateResponse{}, false
	}
	if _, ok := probe["response"]; !ok {
		return GenerateResponse{}, false
	}
	var resp GenerateResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return GenerateResponse{}, false
	}
	return resp, true
}
