package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// emulatedVersion is the Ollama release zolem presents itself as. Clients probe
// /api/version for health and capability gating, so bump this deliberately.
const emulatedVersion = "0.32.11"

type modelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type modelEntry struct {
	Name         string       `json:"name"`
	Model        string       `json:"model"`
	ModifiedAt   string       `json:"modified_at"`
	Size         int64        `json:"size"`
	Digest       string       `json:"digest"`
	Details      modelDetails `json:"details"`
	Capabilities []string     `json:"capabilities"`
}

// defaultModels is a small synthetic catalogue served when the profile does not
// pin a response model. Timestamps and digests are fixed so listings are
// deterministic across calls, mirroring the openai provider's catalogue.
//
// AGENTS.md ("Ollama Models") excludes Chinese-origin LLMs from this
// repository. These three are Meta, Mistral AI, and Google respectively;
// nothing is ever downloaded, but keep any future entry consistent with that
// principle.
var defaultModels = []modelEntry{
	{
		Name: "llama3.2:latest", Model: "llama3.2:latest",
		ModifiedAt: "2026-01-15T10:00:00Z",
		Size:       2019393189,
		Digest:     "a80c4f17acd55265feec403c7aef86be0c25983ab279d83f3bcd3abbcb5b8b72",
		Details: modelDetails{
			Format: "gguf", Family: "llama", Families: []string{"llama"},
			ParameterSize: "3.2B", QuantizationLevel: "Q4_K_M",
		},
		Capabilities: []string{"completion", "tools"},
	},
	{
		Name: "mistral:latest", Model: "mistral:latest",
		ModifiedAt: "2026-01-15T10:00:00Z",
		Size:       4113301824,
		Digest:     "f974a74358d62a017b37c6f424fcdf2744ca02b09a5b1a4b0f36ca39aca0b0b2",
		Details: modelDetails{
			Format: "gguf", Family: "llama", Families: []string{"llama"},
			ParameterSize: "7.2B", QuantizationLevel: "Q4_0",
		},
		Capabilities: []string{"completion", "tools"},
	},
	{
		Name: "gemma3:4b", Model: "gemma3:4b",
		ModifiedAt: "2026-01-15T10:00:00Z",
		Size:       3338801804,
		Digest:     "c0494fe00251a2c2b7d0f0e8bf1a2f1c1d4e6a4b8f9c3d2e1a0b7c6d5e4f3a2b",
		Details: modelDetails{
			Format: "gguf", Family: "gemma3", Families: []string{"gemma3"},
			ParameterSize: "4.3B", QuantizationLevel: "Q4_K_M",
		},
		Capabilities: []string{"completion", "tools", "vision"},
	},
}

// normalizeModel applies Ollama's implicit :latest tag so "llama3.2" and
// "llama3.2:latest" compare equal.
func normalizeModel(name string) string {
	if name == "" || strings.Contains(name, ":") {
		return name
	}
	return name + ":latest"
}

// modelsForProfile returns the catalogue, with the profile's pinned response
// model substituted in when one is configured.
func modelsForProfile(ctx context.Context) []modelEntry {
	pinned := normalizeModel(runtimecfg.ResponseModelForRequest(ctx, ""))
	if pinned == "" {
		return defaultModels
	}
	for _, m := range defaultModels {
		if m.Name == pinned {
			return defaultModels
		}
	}
	// Prepend rather than replace: the pinned model is an addition to the
	// catalogue, not a substitution. Replacing defaultModels[0] would make
	// llama3.2:latest vanish from /api/tags and 404 on /api/show even though
	// /api/chat still accepts it.
	pinnedEntry := modelEntry{
		Name: pinned, Model: pinned,
		ModifiedAt:   defaultModels[0].ModifiedAt,
		Size:         defaultModels[0].Size,
		Digest:       defaultModels[0].Digest,
		Details:      defaultModels[0].Details,
		Capabilities: defaultModels[0].Capabilities,
	}
	return append([]modelEntry{pinnedEntry}, defaultModels...)
}

func (h *Handler) handleTags(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}
	writeJSON(w, map[string]any{"models": modelsForProfile(r.Context())})
}

func (h *Handler) handleVersion(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}
	writeJSON(w, map[string]string{"version": emulatedVersion})
}

// handlePS reports loaded models. Zolem loads nothing, so the honest answer is
// an empty list — a state a real Ollama genuinely reports.
func (h *Handler) handlePS(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}
	writeJSON(w, map[string]any{"models": []modelEntry{}})
}

// handleShow reports model metadata and capabilities. Unlike /api/chat, this
// endpoint 404s on an unknown model: its entire purpose is reporting what
// exists.
func (h *Handler) handleShow(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInvalidRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(w, "model is required")
		return
	}

	want := normalizeModel(req.Model)
	for _, m := range modelsForProfile(r.Context()) {
		if m.Name == want {
			writeJSON(w, map[string]any{
				"modified_at":  m.ModifiedAt,
				"details":      m.Details,
				"capabilities": m.Capabilities,
				"template":     "{{ .Prompt }}",
				"model_info": map[string]any{
					"general.architecture":    m.Details.Family,
					"general.parameter_count": m.Size,
				},
			})
			return
		}
	}
	writeNotFound(w, "model '"+req.Model+"' not found")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
