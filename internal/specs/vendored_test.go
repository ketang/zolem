package specs_test

import (
	"testing"

	"zolem.dev/zolem/internal/specs"
)

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

	invalid := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"system","content":"hello"}]}`)
	if err := validator.Validate("anthropic", "v1", invalid); err == nil {
		t.Fatal("expected invalid anthropic request to fail validation")
	}
}
