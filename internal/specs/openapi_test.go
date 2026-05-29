package specs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const testOpenAPIChatCompletionsSpecYAML = `
openapi: 3.1.0
info:
  title: Chat API
  version: v1
paths:
  /v1/chat/completions:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required:
                - model
                - messages
              properties:
                model:
                  type: string
                messages:
                  type: array
                  items:
                    $ref: '#/components/schemas/ChatMessage'
                stream:
                  type: boolean
              additionalProperties: false
      responses:
        '200':
          description: ok
components:
  schemas:
    ChatMessage:
      type: object
      required:
        - role
        - content
      properties:
        role:
          type: string
        content:
          type: string
      additionalProperties: false
`

const testOpenAPINoChatCompletionsSpec = `
openapi: 3.1.0
info:
  title: Other API
  version: v1
paths:
  /v1/other:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
      responses:
        '200':
          description: ok
`

const testOpenAPIBrokenSchemaSpec = `
openapi: 3.1.0
info:
  title: Broken API
  version: v1
paths:
  /v1/chat/completions:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                messages:
                  type: array
                  items:
                    $ref: '#/components/schemas/MissingMessage'
      responses:
        '200':
          description: ok
`

func TestOpenAPINormalizer_NormalizeOpenAI(t *testing.T) {
	normalizer := OpenAPINormalizer{}

	schema, err := normalizer.Normalize(ContractSource{Provider: "openai", Version: "v1", Kind: SourceKindOpenAPI}, []byte(testOpenAPIChatCompletionsSpecYAML))
	if err != nil {
		t.Fatalf("normalize openapi: %v", err)
	}

	validator := NewValidator()
	if err := validator.LoadNormalized("openai", "v1", schema); err != nil {
		t.Fatalf("load normalized schema: %v", err)
	}

	if err := validator.Validate("openai", "v1", []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
	if err := validator.Validate("openai", "v1", []byte(`{"model":"gpt-4o"}`)); err == nil {
		t.Fatal("expected missing messages validation failure")
	}
	if err := validator.Validate("openai", "v1", []byte(`{"model":"gpt-4o","messages":[{"role":"user"}]}`)); err == nil {
		t.Fatal("expected nested message validation failure")
	}
}

func TestOpenAPINormalizer_NormalizeOpenRouter(t *testing.T) {
	normalizer := OpenAPINormalizer{}

	schema, err := normalizer.Normalize(ContractSource{Provider: "openrouter", Version: "v1", Kind: SourceKindOpenAPI}, []byte(testOpenAPIChatCompletionsSpecYAML))
	if err != nil {
		t.Fatalf("normalize openapi: %v", err)
	}

	validator := NewValidator()
	if err := validator.LoadNormalized("openrouter", "v1", schema); err != nil {
		t.Fatalf("load normalized schema: %v", err)
	}

	if err := validator.Validate("openrouter", "v1", []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`)); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestLoadProviderSchema_NormalizesOpenAPIProviders(t *testing.T) {
	validator := NewValidator()
	for _, provider := range []string{"openai", "openrouter"} {
		if err := LoadProviderSchema(validator, provider, "v1", []byte(testOpenAPIChatCompletionsSpecYAML)); err != nil {
			t.Fatalf("load provider schema %s: %v", provider, err)
		}
		if err := validator.Validate(provider, "v1", []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)); err != nil {
			t.Fatalf("validate provider schema %s: %v", provider, err)
		}
	}
}

func TestOpenAPINormalizer_MissingOperation(t *testing.T) {
	normalizer := OpenAPINormalizer{}

	_, err := normalizer.Normalize(ContractSource{Provider: "openai", Version: "v1", Kind: SourceKindOpenAPI}, []byte(testOpenAPINoChatCompletionsSpec))
	if err == nil {
		t.Fatal("expected missing operation error")
	}
}

func TestOpenAPINormalizer_MalformedSchema(t *testing.T) {
	normalizer := OpenAPINormalizer{}

	_, err := normalizer.Normalize(ContractSource{Provider: "openai", Version: "v1", Kind: SourceKindOpenAPI}, []byte(testOpenAPIBrokenSchemaSpec))
	if err == nil {
		t.Fatal("expected malformed schema error")
	}
}

func TestNormalizeOpenAPIAddsDraftSchemaAndWrapsErrors(t *testing.T) {
	data, err := NormalizeOpenAPI("openai", "v1", []byte(testOpenAPIChatCompletionsSpecYAML))
	if err != nil {
		t.Fatalf("NormalizeOpenAPI: %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(data, &normalized); err != nil {
		t.Fatalf("decode normalized schema: %v", err)
	}
	if normalized["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("$schema = %v", normalized["$schema"])
	}

	_, err = NormalizeOpenAPI("unsupported", "v1", []byte(testOpenAPIChatCompletionsSpecYAML))
	if err == nil || !strings.Contains(err.Error(), "extract openapi request schema for unsupported/v1") {
		t.Fatalf("unsupported error = %v", err)
	}
	_, err = NormalizeOpenAPI("openai", "v1", []byte("not yaml: ["))
	if err == nil || !strings.Contains(err.Error(), "parse openapi document for openai/v1") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestLookupRequestSchemaErrorsAndOctetStreamFallback(t *testing.T) {
	doc, err := loadOpenAPIDocument([]byte(`
openapi: 3.1.0
info: {title: Test, version: v1}
paths:
  /v1/chat/completions:
    post:
      requestBody:
        required: true
        content:
          application/octet-stream:
            schema:
              type: string
      responses:
        '200': {description: ok}
`))
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	ref, err := lookupRequestSchema(doc, ContractSource{Provider: "openai", Version: "v1"})
	if err != nil {
		t.Fatalf("lookup octet-stream schema: %v", err)
	}
	if ref.Value.Type == nil || ref.Value.Type.Slice()[0] != "string" {
		t.Fatalf("schema type = %#v, want string", ref.Value.Type)
	}

	doc, err = loadOpenAPIDocument([]byte(`
openapi: 3.1.0
info: {title: Test, version: v1}
paths:
  /v1/chat/completions:
    post:
      responses:
        '200': {description: ok}
`))
	if err != nil {
		t.Fatalf("load no body document: %v", err)
	}
	_, err = lookupRequestSchema(doc, ContractSource{Provider: "openai", Version: "v1"})
	if err == nil || !strings.Contains(err.Error(), "request body not found") {
		t.Fatalf("missing request body error = %v", err)
	}

	doc, err = loadOpenAPIDocument([]byte(`
openapi: 3.1.0
info: {title: Test, version: v1}
paths:
  /v1/chat/completions:
    post:
      requestBody:
        required: true
        content:
          text/plain:
            schema:
              type: string
      responses:
        '200': {description: ok}
`))
	if err != nil {
		t.Fatalf("load text body document: %v", err)
	}
	_, err = lookupRequestSchema(doc, ContractSource{Provider: "openai", Version: "v1"})
	if err == nil || !strings.Contains(err.Error(), "json request body schema not found") {
		t.Fatalf("missing json body schema error = %v", err)
	}
}

func TestNormalizeSchemaRefCoversOpenAPIKeywords(t *testing.T) {
	maxLength := uint64(64)
	maxItems := uint64(5)
	maxProps := uint64(3)
	min := float64(1)
	max := float64(10)
	multipleOf := float64(0.5)
	allowAdditional := false

	ref := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Type:         &openapi3.Types{"object"},
		Title:        "Root",
		Description:  "A root schema",
		Format:       "custom",
		Default:      map[string]any{"kind": "default"},
		Enum:         []any{"a", "b"},
		Required:     []string{"name"},
		Min:          &min,
		Max:          &max,
		ExclusiveMin: true,
		ExclusiveMax: true,
		MultipleOf:   &multipleOf,
		MinLength:    2,
		MaxLength:    &maxLength,
		Pattern:      "^[a-z]+$",
		MinItems:     1,
		MaxItems:     &maxItems,
		UniqueItems:  true,
		MinProps:     1,
		MaxProps:     &maxProps,
		Items:        &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
		Properties: openapi3.Schemas{
			"name": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Nullable: true}},
		},
		AdditionalProperties: openapi3.AdditionalProperties{Has: &allowAdditional},
		AllOf:                []*openapi3.SchemaRef{{Value: &openapi3.Schema{Type: &openapi3.Types{"object"}}}},
		AnyOf:                []*openapi3.SchemaRef{{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}},
		OneOf:                []*openapi3.SchemaRef{{Value: &openapi3.Schema{Type: &openapi3.Types{"number"}}}},
		Not:                  &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"boolean"}}},
	}}

	got, err := normalizeSchemaRef(ref)
	if err != nil {
		t.Fatalf("normalizeSchemaRef: %v", err)
	}
	for _, key := range []string{
		"type", "title", "description", "format", "default", "enum", "required",
		"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
		"minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems",
		"minProperties", "maxProperties", "items", "properties", "additionalProperties",
		"allOf", "anyOf", "oneOf", "not",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("normalized schema missing key %q: %#v", key, got)
		}
	}
	properties := got["properties"].(map[string]any)
	nameType := properties["name"].(map[string]any)["type"].([]string)
	if len(nameType) != 2 || nameType[0] != "string" || nameType[1] != "null" {
		t.Fatalf("nullable property type = %#v", nameType)
	}
}

func TestNormalizeSchemaRefErrorPaths(t *testing.T) {
	if _, err := normalizeSchemaRef(nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil ref error = %v", err)
	}
	if _, err := normalizeSchemaRef(&openapi3.SchemaRef{}); err == nil || !strings.Contains(err.Error(), "no value") {
		t.Fatalf("empty ref error = %v", err)
	}
	if _, err := normalizeSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{
		Items: &openapi3.SchemaRef{},
	}}); err == nil || !strings.Contains(err.Error(), "normalize items") {
		t.Fatalf("items error = %v", err)
	}
	if _, err := normalizeSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{
		Properties: openapi3.Schemas{"bad": {}},
	}}); err == nil || !strings.Contains(err.Error(), `normalize property "bad"`) {
		t.Fatalf("property error = %v", err)
	}
	if _, err := normalizeSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{
		AdditionalProperties: openapi3.AdditionalProperties{Schema: &openapi3.SchemaRef{}},
	}}); err == nil || !strings.Contains(err.Error(), "normalize additionalProperties") {
		t.Fatalf("additionalProperties error = %v", err)
	}
	if _, err := normalizeSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{
		AllOf: []*openapi3.SchemaRef{{}},
	}}); err == nil || !strings.Contains(err.Error(), "normalize allOf") {
		t.Fatalf("allOf error = %v", err)
	}
	if _, err := normalizeSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{
		AnyOf: []*openapi3.SchemaRef{{}},
	}}); err == nil || !strings.Contains(err.Error(), "normalize anyOf") {
		t.Fatalf("anyOf error = %v", err)
	}
	if _, err := normalizeSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{
		OneOf: []*openapi3.SchemaRef{{}},
	}}); err == nil || !strings.Contains(err.Error(), "normalize oneOf") {
		t.Fatalf("oneOf error = %v", err)
	}
	if _, err := normalizeSchemaRef(&openapi3.SchemaRef{Value: &openapi3.Schema{
		Not: &openapi3.SchemaRef{},
	}}); err == nil || !strings.Contains(err.Error(), "normalize not") {
		t.Fatalf("not error = %v", err)
	}
}
