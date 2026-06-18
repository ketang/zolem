package specs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/specs"
)

func TestNormalizeGeminiDiscovery_ValidatesGenerateContentRequests(t *testing.T) {
	data := readDiscoveryFixture(t, "gemini-discovery-v1.json")

	normalized, err := specs.NormalizeGeminiDiscovery("v1", data)
	if err != nil {
		t.Fatalf("normalize discovery: %v", err)
	}

	validator := specs.NewValidator()
	if err := validator.LoadRaw("gemini", "v1", normalized); err != nil {
		t.Fatalf("load normalized schema: %v", err)
	}

	valid := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":4}}`)
	if err := validator.Validate("gemini", "v1", valid); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}

	invalid := []byte(`{"contents":[{"role":"user","parts":[{}]}]}`)
	if err := validator.Validate("gemini", "v1", invalid); err == nil {
		t.Fatal("expected invalid request to fail validation")
	}
}

func TestLoadProviderSchema_GeminiDiscoveryVersions(t *testing.T) {
	validator := specs.NewValidator()

	for _, tc := range []struct {
		version string
		fixture string
	}{
		{version: "v1", fixture: "gemini-discovery-v1.json"},
		{version: "v1beta", fixture: "gemini-discovery-v1beta.json"},
	} {
		data := readDiscoveryFixture(t, tc.fixture)
		if err := specs.LoadProviderSchema(validator, "gemini", tc.version, data); err != nil {
			t.Fatalf("load provider schema %s: %v", tc.version, err)
		}

		err := validator.Validate("gemini", tc.version, []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
		if err != nil {
			t.Fatalf("validate %s: %v", tc.version, err)
		}
	}
}

func TestNormalizeGeminiDiscovery_MissingMethod(t *testing.T) {
	data := []byte(`{
	  "resources": {
	    "models": {
	      "methods": {
	        "generateContent": {
	          "id": "models.generateContent",
	          "request": {"$ref": "GenerateContentRequest"}
	        }
	      }
	    }
	  },
	  "schemas": {
	    "GenerateContentRequest": {
	      "type": "object",
	      "properties": {
	        "contents": {"type": "array", "items": {"type": "string"}}
	      }
	    }
	  }
	}`)

	_, err := specs.NormalizeGeminiDiscovery("v1", data)
	if err == nil {
		t.Fatal("expected missing method error")
	}
	if !strings.Contains(err.Error(), `method "models.streamGenerateContent" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func readDiscoveryFixture(t *testing.T, name string) []byte {
	t.Helper()

	path := filepath.Join("..", "..", "testdata", "specs", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}
