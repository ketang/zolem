// internal/specs/validator_test.go
package specs_test

import (
	"testing"

	"zolem.dev/zolem/internal/specs"
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
