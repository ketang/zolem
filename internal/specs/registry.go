package specs

import "sort"

type SourceKind string

const (
	SourceKindOpenAPI              SourceKind = "openapi"
	SourceKindDiscovery            SourceKind = "discovery"
	SourceKindVendoredDocsSnapshot SourceKind = "vendored_docs_snapshot"
)

type ContractSource struct {
	Provider     string
	Version      string
	Kind         SourceKind
	RemoteURL    string
	FallbackPath string
	ContentType  string
	Enabled      bool
}

func (s ContractSource) Key() string {
	return s.Provider + ":" + s.Version
}

func (s ContractSource) HasRemote() bool {
	return s.RemoteURL != ""
}

type Registry struct {
	sources map[string]ContractSource
}

func DefaultRegistry() Registry {
	sources := []ContractSource{
		{
			Provider:     "anthropic",
			Version:      "v1",
			Kind:         SourceKindVendoredDocsSnapshot,
			FallbackPath: "fallbacks/anthropic-v1.json",
			ContentType:  "application/schema+json",
			Enabled:      true,
		},
		{
			Provider:     "openai",
			Version:      "v1",
			Kind:         SourceKindOpenAPI,
			RemoteURL:    "https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml",
			FallbackPath: "fallbacks/openai-v1.json",
			ContentType:  "application/yaml",
			Enabled:      true,
		},
		{
			Provider:     "openrouter",
			Version:      "v1",
			Kind:         SourceKindOpenAPI,
			RemoteURL:    "https://openrouter.ai/openapi.yaml",
			FallbackPath: "fallbacks/openrouter-v1.json",
			ContentType:  "application/yaml",
			Enabled:      true,
		},
		{
			Provider:     "gemini",
			Version:      "v1",
			Kind:         SourceKindDiscovery,
			RemoteURL:    "https://generativelanguage.googleapis.com/$discovery/rest?version=v1",
			FallbackPath: "fallbacks/gemini-v1.json",
			ContentType:  "application/json",
			Enabled:      true,
		},
		{
			Provider:     "gemini",
			Version:      "v1beta",
			Kind:         SourceKindDiscovery,
			RemoteURL:    "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta",
			FallbackPath: "fallbacks/gemini-v1beta.json",
			ContentType:  "application/json",
			Enabled:      true,
		},
	}

	registry := Registry{sources: make(map[string]ContractSource, len(sources))}
	for _, source := range sources {
		registry.sources[source.Key()] = source
	}
	return registry
}

func (r Registry) Lookup(provider, version string) (ContractSource, bool) {
	source, ok := r.sources[provider+":"+version]
	return source, ok
}

func (r Registry) List() []ContractSource {
	keys := make([]string, 0, len(r.sources))
	for key, source := range r.sources {
		if source.Enabled {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	sources := make([]ContractSource, 0, len(keys))
	for _, key := range keys {
		sources = append(sources, r.sources[key])
	}
	return sources
}
