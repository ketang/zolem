package specs

import "testing"

func TestVendoredFallbacks_OpenAIResponsesSchemaLoads(t *testing.T) {
	data, ok := VendoredFallbacks()["openai:v1-responses"]
	if !ok {
		t.Fatal("openai:v1-responses missing from VendoredFallbacks")
	}
	validator := NewValidator()
	if err := LoadProviderSchema(validator, "openai", "v1-responses", data); err != nil {
		t.Fatalf("LoadProviderSchema: %v", err)
	}
	if !validator.Has("openai", "v1-responses") {
		t.Fatal("validator.Has(openai, v1-responses) = false")
	}
	for name, body := range map[string]string{
		"string input": `{"model":"gpt-4o","input":"hi"}`,
		"array input":  `{"model":"gpt-4o","input":[{"role":"user","content":"hi"}],"extra":1}`,
	} {
		if err := validator.Validate("openai", "v1-responses", []byte(body)); err != nil {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
	}
	for name, body := range map[string]string{
		"missing model": `{"input":"hi"}`,
		"bad input":     `{"model":"gpt-4o","input":5}`,
	} {
		if err := validator.Validate("openai", "v1-responses", []byte(body)); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}
