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
	"zolem.dev/zolem/internal/specs"
)

type Handler struct {
	validator *specs.Validator
	matcher   *fixture.Matcher
	generator response.Generator
	mux       *chi.Mux
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator}
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

	matchReq := fixture.MatchRequest{
		Provider: "openai", Version: "v1",
		Labels: labelsFromContext(r.Context()),
		Body:   json.RawMessage(body),
	}
	matched, _ := h.matcher.Match(r.Context(), matchReq)
	if matched != nil {
		serveFixture(w, matched, req)
		return
	}

	tokens := h.generator.Generate(30)
	promptTokens := estimatePromptTokens(req)

	if req.Stream {
		streamResponse(w, req.Model, tokens, promptTokens)
		return
	}

	text := strings.Join(tokens, "")
	resp := ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: text}, FinishReason: "stop"}},
		Usage:   Usage{PromptTokens: promptTokens, CompletionTokens: len(tokens), TotalTokens: promptTokens + len(tokens)},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func serveFixture(w http.ResponseWriter, f *fixture.Fixture, req ChatCompletionRequest) {
	if !req.Stream {
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
	streamResponse(w, resp.Model, tokens, resp.Usage.PromptTokens)
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

func labelsFromContext(ctx context.Context) map[string]string {
	if v := ctx.Value(router.LabelsKey{}); v != nil {
		if labels, ok := v.(map[string]string); ok {
			return labels
		}
	}
	return map[string]string{}
}
