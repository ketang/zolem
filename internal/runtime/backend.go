package runtimecfg

import "context"

const (
	BackendHybrid  = "hybrid"
	BackendLorem   = "lorem"
	BackendFaker   = "faker"
	BackendFixture = "fixture"
	BackendOllama  = "ollama"
	BackendError   = "error"
	BackendWASM    = "wasm"
)

// BackendForRequest returns the explicit local-runtime backend when present,
// otherwise "hybrid" for the legacy static path that still does fixture-first
// fallback behavior.
func BackendForRequest(ctx context.Context) string {
	if rt, ok := ListenerRuntimeFromContext(ctx); ok && rt.Profile.Backend != "" {
		return rt.Profile.Backend
	}
	return BackendHybrid
}

func UsesFixtures(ctx context.Context) bool {
	switch BackendForRequest(ctx) {
	case BackendFixture, BackendHybrid:
		return true
	default:
		return false
	}
}
