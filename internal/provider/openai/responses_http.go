package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/provider/backend"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/zolemerr"
)

type responsesRequest struct {
	Model        string          `json:"model"`
	Instructions json.RawMessage `json:"instructions"`
	Input        json.RawMessage `json:"input"`
	Stream       bool            `json:"stream"`
}

type responsesInputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Output  json.RawMessage `json:"output"`
}

// handleResponsesPost serves the non-streaming HTTP form of POST /v1/responses.
func (h *Handler) handleResponsesPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if writeForcedProfileError(ctx, w) {
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeUnauthorized(ctx, w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(ctx, w, "failed to read request body")
		return
	}
	if err := h.validator.Validate("openai", "v1-responses", body); err != nil {
		writeInvalidRequest(ctx, w, err.Error())
		return
	}
	var req responsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(ctx, w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(ctx, w, "model is required")
		return
	}
	if req.Stream {
		writeInvalidRequest(ctx, w, "streaming is not supported yet")
		return
	}

	responseModel := runtimecfg.ResponseModelForRequest(ctx, req.Model)

	if runtimecfg.UsesFixtures(ctx) {
		matched, err := h.matcher.Match(ctx, fixture.MatchRequest{
			Provider: "openai", Version: "v1-responses",
			Labels: labelsFromContext(ctx),
			Body:   json.RawMessage(body),
		})
		if err != nil {
			writeFixtureSelectionError(w, err)
			return
		}
		if matched != nil {
			rendered, ok := renderFixtureBody(w, ctx, matched)
			if !ok {
				return
			}
			serveResponsesFixture(w, matched, rendered, responseModel)
			return
		}
	}

	input := responsesInputMessages(req.Input)
	messages := input
	if instructions := rawString(req.Instructions); instructions != "" {
		messages = append([]ollama.ChatMessage{{Role: "system", Content: instructions}}, input...)
	}

	cb := backend.Resolve(ctx, h.generator, h.ollamaHTTP, h.wasmGenerator)
	tokens, err := cb.Tokens(ctx, backend.GenerateRequest{
		Messages: messages,
		Model:    req.Model,
		FixtureMatch: &fixture.MatchRequest{
			Provider: "openai", Version: "v1-responses",
			Labels: labelsFromContext(ctx),
			Body:   json.RawMessage(body),
		},
	})
	if err != nil {
		writeBackendError(w, err)
		return
	}

	inputTokens := 0
	for _, m := range input {
		inputTokens += len(strings.Fields(m.Content)) + 4
	}
	outputTokens := response.CountNonEmpty(tokens)
	now := time.Now()
	resp := map[string]any{
		"id":         fmt.Sprintf("resp_zolem%d", now.UnixNano()),
		"object":     "response",
		"created_at": now.Unix(),
		"status":     "completed",
		"model":      responseModel,
		"output": []any{map[string]any{
			"type":   "message",
			"id":     fmt.Sprintf("msg_zolem%d", now.UnixNano()),
			"role":   "assistant",
			"status": "completed",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        strings.Join(tokens, ""),
				"annotations": []any{},
			}},
		}},
		"usage": map[string]any{
			"input_tokens":          inputTokens,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens":         outputTokens,
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
			"total_tokens":          inputTokens + outputTokens,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// serveResponsesFixture serves a v1-responses event-array fixture as a
// non-streaming response: the response object of the last response.completed
// event. A non-2xx fixture is served verbatim with its status.
func serveResponsesFixture(w http.ResponseWriter, f *fixture.Fixture, body []byte, model string) {
	if f.Status < 200 || f.Status > 299 {
		fixture.WriteVerbatim(w, f.Status, body, "model", model)
		return
	}
	var events []struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &events); err != nil {
		zolemerr.Write(w, fmt.Sprintf("fixture %q response must be a JSON array of Responses API events: %v", f.ID, err))
		return
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "response.completed" && len(events[i].Response) > 0 {
			fixture.WriteVerbatim(w, f.Status, events[i].Response, "model", model)
			return
		}
	}
	zolemerr.Write(w, fmt.Sprintf("fixture %q has no response.completed event", f.ID))
}

// responsesInputMessages converts a Responses API input (a string or an array
// of input items) into chat messages, dropping empty and unsupported items.
func responsesInputMessages(raw json.RawMessage) []ollama.ChatMessage {
	var messages []ollama.ChatMessage
	add := func(role, text string) {
		if text != "" {
			messages = append(messages, ollama.ChatMessage{Role: role, Content: text})
		}
	}
	if s := rawString(raw); s != "" {
		add("user", s)
		return messages
	}
	var items []responsesInputItem
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	for _, item := range items {
		switch {
		case item.Type == "function_call_output":
			add("tool", rawString(item.Output))
		case (item.Type == "" || item.Type == "message") && item.Role != "":
			add(item.Role, responsesContentText(item.Content))
		}
	}
	return messages
}

func responsesContentText(raw json.RawMessage) string {
	if s := rawString(raw); s != "" {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if (p.Type == "input_text" || p.Type == "output_text") && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}
