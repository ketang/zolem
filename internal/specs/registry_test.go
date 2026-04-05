package specs_test

import (
	"testing"

	"zolem.dev/zolem/internal/specs"
)

func TestDefaultRegistryLookup(t *testing.T) {
	registry := specs.DefaultRegistry()

	tests := []struct {
		provider string
		version  string
		kind     specs.SourceKind
		remote   bool
	}{
		{provider: "anthropic", version: "v1", kind: specs.SourceKindVendoredDocsSnapshot, remote: false},
		{provider: "openai", version: "v1", kind: specs.SourceKindOpenAPI, remote: true},
		{provider: "openrouter", version: "v1", kind: specs.SourceKindOpenAPI, remote: true},
		{provider: "gemini", version: "v1", kind: specs.SourceKindDiscovery, remote: true},
		{provider: "gemini", version: "v1beta", kind: specs.SourceKindDiscovery, remote: true},
	}

	for _, tt := range tests {
		source, ok := registry.Lookup(tt.provider, tt.version)
		if !ok {
			t.Fatalf("missing registry entry for %s:%s", tt.provider, tt.version)
		}
		if source.Kind != tt.kind {
			t.Fatalf("%s:%s kind: got %q, want %q", tt.provider, tt.version, source.Kind, tt.kind)
		}
		if source.HasRemote() != tt.remote {
			t.Fatalf("%s:%s remote: got %v, want %v", tt.provider, tt.version, source.HasRemote(), tt.remote)
		}
		if source.FallbackPath == "" {
			t.Fatalf("%s:%s missing fallback path", tt.provider, tt.version)
		}
	}
}

func TestDefaultRegistryListIsSortedAndEnabled(t *testing.T) {
	registry := specs.DefaultRegistry()
	sources := registry.List()

	if len(sources) != 5 {
		t.Fatalf("got %d sources, want 5", len(sources))
	}

	got := make([]string, 0, len(sources))
	for _, source := range sources {
		if !source.Enabled {
			t.Fatalf("unexpected disabled source in list: %s", source.Key())
		}
		got = append(got, source.Key())
	}

	want := []string{
		"anthropic:v1",
		"gemini:v1",
		"gemini:v1beta",
		"openai:v1",
		"openrouter:v1",
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted key %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
