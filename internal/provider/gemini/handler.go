package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/ollama"
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
	ollamaHTTP   chatGenerator
	mux          *chi.Mux
}

type textGenerator interface {
	Generate(context.Context, string) (string, error)
}

type chatGenerator interface {
	NonStreaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string) (string, error)
	Streaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string, fn func(delta string) error) error
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaClient textGenerator, ollamaHTTP chatGenerator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, generator: generator, ollamaClient: ollamaClient, ollamaHTTP: ollamaHTTP}
	h.mux = chi.NewRouter()
	// chi uses ':param' syntax so colons in literal path segments break routing.
	// Use a catch-all under /v1/models/ and /v1beta/models/ and dispatch by
	// the action suffix (:generateContent vs :streamGenerateContent).
	h.mux.Post("/v1/models/*", h.handleCatchAll("v1"))
	h.mux.Post("/v1beta/models/*", h.handleCatchAll("v1beta"))
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleCatchAll returns a handler that resolves the model name and action
// from the wildcard path segment (e.g. "gemini-2.0-flash:generateContent").
func (h *Handler) handleCatchAll(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// wildcard contains everything after /v1/models/ or /v1beta/models/
		wildcard := chi.URLParam(r, "*")

		// split on ':' to get model and action
		colonIdx := strings.LastIndex(wildcard, ":")
		if colonIdx == -1 {
			http.NotFound(w, r)
			return
		}
		model := wildcard[:colonIdx]
		action := wildcard[colonIdx+1:]

		// strip any query string from action (e.g. "streamGenerateContent?alt=sse")
		if qIdx := strings.Index(action, "?"); qIdx != -1 {
			action = action[:qIdx]
		}

		var stream bool
		switch action {
		case "generateContent":
			stream = false
		case "streamGenerateContent":
			stream = true
		default:
			http.NotFound(w, r)
			return
		}

		h.handleGenerate(w, r, version, model, stream)
	}
}

func (h *Handler) handleGenerate(w http.ResponseWriter, r *http.Request, version, model string, stream bool) {
	if r.Header.Get("x-goog-api-key") == "" {
		writeForbidden(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(w, "failed to read request body")
		return
	}

	if err := h.validator.Validate("gemini", version, body); err != nil {
		writeInvalidRequest(w, err.Error())
		return
	}

	var req GenerateContentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Contents) == 0 {
		writeInvalidRequest(w, "contents is required")
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
			serveFixture(w, r.Context(), matched, stream, model)
			return
		}
	}

	if runtimecfg.BackendForRequest(r.Context()) == runtimecfg.BackendOllama {
		h.handleOllamaBackend(w, r, req, version, model, stream)
		return
	}

	promptTokens := estimatePromptTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), model)

	if text, ok := h.generateText(r.Context(), promptFromRequest(req)); ok {
		completionTokens := len(strings.Fields(text))
		if stream {
			streamResponse(w, responseModel, tokenize(text), promptTokens)
			return
		}

		resp := GenerateContentResponse{
			Candidates: []Candidate{{
				Content:      Content{Parts: []Part{{Text: text}}, Role: "model"},
				FinishReason: "STOP",
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount:     promptTokens,
				CandidatesTokenCount: completionTokens,
				TotalTokenCount:      promptTokens + completionTokens,
			},
			ModelVersion: responseModel,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	tokens := h.generator.Generate(30)

	if stream {
		streamResponse(w, responseModel, tokens, promptTokens)
		return
	}

	text := strings.Join(tokens, "")
	resp := GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{Text: text}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: len(tokens),
			TotalTokenCount:      promptTokens + len(tokens),
		},
		ModelVersion: responseModel,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
	tokens := tokenize(text)
	streamResponse(w, responseModel, tokens, resp.UsageMetadata.PromptTokenCount)
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

func estimatePromptTokens(req GenerateContentRequest) int {
	total := 0
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			total += len(strings.Fields(p.Text)) + 4
		}
	}
	return total
}

func promptFromRequest(req GenerateContentRequest) string {
	var lines []string
	for _, content := range req.Contents {
		role := content.Role
		if role == "" {
			role = "user"
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
		lines = append(lines, role+": "+strings.Join(parts, " "))
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

func (h *Handler) handleOllamaBackend(w http.ResponseWriter, r *http.Request, req GenerateContentRequest, version, model string, stream bool) {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(r.Context())
	upstream := rt.Profile.OllamaUpstream
	if upstream == "" {
		upstream = "http://localhost:11434"
	}
	ollamaModel := rt.Profile.BackendModel
	if ollamaModel == "" {
		ollamaModel = model
	}

	promptTokens := estimatePromptTokens(req)
	responseModel := runtimecfg.ResponseModelForRequest(r.Context(), model)
	messages := geminiToChatMessages(req)

	if stream {
		h.handleOllamaStream(w, r.Context(), upstream, messages, ollamaModel, responseModel, promptTokens)
		return
	}

	text, err := h.ollamaHTTP.NonStreaming(r.Context(), upstream, messages, ollamaModel)
	if err != nil {
		writeError(w, http.StatusBadGateway, "INTERNAL", "ollama backend error: "+err.Error())
		return
	}

	completionTokens := len(strings.Fields(text))
	resp := GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{Text: text}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: completionTokens,
			TotalTokenCount:      promptTokens + completionTokens,
		},
		ModelVersion: responseModel,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleOllamaStream(w http.ResponseWriter, ctx context.Context, upstream string, messages []ollama.ChatMessage, model, responseModel string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	completionTokens := 0
	err := h.ollamaHTTP.Streaming(ctx, upstream, messages, model, func(delta string) error {
		completionTokens++
		chunk := GenerateContentResponse{
			Candidates: []Candidate{{
				Content:      Content{Parts: []Part{{Text: delta}}, Role: "model"},
				FinishReason: "NONE",
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount: promptTokens,
			},
			ModelVersion: responseModel,
		}
		data, _ := json.Marshal(chunk)
		sse.WriteData(data)
		sse.Flush()
		return nil
	})

	if err != nil {
		errData, _ := json.Marshal(map[string]any{
			"error": map[string]any{"code": 502, "message": "ollama backend error: " + err.Error(), "status": "INTERNAL"},
		})
		sse.WriteData(errData)
		sse.Flush()
		return
	}

	finalChunk := GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{Text: ""}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: completionTokens,
			TotalTokenCount:      promptTokens + completionTokens,
		},
		ModelVersion: responseModel,
	}
	data, _ := json.Marshal(finalChunk)
	sse.WriteData(data)
	sse.Flush()
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

func labelsFromContext(ctx context.Context) map[string]string {
	if v := ctx.Value(router.LabelsKey{}); v != nil {
		if labels, ok := v.(map[string]string); ok {
			return labels
		}
	}
	return map[string]string{}
}
