package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// buildProviderApp builds a local startup app for the given provider using the
// production startup defaults, so the real vendored spec schemas are loaded
// into the validator (no disabledTestFetcher).
func buildProviderApp(t *testing.T, provider string) *startupApp {
	t.Helper()
	app, _, err := buildLocalStartupAppForRuntime(runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     provider + "-demo",
			Addr:     "127.0.0.1:0",
			Provider: provider,
			Profile:  "demo",
		},
		Profile: runtimecfg.RuntimeProfile{Name: "demo", Backend: runtimecfg.BackendLorem},
	}, "", runtimecfg.NewProfileCounters(), nil, RecordCaps{}, startupDeps{})
	if err != nil {
		t.Fatalf("buildLocalStartupAppForRuntime(%s): %v", provider, err)
	}
	return app
}

func TestSpecValidation_OpenAIRejectsSchemaViolation(t *testing.T) {
	app := buildProviderApp(t, "openai")
	defer app.close()

	// Valid request must still succeed.
	valid := httptestRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	valid.Header.Set("Authorization", "Bearer sk-test")
	validResp := doRequest(t, app.handler, valid)
	defer validResp.Body.Close()
	if validResp.StatusCode != http.StatusOK {
		t.Fatalf("valid openai request: got %d, want 200", validResp.StatusCode)
	}

	// A message with a non-string/array content is a schema violation that
	// the handler alone does not catch (it would otherwise serve a 200).
	invalid := httptestRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":123}]}`))
	invalid.Header.Set("Authorization", "Bearer sk-test")
	invalidResp := doRequest(t, app.handler, invalid)
	defer invalidResp.Body.Close()
	if invalidResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid openai request: got %d, want 400", invalidResp.StatusCode)
	}
}

// TestSpecValidation_OpenAIAcceptsAssistantToolCallWithoutContent covers the
// second request of an agent loop: the assistant tool-call turn echoed back
// with content null or omitted, followed by a tool message.
func TestSpecValidation_OpenAIAcceptsAssistantToolCallWithoutContent(t *testing.T) {
	app := buildProviderApp(t, "openai")
	defer app.close()

	toolCall := `"tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]`
	cases := map[string]string{
		"null":    `{"role":"assistant","content":null,` + toolCall + `}`,
		"omitted": `{"role":"assistant",` + toolCall + `}`,
	}
	for name, assistant := range cases {
		t.Run(name, func(t *testing.T) {
			body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"},` + assistant + `,{"role":"tool","tool_call_id":"call_1","content":"42"}]}`
			req := httptestRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
			req.Header.Set("Authorization", "Bearer sk-test")
			resp := doRequest(t, app.handler, req)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("got %d, want 200", resp.StatusCode)
			}
		})
	}
}

func TestSpecValidation_GeminiRejectsSchemaViolation(t *testing.T) {
	app := buildProviderApp(t, "gemini")
	defer app.close()

	valid := httptestRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	valid.Header.Set("x-goog-api-key", "test-key")
	validResp := doRequest(t, app.handler, valid)
	defer validResp.Body.Close()
	if validResp.StatusCode != http.StatusOK {
		t.Fatalf("valid gemini request: got %d, want 200", validResp.StatusCode)
	}

	// A content part missing its required text field is a schema violation.
	invalid := httptestRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{}]}]}`))
	invalid.Header.Set("x-goog-api-key", "test-key")
	invalidResp := doRequest(t, app.handler, invalid)
	defer invalidResp.Body.Close()
	if invalidResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid gemini request: got %d, want 400", invalidResp.StatusCode)
	}
}

func TestSpecValidation_StateReportsLoadedSchemas(t *testing.T) {
	app := buildProviderApp(t, "openai")
	defer app.close()

	resp := doRequest(t, app.handler, httptestRequest(http.MethodGet, "/_zolem/state", bytes.NewBufferString("")))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state: got %d, want 200", resp.StatusCode)
	}

	var payload struct {
		SchemasLoaded    []string `json:"schemas_loaded"`
		SchemaValidation string   `json:"schema_validation"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if payload.SchemaValidation != "enabled" {
		t.Fatalf("schema_validation: got %q, want enabled", payload.SchemaValidation)
	}
	var found bool
	for _, key := range payload.SchemasLoaded {
		if key == "openai:v1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("schemas_loaded missing openai:v1: %v", payload.SchemasLoaded)
	}
}
