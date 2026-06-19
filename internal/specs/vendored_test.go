package specs_test

import (
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/specs"
)

// TestVendoredFallbacks_CoverAllServedProviders guards the core wiring fix: every
// provider/version a local listener validates against must have a vendored
// snapshot that normalizes into a working schema, accepts a representative
// valid request, and rejects a schema violation.
func TestVendoredFallbacks_CoverAllServedProviders(t *testing.T) {
	fallbacks := specs.VendoredFallbacks()
	cases := []struct {
		key     string
		valid   string
		invalid string
	}{
		{
			key:     "anthropic:v1",
			valid:   `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`,
			invalid: `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"system","content":"hi"}]}`,
		},
		{
			key:     "openai:v1",
			valid:   `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
			invalid: `{"model":"gpt-4o","messages":[{"role":"user"}]}`,
		},
		{
			key:     "gemini:v1",
			valid:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			invalid: `{"contents":[{"role":"user","parts":[{}]}]}`,
		},
		{
			key:     "gemini:v1beta",
			valid:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			invalid: `{"contents":[{"role":"user","parts":[{}]}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			data, ok := fallbacks[tc.key]
			if !ok {
				t.Fatalf("missing vendored snapshot for %s", tc.key)
			}
			provider, version, _ := strings.Cut(tc.key, ":")

			validator := specs.NewValidator()
			if err := specs.LoadProviderSchema(validator, provider, version, data); err != nil {
				t.Fatalf("load %s snapshot: %v", tc.key, err)
			}
			if !validator.Has(provider, version) {
				t.Fatalf("validator missing schema for %s after load", tc.key)
			}
			if err := validator.Validate(provider, version, []byte(tc.valid)); err != nil {
				t.Fatalf("valid %s request rejected: %v", tc.key, err)
			}
			if err := validator.Validate(provider, version, []byte(tc.invalid)); err == nil {
				t.Fatalf("invalid %s request accepted, want rejection", tc.key)
			}
		})
	}
}

func TestVendoredFallbacks_AnthropicSnapshotValidatesMessagesRequests(t *testing.T) {
	fallbacks := specs.VendoredFallbacks()
	data, ok := fallbacks["anthropic:v1"]
	if !ok {
		t.Fatal("missing anthropic vendored snapshot")
	}

	validator := specs.NewValidator()
	if err := specs.LoadProviderSchema(validator, "anthropic", "v1", data); err != nil {
		t.Fatalf("load anthropic vendored snapshot: %v", err)
	}

	valid := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
	if err := validator.Validate("anthropic", "v1", valid); err != nil {
		t.Fatalf("expected valid anthropic request, got %v", err)
	}

	validSDKShape := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	if err := validator.Validate("anthropic", "v1", validSDKShape); err != nil {
		t.Fatalf("expected valid anthropic sdk-shaped request, got %v", err)
	}

	validSystemBlocks := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"system":[{"type":"text","text":"be precise","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}]}`)
	if err := validator.Validate("anthropic", "v1", validSystemBlocks); err != nil {
		t.Fatalf("expected valid anthropic system-block request, got %v", err)
	}

	invalid := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"system","content":"hello"}]}`)
	if err := validator.Validate("anthropic", "v1", invalid); err == nil {
		t.Fatal("expected invalid anthropic request to fail validation")
	}
}
