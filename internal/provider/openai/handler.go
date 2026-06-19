package openai

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
	h.mux.Post("/v1/chat/completions", h.handleChatCompletions)
	h.mux.Get("/v1/responses", h.handleResponses)
	h.mux.Get("/v1/models", h.handleListModels)
	h.mux.Get("/v1/models/*", h.handleListModels)
	h.mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeInvalidRequest(r.Context(), w, "Not Found")
	})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		writeUnauthorized(r.Context(), w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(r.Context(), w, "failed to read request body")
		return
	}

	if err := h.validator.Validate("openai", "v1", body); err != nil {
		writeInvalidRequest(r.Context(), w, err.Error())
		return
	}

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(r.Context(), w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(r.Context(), w, "model is required")
		return
	}

	if runtimecfg.UsesFixtures(r.Context()) {
		matchReq := fixture.MatchRequest{
			Provider: "openai", Version: "v1",
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
			serveFixture(w, r.Context(), &served, req)
			return
		}
	}

	promptTokens := estimatePromptTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), req.Model)

	cb := backend.Resolve(r.Context(), h.generator, h.ollamaHTTP, h.wasmGenerator)
	genReq := backend.GenerateRequest{
		Messages: openaiToChatMessages(req),
		Model:    req.Model,
		FixtureMatch: &fixture.MatchRequest{
			Provider: "openai", Version: "v1",
			Labels: labelsFromContext(r.Context()),
			Body:   json.RawMessage(body),
		},
	}

	if req.Stream {
		streamChatCompletions(r.Context(), w, cb, genReq, responseModel, promptTokens, includeUsage(req))
		return
	}

	tokens, err := cb.Tokens(r.Context(), genReq)
	if err != nil {
		writeBackendError(w, err)
		return
	}
	text := strings.Join(tokens, "")
	completionTokens := response.CountNonEmpty(tokens)
	resp := ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   responseModel,
		Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: text}, FinishReason: "stop"}},
		Usage:   Usage{PromptTokens: promptTokens, CompletionTokens: completionTokens, TotalTokens: promptTokens + completionTokens},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func renderFixtureBody(w http.ResponseWriter, ctx context.Context, f *fixture.Fixture) ([]byte, bool) {
	body, err := renderFixtureBodyBytes(ctx, f)
	if err != nil {
		zolemerr.Write(w, err.Error())
		return nil, false
	}
	return body, true
}

func renderFixtureBodyBytes(ctx context.Context, f *fixture.Fixture) ([]byte, error) {
	if !f.Templated {
		return f.ResponseBody, nil
	}
	rt, ok := runtimecfg.ListenerRuntimeFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("fixture %q template requires local runtime metadata", f.ID)
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
		return nil, err
	}
	return body, nil
}

func serveFixture(w http.ResponseWriter, ctx context.Context, f *fixture.Fixture, req ChatCompletionRequest) {
	responseModel := runtimecfg.ResponseModelForRequest(ctx, req.Model)
	if !req.Stream {
		if _, ok := runtimecfg.ListenerRuntimeFromContext(ctx); ok {
			var resp ChatCompletionResponse
			if err := json.Unmarshal(f.ResponseBody, &resp); err == nil {
				resp.Model = responseModel
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
	var resp ChatCompletionResponse
	if err := json.Unmarshal(f.ResponseBody, &resp); err != nil || len(resp.Choices) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	tokens := backend.Tokenize(resp.Choices[0].Message.Content)
	streamResponse(ctx, w, responseModel, tokens, resp.Usage.PromptTokens, includeUsage(req))
}

func includeUsage(req ChatCompletionRequest) bool {
	return req.StreamOptions != nil && req.StreamOptions.IncludeUsage
}

func estimatePromptTokens(req ChatCompletionRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(strings.Fields(m.Content.Text())) + 4
	}
	return total
}

func labelsFromContext(_ context.Context) map[string]string {
	return map[string]string{}
}

func openaiToChatMessages(req ChatCompletionRequest) []ollama.ChatMessage {
	messages := make([]ollama.ChatMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		content := msg.Content.Text()
		if content == "" {
			continue
		}
		messages = append(messages, ollama.ChatMessage{Role: msg.Role, Content: content})
	}
	return messages
}
