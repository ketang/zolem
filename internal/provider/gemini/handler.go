package gemini

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
	"github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/provider/backend"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
	"github.com/ketang/zolem/internal/wasmgen"
	"github.com/ketang/zolem/internal/zolemerr"
)

type Handler struct {
	validator     *specs.Validator
	matcher       *fixture.Matcher
	generator     response.Generator
	wasmGenerator *wasmgen.Generator
	ollamaHTTP    backend.ChatGenerator
	mux           *chi.Mux
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaHTTP backend.ChatGenerator, wasmGenerator ...*wasmgen.Generator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator, ollamaHTTP: ollamaHTTP}
	if len(wasmGenerator) > 0 {
		h.wasmGenerator = wasmGenerator[0]
	}
	h.mux = chi.NewRouter()
	// chi uses ':param' syntax so colons in literal path segments break routing.
	// Use a catch-all under /v1/models/ and /v1beta/models/ and dispatch by
	// the action suffix (:generateContent vs :streamGenerateContent).
	h.mux.Post("/v1/models/*", h.handleCatchAll("v1"))
	h.mux.Post("/v1beta/models/*", h.handleCatchAll("v1beta"))
	h.mux.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Not found.")
	})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleCatchAll returns a handler that resolves the model name and action
// from the wildcard path segment (e.g. "gemini-2.0-flash:generateContent").
func (h *Handler) handleCatchAll(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wildcard := chi.URLParam(r, "*")

		colonIdx := strings.LastIndex(wildcard, ":")
		if colonIdx == -1 {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Not found.")
			return
		}
		model := wildcard[:colonIdx]
		action := wildcard[colonIdx+1:]

		if qIdx := strings.Index(action, "?"); qIdx != -1 {
			action = action[:qIdx]
		}

		var stream bool
		switch action {
		case "generateContent":
			stream = false
		case "streamGenerateContent":
			stream = true
		case "countTokens":
			h.handleCountTokens(w, r)
			return
		default:
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Not found.")
			return
		}

		h.handleGenerate(w, r, version, model, stream)
	}
}

func (h *Handler) handleGenerate(w http.ResponseWriter, r *http.Request, version, model string, stream bool) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}

	if r.Header.Get("x-goog-api-key") == "" && r.URL.Query().Get("key") == "" {
		writeForbidden(r.Context(), w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(r.Context(), w, "failed to read request body")
		return
	}

	if err := h.validator.Validate("gemini", version, body); err != nil {
		writeInvalidRequest(r.Context(), w, err.Error())
		return
	}

	var req GenerateContentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(r.Context(), w, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Contents) == 0 {
		writeInvalidRequest(r.Context(), w, "contents is required")
		return
	}

	if runtimecfg.UsesFixtures(r.Context()) {
		matchReq := fixture.MatchRequest{
			Provider: "gemini", Version: version,
			Labels: labelsFromContext(r.Context()),
			Body:   json.RawMessage(body),
		}
		matched, _ := h.matcher.Match(r.Context(), matchReq)
		if matched != nil {
			rendered, ok := renderFixtureBody(w, r.Context(), matched)
			if !ok {
				return
			}
			served := *matched
			served.ResponseBody = rendered
			serveFixture(w, r.Context(), &served, stream, model)
			return
		}
	}

	promptTokens := estimatePromptTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), model)

	if fd := geminiToolCallRequired(req); fd != nil {
		serveGeminiFunctionCallResponse(r.Context(), w, req, fd, responseModel, promptTokens, stream)
		return
	}

	cb := backend.Resolve(r.Context(), h.generator, h.ollamaHTTP, h.wasmGenerator)
	genReq := backend.GenerateRequest{
		Messages: geminiToChatMessages(req),
		Model:    model,
		FixtureMatch: &fixture.MatchRequest{
			Provider: "gemini", Version: version,
			Labels: labelsFromContext(r.Context()),
			Body:   json.RawMessage(body),
		},
	}

	if stream {
		streamGenerateContent(r.Context(), w, cb, genReq, responseModel, promptTokens)
		return
	}

	tokens, err := cb.Tokens(r.Context(), genReq)
	if err != nil {
		writeBackendError(w, err)
		return
	}
	candidateTokens := response.CountNonEmpty(tokens)
	resp := GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{Text: strings.Join(tokens, "")}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: candidateTokens,
			TotalTokenCount:      promptTokens + candidateTokens,
		},
		ModelVersion: responseModel,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func serveGeminiFunctionCallResponse(ctx context.Context, w http.ResponseWriter, req GenerateContentRequest, fd *FunctionDeclaration, model string, promptTokens int, stream bool) {
	args := backend.SynthArgs(fd.Parameters)
	fc := FunctionCall{Name: fd.Name, Args: json.RawMessage(args)}
	if stream {
		streamFunctionCallContent(ctx, w, fc, model, promptTokens)
		return
	}
	resp := GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{FunctionCall: &fc}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: 1,
			TotalTokenCount:      promptTokens + 1,
		},
		ModelVersion: model,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func renderFixtureBody(w http.ResponseWriter, ctx context.Context, f *fixture.Fixture) ([]byte, bool) {
	if !f.Templated {
		return f.ResponseBody, true
	}
	rt, ok := runtimecfg.ListenerRuntimeFromContext(ctx)
	if !ok {
		zolemerr.Write(w, fmt.Sprintf("fixture %q template requires local runtime metadata", f.ID))
		return nil, false
	}
	renderSeq := runtimecfg.IncrementTemplateRenderForRequest(ctx)
	body, err := fixture.RenderBody(*f, fixture.RenderInput{
		Runtime: fixture.RuntimeContext(rt),
		Sequence: fixture.TemplateSequenceContext{
			ProfileRequest: runtimecfg.ProfileRequestSequenceFromContext(ctx),
			TemplateRender: renderSeq,
		},
		Now: time.Now().UTC(),
	})
	if err != nil {
		zolemerr.Write(w, err.Error())
		return nil, false
	}
	return body, true
}

func serveFixture(w http.ResponseWriter, ctx context.Context, f *fixture.Fixture, stream bool, model string) {
	responseModel := runtimecfg.ResponseModelForRequest(ctx, model)
	if !stream {
		if _, ok := runtimecfg.ListenerRuntimeFromContext(ctx); ok {
			var resp GenerateContentResponse
			if err := json.Unmarshal(f.ResponseBody, &resp); err == nil {
				resp.ModelVersion = responseModel
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(f.Status)
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	var resp GenerateContentResponse
	if err := json.Unmarshal(f.ResponseBody, &resp); err != nil || len(resp.Candidates) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	text := ""
	if len(resp.Candidates[0].Content.Parts) > 0 {
		text = resp.Candidates[0].Content.Parts[0].Text
	}
	tokens := backend.Tokenize(text)
	streamResponse(ctx, w, responseModel, tokens, resp.UsageMetadata.PromptTokenCount)
}

func estimatePromptTokens(req GenerateContentRequest) int {
	total := 0
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			total += len(strings.Fields(p.Text)) + 4
		}
	}
	return total
}

func labelsFromContext(_ context.Context) map[string]string {
	return map[string]string{}
}

func geminiToChatMessages(req GenerateContentRequest) []ollama.ChatMessage {
	var messages []ollama.ChatMessage
	for _, content := range req.Contents {
		role := content.Role
		if role == "" {
			role = "user"
		}
		if role == "model" {
			role = "assistant"
		}
		var parts []string
		for _, part := range content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) == 0 {
			continue
		}
		messages = append(messages, ollama.ChatMessage{Role: role, Content: strings.Join(parts, " ")})
	}
	return messages
}
