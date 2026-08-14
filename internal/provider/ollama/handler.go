// Package ollama serves a mock of Ollama's *native* HTTP API (/api/chat and
// friends), so authors of Ollama clients can develop against zolem with no
// model pulled and no GPU.
//
// This is distinct from internal/ollama, which is the client zolem uses to
// forward generation to a real Ollama instance when a profile selects the
// `ollama` *backend*. Provider and backend are orthogonal axes: this package
// is the surface zolem impersonates, not the source of content.
package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ketang/zolem/internal/fixture"
	ollamaclient "github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/provider/backend"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
	"github.com/ketang/zolem/internal/wasmgen"
)

type Handler struct {
	validator     *specs.Validator
	matcher       *fixture.Matcher
	generator     response.Generator
	wasmGenerator *wasmgen.Generator
	ollamaHTTP    backend.ChatGenerator
	mux           *chi.Mux
}

// NewHandler matches the constructor convention shared by the anthropic,
// openai, and gemini provider packages.
//
// ollamaHTTP and wasmGenerator are passed through unchanged: backend selection
// happens generically in backend.Resolve, so every backend (lorem, faker,
// fixture, ollama, wasm, error) works for this provider with no
// provider-specific code.
func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaHTTP backend.ChatGenerator, wasmGenerator ...*wasmgen.Generator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator, ollamaHTTP: ollamaHTTP}
	if len(wasmGenerator) > 0 {
		h.wasmGenerator = wasmGenerator[0]
	}
	h.mux = chi.NewRouter()
	h.mux.Post("/api/chat", h.handleChat)
	h.mux.Get("/api/tags", h.handleTags)
	h.mux.Get("/api/version", h.handleVersion)
	h.mux.Post("/api/show", h.handleShow)
	h.mux.Get("/api/ps", h.handlePS)
	h.mux.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeNotFound(w, "404 page not found")
	})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}

	// No authentication check: Ollama's native API is unauthenticated, unlike
	// every other provider zolem serves.

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(w, "failed to read request body")
		return
	}

	if err := h.validator.Validate("ollama", "v1", body); err != nil {
		writeInvalidRequest(w, err.Error())
		return
	}

	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(w, "model is required")
		return
	}

	if runtimecfg.UsesFixtures(r.Context()) {
		matchReq := fixture.MatchRequest{
			Provider: "ollama", Version: "v1",
			Labels: map[string]string{},
			Body:   json.RawMessage(body),
		}
		if matched, _ := h.matcher.Match(r.Context(), matchReq); matched != nil {
			serveFixture(w, r.Context(), matched, req)
			return
		}
	}

	promptTokens := estimatePromptTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), req.Model)

	cb := backend.Resolve(r.Context(), h.generator, h.ollamaHTTP, h.wasmGenerator)
	genReq := backend.GenerateRequest{
		Messages: ollamaToChatMessages(req),
		Model:    req.Model,
		FixtureMatch: &fixture.MatchRequest{
			Provider: "ollama", Version: "v1",
			Labels: map[string]string{},
			Body:   json.RawMessage(body),
		},
	}

	if streamRequested(req) {
		streamChat(r.Context(), w, cb, genReq, responseModel, promptTokens)
		return
	}

	start := time.Now()
	tokens, err := cb.Tokens(r.Context(), genReq)
	if err != nil {
		writeBackendError(w, err)
		return
	}
	elapsed := time.Since(start)

	text := strings.Join(tokens, "")
	resp := ChatResponse{
		Model:      responseModel,
		CreatedAt:  nowRFC3339Nano(),
		Message:    Message{Role: "assistant", Content: text},
		Done:       true,
		DoneReason: doneReasonStop,
	}
	applyMetrics(&resp, elapsed, promptTokens, response.CountNonEmpty(tokens))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

const doneReasonStop = "stop"

func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// applyMetrics fills the final-object metrics block. Durations are reported in
// nanoseconds, Ollama's native unit. The segments are apportioned from the
// measured elapsed time so the parts always stay consistent with the whole
// (load + prompt_eval + eval <= total), which is the invariant tests assert.
func applyMetrics(resp *ChatResponse, elapsed time.Duration, promptTokens, evalTokens int) {
	total := elapsed.Nanoseconds()
	if total <= 0 {
		// A backend can complete faster than the clock resolution; keep the
		// counters strictly positive so consumers never divide by zero.
		total = 1
	}
	load := total / 10
	promptEval := total / 10
	eval := total - load - promptEval

	resp.TotalDuration = total
	resp.LoadDuration = load
	resp.PromptEvalCount = promptTokens
	resp.PromptEvalDuration = promptEval
	resp.EvalCount = evalTokens
	resp.EvalDuration = eval
}

// estimatePromptTokens mirrors the word-count-plus-overhead estimate the other
// providers use. Zolem never runs a real tokenizer.
func estimatePromptTokens(req ChatRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(strings.Fields(m.Content)) + 4
	}
	if total == 0 {
		total = 1
	}
	return total
}

// ollamaToChatMessages lowers the native request onto the provider-agnostic
// wire format the content backends consume. Ollama message content is already
// a plain string, so no parts-array flattening is needed.
func ollamaToChatMessages(req ChatRequest) []ollamaclient.ChatMessage {
	messages := make([]ollamaclient.ChatMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		if msg.Content == "" {
			continue
		}
		messages = append(messages, ollamaclient.ChatMessage{Role: msg.Role, Content: msg.Content})
	}
	return messages
}

func serveFixture(w http.ResponseWriter, ctx context.Context, f *fixture.Fixture, req ChatRequest) {
	responseModel := runtimecfg.ResponseModelForRequest(ctx, req.Model)

	var resp ChatResponse
	if err := json.Unmarshal(f.ResponseBody, &resp); err != nil {
		// Not a chat envelope: serve the fixture bytes verbatim.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		_, _ = w.Write(f.ResponseBody)
		return
	}
	resp.Model = responseModel

	if !streamRequested(req) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	streamFixture(ctx, w, resp)
}
