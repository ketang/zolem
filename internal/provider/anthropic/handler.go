package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/router"
	runtimecfg "zolem.dev/zolem/internal/runtime"
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
	h.mux.Post("/v1/messages", h.handleMessages)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("x-api-key") == "" {
		writeUnauthorized(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(w, "failed to read request body")
		return
	}

	version := "v1"
	if strings.HasPrefix(r.URL.Path, "/v1beta") {
		version = "v1beta"
	}

	if err := h.validator.Validate("anthropic", version, body); err != nil {
		writeInvalidRequest(w, err.Error())
		return
	}

	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(w, "model is required")
		return
	}
	if req.MaxTokens == 0 {
		writeInvalidRequest(w, "max_tokens is required")
		return
	}

	if runtimecfg.UsesFixtures(r.Context()) {
		matchReq := fixture.MatchRequest{
			Provider: "anthropic",
			Version:  version,
			Labels:   labelsFromContext(r.Context()),
			Body:     json.RawMessage(body),
		}
		matched, _ := h.matcher.Match(r.Context(), matchReq)

		if matched != nil {
			serveFixture(w, r.Context(), matched, req)
			return
		}
	}

	tokens := h.generator.Generate(30)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), req.Model)
	if req.Stream {
		streamResponse(w, responseModel, tokens, estimateInputTokens(req))
		return
	}

	text := strings.Join(tokens, "")
	resp := MessagesResponse{
		ID:         "msg_zolem_generated",
		Type:       "message",
		Role:       "assistant",
		Content:    []ContentBlock{{Type: "text", Text: text}},
		Model:      responseModel,
		StopReason: "end_turn",
		Usage:      Usage{InputTokens: estimateInputTokens(req), OutputTokens: len(tokens)},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
	text := msg.Content[0].Text
	tokens := tokenize(text)
	streamResponse(w, responseModel, tokens, msg.Usage.InputTokens)
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

func estimateInputTokens(req MessagesRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(strings.Fields(m.Content.PlainText())) + 4
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
