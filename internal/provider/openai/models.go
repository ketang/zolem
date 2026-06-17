package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

// modelObject mirrors the OpenAI `model` resource returned by GET /v1/models.
type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type modelList struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

// defaultModels is a small, plausible catalogue served when the profile does
// not pin a specific response model. Created timestamps are fixed so the
// listing is deterministic across calls.
var defaultModels = []modelObject{
	{ID: "gpt-4o", Object: "model", Created: 1715367049, OwnedBy: "openai"},
	{ID: "gpt-4o-mini", Object: "model", Created: 1721172741, OwnedBy: "openai"},
	{ID: "gpt-4-turbo", Object: "model", Created: 1712361441, OwnedBy: "openai"},
	{ID: "gpt-3.5-turbo", Object: "model", Created: 1677610602, OwnedBy: "openai"},
}

// handleListModels serves GET /v1/models and GET /v1/models/{model} so SDK
// init paths and model pickers see a well-formed catalogue instead of a 404.
func (h *Handler) handleListModels(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		writeUnauthorized(r.Context(), w)
		return
	}

	models := modelsForProfile(r.Context())

	if requested := strings.TrimPrefix(r.URL.Path, "/v1/models/"); requested != "" && requested != r.URL.Path {
		for _, m := range models {
			if m.ID == requested {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(m)
				return
			}
		}
		writeError(w, http.StatusNotFound, "invalid_request_error", "The model '"+requested+"' does not exist.", nil)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(modelList{Object: "list", Data: models})
}

// modelsForProfile honours response_model_policy: when the profile forces a
// literal or backend model, that id is advertised first so model pickers select
// something the listener will actually return. Passing an empty request model to
// the shared resolver yields the pinned model ("" when requests are echoed).
func modelsForProfile(ctx context.Context) []modelObject {
	models := append([]modelObject(nil), defaultModels...)
	forced := runtimecfg.ResponseModelForRequest(ctx, "")
	if forced == "" {
		return models
	}
	for _, m := range models {
		if m.ID == forced {
			return models
		}
	}
	created := int64(1715367049)
	return append([]modelObject{{ID: forced, Object: "model", Created: created, OwnedBy: "openai"}}, models...)
}
