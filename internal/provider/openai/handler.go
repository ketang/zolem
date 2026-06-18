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
	"github.com/ketang/zolem/internal/router"
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
	ollamaClient  backend.TextGenerator
	ollamaHTTP    backend.ChatGenerator
	mux           *chi.Mux
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaClient backend.TextGenerator, ollamaHTTP backend.ChatGenerator, wasmGenerator ...*wasmgen.Generator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator, ollamaClient: ollamaClient, ollamaHTTP: ollamaHTTP}
	if len(wasmGenerator) > 0 {
		h.wasmGenerator = wasmGenerator[0]
	}
	h.mux = chi.NewRouter()
	h.mux.Post("/v1/chat/completions", h.handleChatCompletions)
	h.mux.Get("/v1/responses", h.handleResponses)
	h.mux.Get("/v1/models", h.handleListModels)
	h.mux.Get("/v1/models/*", h.handleListModels)
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

	if runtimecfg.BackendForRequest(r.Context()) == runtimecfg.BackendOllama {
		h.handleOllamaBackend(w, r, req, responseModel, promptTokens)
		return
	}
	if runtimecfg.BackendForRequest(r.Context()) == runtimecfg.BackendWASM {
		matchReq := fixture.MatchRequest{
			Provider: "openai", Version: "v1",
			Labels: labelsFromContext(r.Context()),
			Body:   json.RawMessage(body),
		}
		tokens, err := h.generateWASM(r.Context(), matchReq)
		if err != nil {
			response.WriteZolemError(w, "wasm generator error: "+err.Error())
			return
		}
		if req.Stream {
			streamResponse(r.Context(), w, responseModel, tokens, promptTokens, includeUsage(req))
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
		return
	}

	if text, ok := h.generateText(r.Context(), promptFromRequest(req)); ok {
		completionTokens := len(strings.Fields(text))
		if req.Stream {
			streamResponse(r.Context(), w, responseModel, tokenize(text), promptTokens, includeUsage(req))
			return
		}

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
		return
	}

	tokens := h.generator.Generate(30)

	if req.Stream {
		streamResponse(r.Context(), w, responseModel, tokens, promptTokens, includeUsage(req))
		return
	}

	text := strings.Join(tokens, "")
	resp := ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   responseModel,
		Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: text}, FinishReason: "stop"}},
		Usage:   Usage{PromptTokens: promptTokens, CompletionTokens: len(tokens), TotalTokens: promptTokens + len(tokens)},
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
	tokens := tokenize(resp.Choices[0].Message.Content)
	streamResponse(ctx, w, responseModel, tokens, resp.Usage.PromptTokens, includeUsage(req))
}

func includeUsage(req ChatCompletionRequest) bool {
	return req.StreamOptions != nil && req.StreamOptions.IncludeUsage
}

func tokenize(text string) []string {
	words := strings.Fields(text)
	tokens := make([]string, len(words))
	for i, w := range words {
		if i < len(words)-1 {
			tokens[i] = w + " "
		} else {
			tokens[i] = w
		}
	}
	return tokens
}

func estimatePromptTokens(req ChatCompletionRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(strings.Fields(m.Content.Text())) + 4
	}
	return total
}

func promptFromRequest(req ChatCompletionRequest) string {
	var lines []string
	for _, msg := range req.Messages {
		line := strings.TrimSpace(msg.Role + ": " + msg.Content.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) generateText(ctx context.Context, prompt string) (string, bool) {
	if h.ollamaClient == nil {
		return "", false
	}

	text, err := h.ollamaClient.Generate(ctx, prompt)
	if err != nil {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func (h *Handler) generateWASM(ctx context.Context, req fixture.MatchRequest) ([]string, error) {
	if h.wasmGenerator == nil {
		return nil, fmt.Errorf("wasm generator is not configured")
	}
	return h.wasmGenerator.Generate(ctx, req)
}

func labelsFromContext(ctx context.Context) map[string]string {
	if v := ctx.Value(router.LabelsKey{}); v != nil {
		if labels, ok := v.(map[string]string); ok {
			return labels
		}
	}
	return map[string]string{}
}

func (h *Handler) handleOllamaBackend(w http.ResponseWriter, r *http.Request, req ChatCompletionRequest, responseModel string, promptTokens int) {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(r.Context())
	upstream := rt.Profile.OllamaUpstream
	if upstream == "" {
		upstream = "http://localhost:11434"
	}
	model := rt.Profile.BackendModel
	if model == "" {
		model = req.Model
	}

	messages := openaiToChatMessages(req)

	if req.Stream {
		h.handleOllamaStream(w, r.Context(), upstream, messages, model, responseModel, promptTokens, includeUsage(req))
		return
	}

	text, err := h.ollamaHTTP.NonStreaming(r.Context(), upstream, messages, model)
	if err != nil {
		writeError(w, http.StatusBadGateway, "server_error", "ollama backend error: "+err.Error(), nil)
		return
	}

	completionTokens := len(strings.Fields(text))
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

func (h *Handler) handleOllamaStream(w http.ResponseWriter, ctx context.Context, upstream string, messages []ollama.ChatMessage, model, responseModel string, promptTokens int, includeUsage bool) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	id := fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano())
	created := time.Now().Unix()

	firstChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": responseModel,
		"choices": []map[string]any{{"index": 0, "delta": map[string]string{"role": "assistant", "content": ""}, "finish_reason": nil}},
	}
	data, _ := json.Marshal(firstChunk)
	sse.WriteData(data)
	sse.Flush()

	completionTokens := 0
	err := h.ollamaHTTP.Streaming(ctx, upstream, messages, model, func(delta string) error {
		completionTokens++
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": responseModel,
			"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": delta}, "finish_reason": nil}},
		}
		d, _ := json.Marshal(chunk)
		sse.WriteData(d)
		sse.Flush()
		return nil
	})

	if err != nil {
		errChunk := map[string]any{"error": map[string]string{"message": "ollama backend error: " + err.Error(), "type": "server_error"}}
		d, _ := json.Marshal(errChunk)
		sse.WriteData(d)
		sse.Flush()
		return
	}

	stop := "stop"
	finalChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": responseModel,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": stop}},
	}
	data, _ = json.Marshal(finalChunk)
	sse.WriteData(data)
	sse.Flush()

	if includeUsage {
		usageChunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": responseModel,
			"choices": []any{},
			"usage": map[string]int{
				"prompt_tokens": promptTokens, "completion_tokens": completionTokens,
				"total_tokens": promptTokens + completionTokens,
			},
		}
		data, _ = json.Marshal(usageChunk)
		sse.WriteData(data)
		sse.Flush()
	}

	sse.WriteData([]byte("[DONE]"))
	sse.Flush()
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
