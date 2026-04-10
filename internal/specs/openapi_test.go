package specs

import "testing"

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
