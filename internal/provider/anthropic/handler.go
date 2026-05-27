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
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/ollama"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/router"
	runtimecfg "zolem.dev/zolem/internal/runtime"
	"zolem.dev/zolem/internal/specs"
	"zolem.dev/zolem/internal/wasmgen"
	"zolem.dev/zolem/internal/zolemerr"
)

type Handler struct {
	validator     *specs.Validator
	matcher       *fixture.Matcher
	generator     response.Generator
	wasmGenerator *wasmgen.Generator
	ollamaClient  textGenerator
	ollamaHTTP    chatGenerator
	mux           *chi.Mux
}

type textGenerator interface {
	Generate(context.Context, string) (string, error)
}

type chatGenerator interface {
	NonStreaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string) (string, error)
	Streaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string, fn func(delta string) error) error
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaClient textGenerator, ollamaHTTP chatGenerator, wasmGenerator ...*wasmgen.Generator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator, ollamaClient: ollamaClient, ollamaHTTP: ollamaHTTP}
	if len(wasmGenerator) > 0 {
		h.wasmGenerator = wasmGenerator[0]
	}
	h.mux = chi.NewRouter()
	h.mux.Post("/v1/messages", h.handleMessages)
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

	version := "v1"
	if strings.HasPrefix(r.URL.Path, "/v1beta") {
		version = "v1beta"
	}

	if err := h.validator.Validate("anthropic", version, body); err != nil {
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
			Version:  version,
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

	if runtimecfg.BackendForRequest(r.Context()) == runtimecfg.BackendOllama {
		h.handleOllamaBackend(w, r, req, responseModel, inputTokens)
		return
	}
	if runtimecfg.BackendForRequest(r.Context()) == runtimecfg.BackendWASM {
		matchReq := fixture.MatchRequest{
			Provider: "anthropic",
			Version:  version,
			Labels:   labelsFromContext(r.Context()),
			Body:     json.RawMessage(body),
		}
		tokens, err := h.generateWASM(r.Context(), matchReq)
		if err != nil {
			response.WriteZolemError(w, "wasm generator error: "+err.Error())
			return
		}
		if req.Stream {
			streamResponse(r.Context(), w, responseModel, tokens, inputTokens)
			return
		}
		resp := MessagesResponse{
			ID:         "msg_zolem_generated",
			Type:       "message",
			Role:       "assistant",
			Content:    []ContentBlock{{Type: "text", Text: strings.Join(tokens, "")}},
			Model:      responseModel,
			StopReason: "end_turn",
			Usage:      Usage{InputTokens: inputTokens, OutputTokens: response.CountNonEmpty(tokens)},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	if text, ok := h.generateText(r.Context(), promptFromRequest(req)); ok {
		if req.Stream {
			streamResponse(r.Context(), w, responseModel, tokenize(text), inputTokens)
			return
		}

		resp := MessagesResponse{
			ID:         "msg_zolem_ollama",
			Type:       "message",
			Role:       "assistant",
			Content:    []ContentBlock{{Type: "text", Text: text}},
			Model:      responseModel,
			StopReason: "end_turn",
			Usage:      Usage{InputTokens: inputTokens, OutputTokens: len(strings.Fields(text))},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	tokens := h.generator.Generate(30)
	if req.Stream {
		streamResponse(r.Context(), w, responseModel, tokens, inputTokens)
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
		Usage:      Usage{InputTokens: inputTokens, OutputTokens: len(tokens)},
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
	tokens := tokenize(text)
	streamResponse(ctx, w, responseModel, tokens, msg.Usage.InputTokens)
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

func promptFromRequest(req MessagesRequest) string {
	var lines []string
	if system := strings.TrimSpace(req.System.PlainText()); system != "" {
		lines = append(lines, "system: "+system)
	}
	for _, msg := range req.Messages {
		line := strings.TrimSpace(msg.Role + ": " + msg.Content.PlainText())
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

func (h *Handler) handleOllamaBackend(w http.ResponseWriter, r *http.Request, req MessagesRequest, responseModel string, inputTokens int) {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(r.Context())
	upstream := rt.Profile.OllamaUpstream
	if upstream == "" {
		upstream = "http://localhost:11434"
	}
	model := rt.Profile.BackendModel
	if model == "" {
		model = req.Model
	}

	messages := anthropicToChatMessages(req)

	if req.Stream {
		h.handleOllamaStream(w, r.Context(), upstream, messages, model, responseModel, inputTokens)
		return
	}

	text, err := h.ollamaHTTP.NonStreaming(r.Context(), upstream, messages, model)
	if err != nil {
		writeError(w, http.StatusBadGateway, "api_error", "ollama backend error: "+err.Error())
		return
	}

	resp := MessagesResponse{
		ID:         "msg_zolem_" + fmt.Sprintf("%016x", pseudoRandID()),
		Type:       "message",
		Role:       "assistant",
		Content:    []ContentBlock{{Type: "text", Text: text}},
		Model:      responseModel,
		StopReason: "end_turn",
		Usage:      Usage{InputTokens: inputTokens, OutputTokens: len(strings.Fields(text))},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleOllamaStream(w http.ResponseWriter, ctx context.Context, upstream string, messages []ollama.ChatMessage, model, responseModel string, inputTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	msgID := "msg_zolem_" + fmt.Sprintf("%016x", pseudoRandID())

	msgStart, _ := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []any{}, "model": responseModel,
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": inputTokens, "output_tokens": 1},
		},
	})
	sse.WriteEvent("message_start", msgStart)
	sse.Flush()

	cbStart, _ := json.Marshal(map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]string{"type": "text", "text": ""},
	})
	sse.WriteEvent("content_block_start", cbStart)
	sse.Flush()

	sse.WriteEvent("ping", []byte(`{"type":"ping"}`))
	sse.Flush()

	outputTokens := 0
	err := h.ollamaHTTP.Streaming(ctx, upstream, messages, model, func(delta string) error {
		outputTokens++
		d, _ := json.Marshal(map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]string{"type": "text_delta", "text": delta},
		})
		sse.WriteEvent("content_block_delta", d)
		sse.Flush()
		return nil
	})

	if err != nil {
		errData, _ := json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]string{"type": "api_error", "message": "ollama backend error: " + err.Error()},
		})
		sse.WriteEvent("error", errData)
		sse.Flush()
		return
	}

	sse.WriteEvent("content_block_stop", []byte(`{"type":"content_block_stop","index":0}`))
	sse.Flush()

	msgDelta, _ := json.Marshal(map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": outputTokens},
	})
	sse.WriteEvent("message_delta", msgDelta)
	sse.Flush()

	sse.WriteEvent("message_stop", []byte(`{"type":"message_stop"}`))
	sse.Flush()
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
