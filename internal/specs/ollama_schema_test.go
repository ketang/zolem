package specs_test

import (
	"testing"

	"github.com/ketang/zolem/internal/specs"
)

// TestVendoredFallbacks_OllamaSnapshotIsPermissive pins the permissiveness
// requirement for the Ollama request schema. Ollama's native API is
// unversioned and gains fields regularly, and zolem's schemas are
// representative subsets focused on request structure. A client sending any
// documented optional field — or a field added upstream after this snapshot
// was written — must not receive a spurious 400.
func TestVendoredFallbacks_OllamaSnapshotIsPermissive(t *testing.T) {
	fallbacks := specs.VendoredFallbacks()
	data, ok := fallbacks["ollama:v1"]
	if !ok {
		t.Fatal("missing ollama vendored snapshot")
	}

	validator := specs.NewValidator()
	if err := specs.LoadProviderSchema(validator, "ollama", "v1", data); err != nil {
		t.Fatalf("load ollama vendored snapshot: %v", err)
	}

	accepted := []struct {
		name string
		body string
	}{
		{"minimal", `{"model":"llama3.2","messages":[{"role":"user","content":"hi"}]}`},
		{"stream absent", `{"model":"llama3.2","messages":[]}`},
		{"stream present", `{"model":"llama3.2","messages":[],"stream":false}`},
		{"options and keep_alive", `{"model":"llama3.2","messages":[],"options":{"num_ctx":4096,"seed":1,"temperature":0},"keep_alive":"10m"}`},
		{"keep_alive as number", `{"model":"llama3.2","messages":[],"keep_alive":300}`},
		{"format as string", `{"model":"llama3.2","messages":[],"format":"json"}`},
		{"format as schema", `{"model":"llama3.2","messages":[],"format":{"type":"object","properties":{"age":{"type":"integer"}}}}`},
		{"think as bool", `{"model":"llama3.2","messages":[],"think":true}`},
		{"think as level", `{"model":"llama3.2","messages":[],"think":"high"}`},
		{"tools", `{"model":"llama3.2","messages":[],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]}`},
		{"truncate and shift", `{"model":"llama3.2","messages":[],"truncate":true,"shift":false}`},
		// Message-level fields are deliberately unconstrained.
		{"message images", `{"model":"llama3.2","messages":[{"role":"user","content":"describe","images":["aGVsbG8="]}]}`},
		{"message tool result", `{"model":"llama3.2","messages":[{"role":"tool","content":"11c","tool_name":"get_weather"}]}`},
		// Fields added upstream after this snapshot must not 400.
		{"unknown future field", `{"model":"llama3.2","messages":[],"logprobs":true,"top_logprobs":5,"invented_later":{"a":1}}`},
	}

	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if err := validator.Validate("ollama", "v1", []byte(tc.body)); err != nil {
				t.Fatalf("expected %s to be accepted, got %v", tc.name, err)
			}
		})
	}

	rejected := []struct {
		name string
		body string
	}{
		{"missing model", `{"messages":[{"role":"user","content":"hi"}]}`},
		{"missing messages", `{"model":"llama3.2"}`},
		{"model wrong type", `{"model":123,"messages":[]}`},
		{"messages wrong type", `{"model":"llama3.2","messages":"hi"}`},
	}

	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if err := validator.Validate("ollama", "v1", []byte(tc.body)); err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
		})
	}
}
