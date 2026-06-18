package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// modelInfo mirrors the Anthropic `model` resource returned by GET /v1/models.
type modelInfo struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// modelList mirrors the paginated envelope the Anthropic SDK expects from the
// Models API.
type modelList struct {
	Data    []modelInfo `json:"data"`
	HasMore bool        `json:"has_more"`
	FirstID *string     `json:"first_id"`
	LastID  *string     `json:"last_id"`
}

// defaultModels is a small, plausible catalogue served when the profile does
// not pin a specific response model. Created timestamps are fixed so the
// listing is deterministic across calls.
var defaultModels = []modelInfo{
	{Type: "model", ID: "claude-3-5-sonnet-20241022", DisplayName: "Claude 3.5 Sonnet (New)", CreatedAt: "2024-10-22T00:00:00Z"},
	{Type: "model", ID: "claude-3-5-haiku-20241022", DisplayName: "Claude 3.5 Haiku", CreatedAt: "2024-10-22T00:00:00Z"},
	{Type: "model", ID: "claude-3-opus-20240229", DisplayName: "Claude 3 Opus", CreatedAt: "2024-02-29T00:00:00Z"},
	{Type: "model", ID: "claude-3-haiku-20240307", DisplayName: "Claude 3 Haiku", CreatedAt: "2024-03-07T00:00:00Z"},
}

// handleListModels serves GET /v1/models and GET /v1/models/{model} so SDK
// init paths and model pickers see a well-formed catalogue instead of a 404.
func (h *Handler) handleListModels(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}
	if r.Header.Get("x-api-key") == "" {
		writeUnauthorized(r.Context(), w)
		return
	}

	models := modelsForProfile(r.Context())

	if requested := chi.URLParam(r, "model"); requested != "" {
		for _, m := range models {
			if m.ID == requested {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(m)
				return
			}
		}
		writeErrorWithRequestID(w, http.StatusNotFound, "not_found_error", "model: "+requested, "req_zolem_not_found")
		return
	}

	resp := modelList{Data: models, HasMore: false}
	if len(models) > 0 {
		first := models[0].ID
		last := models[len(models)-1].ID
		resp.FirstID = &first
		resp.LastID = &last
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleCountTokens serves POST /v1/messages/count_tokens, returning the same
// deterministic token estimate the messages handler reports as input_tokens.
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
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

	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(r.Context(), w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(r.Context(), w, "model is required")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"input_tokens": estimateInputTokens(req)})
}

// modelsForProfile honours response_model_policy: when the profile forces a
// literal or backend model, that id is advertised first so model pickers select
// something the listener will actually return. Passing an empty request model to
// the shared resolver yields the pinned model ("" when requests are echoed).
func modelsForProfile(ctx context.Context) []modelInfo {
	models := append([]modelInfo(nil), defaultModels...)
	forced := runtimecfg.ResponseModelForRequest(ctx, "")
	if forced == "" {
		return models
	}
	for _, m := range models {
		if m.ID == forced {
			return models
		}
	}
	return append([]modelInfo{{Type: "model", ID: forced, DisplayName: forced, CreatedAt: "2024-10-22T00:00:00Z"}}, models...)
}
