// Package typesafe serves a mock of TypeSafe's Jev System One API
// (POST /v1/systemone, GET /v1/models): a request/response, non-streaming
// surface that trades free text for typed judgments (choice/score/noul)
// with probabilities. It is structurally unlike the chat-completion
// providers zolem otherwise mocks (Anthropic, Gemini, Ollama, OpenAI), so
// this package does not reuse internal/provider/backend's token-generation
// abstractions; see synth.go for the lorem/faker answer synthesis this
// provider uses instead.
package typesafe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
)

// Handler serves the TypeSafe provider surface.
//
// validator and matcher are shared with the other provider packages'
// convention. generator is accepted for constructor-signature parity with the
// other providers (anthropic/openai/gemini/ollama all take one), but is
// unused: typesafe's lorem/faker backends synthesize typed answers directly
// (synth.go), not lorem/faker text tokens.
type Handler struct {
	validator *specs.Validator
	matcher   *fixture.Matcher
	generator response.Generator
	mux       *chi.Mux
}

// NewHandler matches the constructor convention shared by the anthropic,
// gemini, ollama, and openai provider packages. The variadic trailing
// parameters are accepted but unused, purely so callers can pass the same
// argument list buildLocalHandler already builds for every other provider
// without a provider-specific branch.
func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, _ ...any) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator}
	h.mux = chi.NewRouter()
	h.mux.Post("/v1/systemone", h.handleSystemOne)
	h.mux.Get("/v1/models", h.handleModels)
	h.mux.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeNotFound(w, "404 page not found")
	})
	h.mux.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// requireAuth reports whether the request carries an Authorization: Bearer
// header, writing a 401 and returning false if not. Per the issue, the key's
// contents are accepted and ignored — matching how zolem treats other
// providers' keys — but the header's presence is validated, matching real
// TypeSafe's requirement that the header exist.
func requireAuth(w http.ResponseWriter, r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") == "" {
		writeUnauthorized(w)
		return false
	}
	return true
}

func (h *Handler) handleSystemOne(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}
	if !requireAuth(w, r) {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(w, "failed to read request body")
		return
	}

	if err := h.validator.Validate("typesafe", "v1", body); err != nil {
		writeInvalidRequest(w, err.Error())
		return
	}

	var req Request
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
			Provider: "typesafe", Version: "v1",
			Labels: map[string]string{},
			Body:   json.RawMessage(body),
		}
		if matched, _ := h.matcher.Match(r.Context(), matchReq); matched != nil {
			h.serveFixture(w, r.Context(), matched, req)
			return
		}
	}

	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), req.Model)
	answers, err := answersForBackend(r.Context(), req, body)
	if err != nil {
		writeBackendError(w, err)
		return
	}
	if err := ValidateAnswers(req, answers); err != nil {
		var ve *AnswerValidationError
		if asAnswerValidationError(err, &ve) {
			writeAnswerValidationError(w, ve)
			return
		}
		writeBackendError(w, err)
		return
	}

	resp := Response{
		Model:   responseModel,
		Answers: answers,
		Usage:   estimateUsage(req, answers),
	}
	writeJSON(w, resp)
}

// answersForBackend synthesizes an answer for every question in req using
// the profile's configured backend. lorem and the "" (default) and "hybrid"
// legacy backends are deterministic; faker is seeded from the raw request
// body so identical requests always answer identically and a changed request
// (e.g. a different state) answers differently. The ollama and wasm backend
// names are accepted by profile validation generically (internal/runtime),
// but neither has typesafe-specific behavior yet: ollama-logprob is
// zolem-w0i (out of scope here), and a typesafe wasm backend is out of scope
// per the issue. Both surface a clear error instead of silently falling back,
// consistent with this provider's "cannot hallucinate" contract-enforcement
// goal.
func answersForBackend(ctx context.Context, req Request, rawBody []byte) (map[string]Answer, error) {
	backend := runtimecfg.BackendForRequest(ctx)
	switch backend {
	case "", runtimecfg.BackendHybrid, runtimecfg.BackendLorem:
		return answersWith(req, func(q Question, _ string) (Answer, error) {
			return answerLorem(q)
		})
	case runtimecfg.BackendFaker:
		seedBase := requestSeed(rawBody)
		return answersWith(req, func(q Question, key string) (Answer, error) {
			return answerFaker(q, key, seedBase)
		})
	default:
		return nil, fmt.Errorf("backend %q is not supported for the typesafe provider yet", backend)
	}
}

func answersWith(req Request, gen func(Question, string) (Answer, error)) (map[string]Answer, error) {
	answers := make(map[string]Answer, len(req.Questions))
	for key, q := range req.Questions {
		answer, err := gen(q, key)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", key, err)
		}
		answers[key] = answer
	}
	return answers, nil
}

// estimateUsage produces word-count-based token estimates. Zolem never runs a
// real tokenizer, matching the other providers' convention.
func estimateUsage(req Request, answers map[string]Answer) Usage {
	input := len(strings.Fields(string(req.State))) + 4
	for _, q := range req.Questions {
		input += len(strings.Fields(string(q.Instructions))) + len(strings.Fields(string(q.Criteria))) + 4
	}
	output := 0
	for range answers {
		output += 8
	}
	if output == 0 {
		output = 1
	}
	return Usage{InputTokens: input, OutputTokens: output}
}

func (h *Handler) serveFixture(w http.ResponseWriter, ctx context.Context, f *fixture.Fixture, req Request) {
	body, err := renderFixtureBodyBytes(ctx, f)
	if err != nil {
		writeBackendError(w, err)
		return
	}

	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		// Not a systemone envelope: serve the rendered bytes verbatim rather
		// than silently emitting an empty response, matching the other
		// providers' fixture-passthrough behavior.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		_, _ = w.Write(body)
		return
	}
	resp.Model = runtimecfg.ResponseModelForRequest(ctx, req.Model)

	if err := ValidateAnswers(req, resp.Answers); err != nil {
		var ve *AnswerValidationError
		if asAnswerValidationError(err, &ve) {
			writeAnswerValidationError(w, ve)
			return
		}
		writeBackendError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.Status)
	_ = json.NewEncoder(w).Encode(resp)
}

// renderFixtureBodyBytes expands a templated fixture body. Mirrors the
// equivalent helpers in the anthropic, gemini, ollama, and openai providers.
func renderFixtureBodyBytes(ctx context.Context, f *fixture.Fixture) ([]byte, error) {
	if !f.Templated {
		return f.ResponseBody, nil
	}
	rt, ok := runtimecfg.ListenerRuntimeFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("fixture %q template requires local runtime metadata", f.ID)
	}
	renderSeq := runtimecfg.IncrementTemplateRenderForRequest(ctx)
	return fixture.RenderBody(*f, fixture.RenderInput{
		Runtime: fixture.RuntimeContext(rt),
		Sequence: fixture.TemplateSequenceContext{
			ProfileRequest: runtimecfg.ProfileRequestSequenceFromContext(ctx),
			TemplateRender: renderSeq,
		},
		Now: time.Now().UTC(),
	})
}

func asAnswerValidationError(err error, out **AnswerValidationError) bool {
	if ve, ok := err.(*AnswerValidationError); ok {
		*out = ve
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
