package runtimecfg

import "context"

const (
	BackendHybrid  = "hybrid"
	BackendLorem   = "lorem"
	BackendFaker   = "faker"
	BackendFixture = "fixture"
	BackendOllama  = "ollama"
	// BackendOllamaLogprob answers typed TypeSafe questions from a local
	// Ollama model's next-token log probabilities. Valid only for the typesafe
	// provider; the listener builder rejects it elsewhere.
	BackendOllamaLogprob = "ollama-logprob"
	BackendError         = "error"
	BackendWASM          = "wasm"
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
