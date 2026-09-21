package typesafe

import (
	"net/http"
)

// modelEntry and the /v1/models envelope below are INVENTED.
//
// The confirmed contract (docs.typesafe.ai, fetched 2026-09-21) does not
// mention a GET /v1/models endpoint at all — its existence and shape were not
// found on any fetched page. The official SDK source (npm @typesafe-ai/sdk,
// PyPI typesafe-sdk) and the Pydantic AI / Vercel AI SDK / LiteLLM TypeSafe
// integrations were not reachable from this environment to confirm one
// either. This handler and shape are invented, modeled on the common
// {"models": [{"id": ...}]} convention shared by OpenAI and Ollama's own
// /v1/models-compatible surfaces, purely so zolem has *something* reasonable
// to serve. See docs/typesafe.md for this provenance note.
type modelEntry struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

// defaultModels is the synthetic catalogue served by GET /v1/models.
var defaultModels = []modelEntry{
	{ID: "jev-latest", Object: "model"},
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
