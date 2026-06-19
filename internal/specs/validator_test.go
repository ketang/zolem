// internal/specs/validator_test.go
package specs_test

import (
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/specs"
)

const minimalSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model"],
  "properties": {
    "model": {"type": "string"},
    "stream": {"type": "boolean"}
  },
  "additionalProperties": true
}`

func TestValidator_Valid(t *testing.T) {
	v := specs.NewValidator()
	v.LoadRaw("test", "v1", []byte(minimalSchema))

	err := v.Validate("test", "v1", []byte(`{"model":"test-model"}`))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidator_MissingRequired(t *testing.T) {
	v := specs.NewValidator()
	v.LoadRaw("test", "v1", []byte(minimalSchema))

	err := v.Validate("test", "v1", []byte(`{"stream":true}`))
	if err == nil {
		t.Error("expected validation error for missing model")
	}
}

func TestValidator_UnknownProviderVersion(t *testing.T) {
	v := specs.NewValidator()
	err := v.Validate("unknown", "v99", []byte(`{}`))
	if err != nil {
		t.Errorf("unknown provider should not error, got: %v", err)
	}
}

func TestValidator_ErrorDoesNotLeakMemURI(t *testing.T) {
	v := specs.NewValidator()
	v.LoadRaw("test", "v1", []byte(minimalSchema))

	err := v.Validate("test", "v1", []byte(`{"stream":true}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
	msg := err.Error()
	if strings.Contains(msg, "mem://") {
		t.Fatalf("validation error leaks mem:// URI: %q", msg)
	}
	if strings.Contains(msg, "jsonschema validation failed with") {
		t.Fatalf("validation error leaks jsonschema internal message: %q", msg)
	}
}
