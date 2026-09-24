package typesafe

import (
	"net/http"
)

// modelEntry follows the model-card shape in TypeSafe's official JS SDK and
// models reference: name, description, and release_date. The catalogue data
// below is synthetic; the alias is stable while the real model behind it may
// change. See docs/typesafe.md for provenance.
type modelEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"`
}

// defaultModels is the synthetic catalogue served by GET /v1/models.
var defaultModels = []modelEntry{
	{Name: "jev-latest", Description: "Synthetic Jev latest model alias", ReleaseDate: "1970-01-01"},
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}
	if !requireAuth(w, r) {
		return
	}
	writeJSON(w, map[string]any{"models": defaultModels})
}
