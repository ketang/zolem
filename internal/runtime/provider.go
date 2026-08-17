package runtimecfg

import "slices"

// Providers lists every API surface zolem can impersonate on a listener.
//
// This is the single source of truth. It previously lived as the literal
// string "anthropic, openai, or gemini" duplicated across the listener
// validator and the fixed-listener flag parser, which let the two drift.
var Providers = []string{
	ProviderAnthropic,
	ProviderGemini,
	ProviderOllama,
	ProviderOpenAI,
}

const (
	ProviderAnthropic = "anthropic"
	ProviderGemini    = "gemini"
	ProviderOllama    = "ollama"
	ProviderOpenAI    = "openai"
)

// ProviderList is the human-readable form used in error messages and CLI help.
const ProviderList = "anthropic, gemini, ollama, or openai"

// ValidProvider reports whether name is a provider surface zolem serves.
func ValidProvider(name string) bool {
	return slices.Contains(Providers, name)
}
