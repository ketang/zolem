package specs

import _ "embed"

// Vendored, offline-first source documents for every provider schema zolem
// validates. They ship in the binary so request validation works with no
// network egress. Each blob is in its provider's canonical source form and is
// normalized into JSON Schema at load time by LoadProviderSchema:
//
//   - anthropic:v1 is a pre-normalized JSON Schema snapshot (no machine-readable
//     upstream source exists; it is loaded as-is).
//   - openai:v1 is an OpenAPI document, normalized by the OpenAPI normalizer.
//   - gemini:v1 / gemini:v1beta are Google API Discovery documents, normalized
//     by the discovery normalizer.
var (
	//go:embed vendored/anthropic-v1.json
	anthropicV1Snapshot []byte

	//go:embed vendored/openai-v1.openapi.yaml
	openaiV1Source []byte

	//go:embed vendored/gemini-v1.discovery.json
	geminiV1Source []byte

	//go:embed vendored/gemini-v1beta.discovery.json
	geminiV1betaSource []byte
)

// VendoredFallbacks returns the bundled provider source documents keyed by
// "provider:version". They are used when no canonical machine-readable remote
// source is configured or reachable, which—given zolem's no-egress posture—is
// always: production startup passes no remote sources, so these vendored
// snapshots are the sole schema source. Callers pass each blob through
// LoadProviderSchema, which normalizes it for the provider.
func VendoredFallbacks() map[string][]byte {
	return map[string][]byte{
		"anthropic:v1":  append([]byte(nil), anthropicV1Snapshot...),
		"openai:v1":     append([]byte(nil), openaiV1Source...),
		"gemini:v1":     append([]byte(nil), geminiV1Source...),
		"gemini:v1beta": append([]byte(nil), geminiV1betaSource...),
	}
}
