package specs_test

import (
	"strings"
	"testing"

	"zolem.dev/zolem/internal/specs"
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

func TestNormalizeOpenAPI_OpenAI(t *testing.T) {
	normalized, err := specs.NormalizeOpenAPI("openai", "v1", []byte(testOpenAPIChatCompletionsSpecYAML))
	if err != nil {
		t.Fatalf("normalize openapi: %v", err)
	}

	validator := specs.NewValidator()
	if err := validator.LoadRaw("openai", "v1", normalized); err != nil {
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

func TestNormalizeOpenAPI_OpenRouter(t *testing.T) {
	normalized, err := specs.NormalizeOpenAPI("openrouter", "v1", []byte(testOpenAPIChatCompletionsSpecYAML))
	if err != nil {
		t.Fatalf("normalize openapi: %v", err)
	}

	validator := specs.NewValidator()
	if err := validator.LoadRaw("openrouter", "v1", normalized); err != nil {
		t.Fatalf("load normalized schema: %v", err)
	}

	if err := validator.Validate("openrouter", "v1", []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`)); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

func TestLoadProviderSchema_NormalizesOpenAPIProviders(t *testing.T) {
	validator := specs.NewValidator()
	for _, provider := range []string{"openai", "openrouter"} {
		if err := specs.LoadProviderSchema(validator, provider, "v1", []byte(testOpenAPIChatCompletionsSpecYAML)); err != nil {
			t.Fatalf("load provider schema %s: %v", provider, err)
		}
		if err := validator.Validate(provider, "v1", []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)); err != nil {
			t.Fatalf("validate provider schema %s: %v", provider, err)
		}
	}
}

func TestNormalizeOpenAPI_MissingOperation(t *testing.T) {
	_, err := specs.NormalizeOpenAPI("openai", "v1", []byte(testOpenAPINoChatCompletionsSpec))
	if err == nil {
		t.Fatal("expected missing operation error")
	}
	if !strings.Contains(err.Error(), "operation not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeOpenAPI_MalformedSchema(t *testing.T) {
	_, err := specs.NormalizeOpenAPI("openai", "v1", []byte(testOpenAPIBrokenSchemaSpec))
	if err == nil {
		t.Fatal("expected malformed schema error")
	}
	if !strings.Contains(err.Error(), "MissingMessage") {
		t.Fatalf("unexpected error: %v", err)
	}
}
