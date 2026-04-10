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
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/router"
	runtimecfg "zolem.dev/zolem/internal/runtime"
	"zolem.dev/zolem/internal/specs"
)

type Handler struct {
	validator    *specs.Validator
	matcher      *fixture.Matcher
	generator    response.Generator
	ollamaClient textGenerator
	mux          *chi.Mux
}

type textGenerator interface {
	Generate(context.Context, string) (string, error)
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaClient textGenerator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator, ollamaClient: ollamaClient}
	h.mux = chi.NewRouter()
	h.mux.Post("/v1/chat/completions", h.handleChatCompletions)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		writeUnauthorized(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(w, "failed to read request body")
		return
	}

	if err := h.validator.Validate("openai", "v1", body); err != nil {
		writeInvalidRequest(w, err.Error())
		return
	}

	var req ChatCompletionRequest
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
			Provider: "openai", Version: "v1",
			Labels: labelsFromContext(r.Context()),
			Body:   json.RawMessage(body),
		}
		matched, _ := h.matcher.Match(r.Context(), matchReq)
		if matched != nil {
			serveFixture(w, r.Context(), matched, req)
			return
		}
	}

	promptTokens := estimatePromptTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), req.Model)

	if text, ok := h.generateText(r.Context(), promptFromRequest(req)); ok {
		completionTokens := len(strings.Fields(text))
		if req.Stream {
			streamResponse(w, responseModel, tokenize(text), promptTokens)
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
		streamResponse(w, responseModel, tokens, promptTokens)
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
	streamResponse(w, responseModel, tokens, resp.Usage.PromptTokens)
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
		total += len(strings.Fields(m.Content)) + 4
	}
	return total
}

func promptFromRequest(req ChatCompletionRequest) string {
	var lines []string
	for _, msg := range req.Messages {
		line := strings.TrimSpace(msg.Role + ": " + msg.Content)
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

func labelsFromContext(ctx context.Context) map[string]string {
	if v := ctx.Value(router.LabelsKey{}); v != nil {
		if labels, ok := v.(map[string]string); ok {
			return labels
		}
	}
	return map[string]string{}
}
