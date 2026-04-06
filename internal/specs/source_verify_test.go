package specs_test

import (
	"encoding/json"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/specs"
)

func TestSourceVerification_AnthropicV1SnapshotInvariants(t *testing.T) {
	data, ok := specs.VendoredFallbacks()["anthropic:v1"]
	if !ok {
		t.Fatal("missing anthropic:v1 vendored snapshot")
	}

	var doc struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
		Defs       struct {
			Message struct {
				Required   []string `json:"required"`
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"message"`
			MessageContentBlock struct {
				Required   []string `json:"required"`
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"message_content_block"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal anthropic snapshot: %v", err)
	}

	assertContainsAll(t, doc.Required, "model", "max_tokens", "messages")
	assertContainsAll(t, keys(doc.Properties), "model", "max_tokens", "messages", "system", "stream")
	assertContainsAll(t, doc.Defs.Message.Required, "role", "content")
	assertContainsAll(t, doc.Defs.Message.Properties["role"].Enum, "user", "assistant")
	assertContainsAll(t, doc.Defs.MessageContentBlock.Required, "type", "text")
	assertContainsAll(t, doc.Defs.MessageContentBlock.Properties["type"].Enum, "text")

	validator := specs.NewValidator()
	if err := specs.LoadProviderSchema(validator, "anthropic", "v1", data); err != nil {
		t.Fatalf("load anthropic snapshot: %v", err)
	}

	valid := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"system":"be precise","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	if err := validator.Validate("anthropic", "v1", valid); err != nil {
		t.Fatalf("valid anthropic request rejected: %v", err)
	}

	validSDKShape := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	if err := validator.Validate("anthropic", "v1", validSDKShape); err != nil {
		t.Fatalf("valid anthropic sdk-shaped request rejected: %v", err)
	}

	drifted := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"tool","content":"hello"}]}`)
	if err := validator.Validate("anthropic", "v1", drifted); err == nil {
		t.Fatal("expected anthropic drifted role to fail validation")
	}
}

func TestSourceVerification_GeminiDiscoveryInvariants(t *testing.T) {
	for _, tc := range []struct {
		version string
		fixture string
	}{
		{version: "v1", fixture: "gemini-discovery-v1.json"},
		{version: "v1beta", fixture: "gemini-discovery-v1beta.json"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			data := readDiscoveryFixture(t, tc.fixture)

			normalized, err := specs.NormalizeGeminiDiscovery(tc.version, data)
			if err != nil {
				t.Fatalf("normalize discovery: %v", err)
			}

			var schema struct {
				Ref  string `json:"$ref"`
				Defs map[string]struct {
					Required   []string                   `json:"required"`
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"$defs"`
			}
			if err := json.Unmarshal(normalized, &schema); err != nil {
				t.Fatalf("unmarshal normalized schema: %v", err)
			}

			if schema.Ref != "#/$defs/GenerateContentRequest" {
				t.Fatalf("unexpected root ref: %q", schema.Ref)
			}
			root, ok := schema.Defs["GenerateContentRequest"]
			if !ok {
				t.Fatal("missing GenerateContentRequest definition")
			}
			assertContainsAll(t, root.Required, "contents")
			assertContainsAll(t, keys(root.Properties), "contents", "generationConfig")

			validator := specs.NewValidator()
			if err := validator.LoadRaw("gemini", tc.version, normalized); err != nil {
				t.Fatalf("load normalized schema: %v", err)
			}

			valid := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":8}}`)
			if err := validator.Validate("gemini", tc.version, valid); err != nil {
				t.Fatalf("valid gemini request rejected: %v", err)
			}

			drifted := []byte(`{"contents":[{"role":"user","parts":[{}]}],"generationConfig":{"maxOutputTokens":8}}`)
			if err := validator.Validate("gemini", tc.version, drifted); err == nil {
				t.Fatal("expected drifted gemini request to fail validation")
			}
		})
	}
}

func TestSourceVerification_GeminiDiscoveryDrift_MissingMethod(t *testing.T) {
	data := readDiscoveryFixture(t, "gemini-discovery-v1.json")
	drifted := strings.Replace(string(data), `"streamGenerateContent": {`, `"streamGenerateContent_removed": {`, 1)
	drifted = strings.Replace(drifted, `"id": "models.streamGenerateContent"`, `"id": "models.streamGenerateContent_removed"`, 1)

	_, err := specs.NormalizeGeminiDiscovery("v1", []byte(drifted))
	if err == nil {
		t.Fatal("expected missing method drift to fail")
	}
	if !strings.Contains(err.Error(), `method "models.streamGenerateContent" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSourceVerification_GeminiDiscoveryDrift_RequestRefMismatch(t *testing.T) {
	data := readDiscoveryFixture(t, "gemini-discovery-v1.json")
	drifted := strings.Replace(string(data), `"GenerateContentRequest"`, `"StreamGenerateContentRequest"`, 1)

	_, err := specs.NormalizeGeminiDiscovery("v1", []byte(drifted))
	if err == nil {
		t.Fatal("expected request ref mismatch drift to fail")
	}
	if !strings.Contains(err.Error(), "uses request schema") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func keys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func assertContainsAll[T comparable](t *testing.T, got []T, want ...T) {
	t.Helper()

	have := make(map[T]struct{}, len(got))
	for _, item := range got {
		have[item] = struct{}{}
	}
	for _, item := range want {
		if _, ok := have[item]; !ok {
			t.Fatalf("missing %v in %v", item, got)
		}
	}
}
