package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// modelObject mirrors the Gemini `Model` resource returned by models.list and
// models.get.
type modelObject struct {
	Name                       string   `json:"name"`
	Version                    string   `json:"version"`
	DisplayName                string   `json:"displayName"`
	Description                string   `json:"description"`
	InputTokenLimit            int      `json:"inputTokenLimit"`
	OutputTokenLimit           int      `json:"outputTokenLimit"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

type modelList struct {
	Models        []modelObject `json:"models"`
	NextPageToken string        `json:"nextPageToken"`
}

func catalogModel(id, version, display, description string, outputLimit int) modelObject {
	return modelObject{
		Name:                       "models/" + id,
		Version:                    version,
		DisplayName:                display,
		Description:                description,
		InputTokenLimit:            1048576,
		OutputTokenLimit:           outputLimit,
		SupportedGenerationMethods: []string{"generateContent", "countTokens"},
	}
}

// defaultModels is a static, deterministic catalogue served when the profile
// does not pin a response model.
var defaultModels = []modelObject{
	catalogModel("gemini-2.5-pro", "2.5", "Gemini 2.5 Pro", "Stable release of Gemini 2.5 Pro.", 65536),
	catalogModel("gemini-2.5-flash", "2.5", "Gemini 2.5 Flash", "Stable release of Gemini 2.5 Flash.", 65536),
	catalogModel("gemini-2.0-flash", "2.0", "Gemini 2.0 Flash", "Gemini 2.0 Flash.", 8192),
	catalogModel("gemini-2.0-flash-lite", "2.0", "Gemini 2.0 Flash-Lite", "Gemini 2.0 Flash-Lite.", 8192),
}

// modelsForProfile honours response_model_policy: a pinned response model is
// part of the catalogue and listed first (moved, not duplicated, when it is
// already a catalogue entry).
func modelsForProfile(ctx context.Context) []modelObject {
	forced := strings.TrimPrefix(runtimecfg.ResponseModelForRequest(ctx, ""), "models/")
	if forced == "" {
		return append([]modelObject(nil), defaultModels...)
	}
	pinned := catalogModel(forced, "001", forced, "Model pinned by the listener profile.", 8192)
	models := []modelObject{pinned}
	for _, m := range defaultModels {
		if m.Name == pinned.Name {
			models[0] = m
			continue
		}
		models = append(models, m)
	}
	return models
}

// modelsPreflight applies the forced-error and API-key checks shared by the
// model endpoints, in the same order as generation. It reports whether the
// request may proceed.
func modelsPreflight(w http.ResponseWriter, r *http.Request) bool {
	if writeForcedProfileError(r.Context(), w) {
		return false
	}
	if r.Header.Get("x-goog-api-key") == "" && r.URL.Query().Get("key") == "" {
		writeForbidden(r.Context(), w)
		return false
	}
	return true
}

func (h *Handler) handleListModels(w http.ResponseWriter, r *http.Request) {
	if !modelsPreflight(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(modelList{Models: modelsForProfile(r.Context())})
}

func (h *Handler) handleGetModel(w http.ResponseWriter, r *http.Request) {
	if !modelsPreflight(w, r) {
		return
	}
	requested := strings.TrimPrefix(chi.URLParam(r, "*"), "models/")
	for _, m := range modelsForProfile(r.Context()) {
		if m.Name == "models/"+requested {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(m)
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "models/"+requested+" is not found for API version, or is not supported for the requested method.")
}
