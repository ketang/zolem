package specs

import _ "embed"

var (
	//go:embed vendored/anthropic-v1.json
	anthropicV1Snapshot []byte
)

// VendoredFallbacks returns bundled provider schemas used when no canonical
// machine-readable remote source is available or reachable.
func VendoredFallbacks() map[string][]byte {
	return map[string][]byte{
		"anthropic:v1": append([]byte(nil), anthropicV1Snapshot...),
	}
}
