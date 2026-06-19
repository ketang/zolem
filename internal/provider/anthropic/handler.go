package anthropic

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
	validator  *specs.Validator
	matcher    *fixture.Matcher
	generator  response.Generator
	wasmGen    *wasmgen.Generator
	ollamaHTTP backend.ChatGenerator
	mux        *chi.Mux
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaHTTP backend.ChatGenerator, wasmGenerator ...*wasmgen.Generator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator, ollamaHTTP: ollamaHTTP}
	if len(wasmGenerator) > 0 {
		h.wasmGen = wasmGenerator[0]
	}
	h.mux = chi.NewRouter()
	h.mux.Post("/v1/messages", h.handleMessages)
	h.mux.Post("/v1/messages/count_tokens", h.handleCountTokens)
	h.mux.Get("/v1/models", h.handleListModels)
	h.mux.Get("/v1/models/{model}", h.handleListModels)
	h.mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeInvalidRequest(r.Context(), w, "Not Found")
	})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}

	if r.Header.Get("x-api-key") == "" {
		writeUnauthorized(r.Context(), w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(r.Context(), w, "failed to read request body")
		return
	}

	if err := h.validator.Validate("anthropic", "v1", body); err != nil {
		writeInvalidRequest(r.Context(), w, err.Error())
		return
	}

	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(r.Context(), w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(r.Context(), w, "model is required")
		return
	}
	if req.MaxTokens == 0 {
		writeInvalidRequest(r.Context(), w, "max_tokens is required")
		return
	}

	if runtimecfg.UsesFixtures(r.Context()) {
		matchReq := fixture.MatchRequest{
			Provider: "anthropic",
			Version:  "v1",
			Labels:   labelsFromContext(r.Context()),
			Body:     json.RawMessage(body),
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

	inputTokens := estimateInputTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), req.Model)

	if required, name := anthropicToolChoiceRequiresCall(req.ToolChoice); required {
		if tool := pickAnthropicTool(req.Tools, name); tool != nil {
			serveAnthropicToolCallResponse(r.Context(), w, req, tool, responseModel, inputTokens)
			return
		}
	}

	cb := backend.Resolve(r.Context(), h.generator, h.ollamaHTTP, h.wasmGen)
	genReq := backend.GenerateRequest{
		Messages: anthropicToChatMessages(req),
		Model:    req.Model,
		FixtureMatch: &fixture.MatchRequest{
			Provider: "anthropic", Version: "v1",
			Labels: labelsFromContext(r.Context()),
			Body:   json.RawMessage(body),
		},
	}

	if req.Stream {
		streamMessages(r.Context(), w, cb, genReq, responseModel, inputTokens)
		return
	}

	tokens, err := cb.Tokens(r.Context(), genReq)
	if err != nil {
		writeBackendError(w, err)
		return
	}
	resp := MessagesResponse{
		ID:         newMessageID(),
		Type:       "message",
		Role:       "assistant",
		Content:    []ContentBlock{{Type: "text", Text: strings.Join(tokens, "")}},
		Model:      responseModel,
		StopReason: "end_turn",
		Usage:      Usage{InputTokens: inputTokens, OutputTokens: response.CountNonEmpty(tokens)},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func serveAnthropicToolCallResponse(ctx context.Context, w http.ResponseWriter, req MessagesRequest, tool *AnthropicTool, model string, inputTokens int) {
	args := backend.SynthArgs(tool.InputSchema)
	block := ContentBlock{
		Type:  "tool_use",
		ID:    newToolUseID(),
		Name:  tool.Name,
		Input: json.RawMessage(args),
	}
	if req.Stream {
		streamToolUseResponse(ctx, w, model, block, inputTokens, 1)
		return
	}
	resp := MessagesResponse{
		ID:         newMessageID(),
		Type:       "message",
		Role:       "assistant",
		Content:    []ContentBlock{block},
		Model:      model,
		StopReason: "tool_use",
		Usage:      Usage{InputTokens: inputTokens, OutputTokens: 1},
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

func serveFixture(w http.ResponseWriter, ctx context.Context, f *fixture.Fixture, req MessagesRequest) {
	responseModel := runtimecfg.ResponseModelForRequest(ctx, req.Model)
	if !req.Stream {
		if _, ok := runtimecfg.ListenerRuntimeFromContext(ctx); ok {
			var msg MessagesResponse
			if err := json.Unmarshal(f.ResponseBody, &msg); err == nil {
				msg.Model = responseModel
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(f.Status)
				_ = json.NewEncoder(w).Encode(msg)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	var msg MessagesResponse
	if err := json.Unmarshal(f.ResponseBody, &msg); err != nil || len(msg.Content) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	if msg.Content[0].Type == "tool_use" {
		streamToolUseResponse(ctx, w, responseModel, msg.Content[0], msg.Usage.InputTokens, msg.Usage.OutputTokens)
		return
	}
	text := msg.Content[0].Text
	tokens := backend.Tokenize(text)
	streamResponse(ctx, w, responseModel, tokens, msg.Usage.InputTokens)
}

func estimateInputTokens(req MessagesRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(strings.Fields(m.Content.PlainText())) + 4
	}
	return total
}

func labelsFromContext(_ context.Context) map[string]string {
	return map[string]string{}
}

func anthropicToChatMessages(req MessagesRequest) []ollama.ChatMessage {
	var messages []ollama.ChatMessage
	if system := strings.TrimSpace(req.System.PlainText()); system != "" {
		messages = append(messages, ollama.ChatMessage{Role: "system", Content: system})
	}
	for _, msg := range req.Messages {
		text := msg.Content.PlainText()
		if text == "" {
			continue
		}
		messages = append(messages, ollama.ChatMessage{Role: msg.Role, Content: text})
	}
	return messages
}
