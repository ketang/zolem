package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestE2E_Gemini_ToolCallArgsFollowSchema verifies that forced Gemini function
// calls carry arguments built from the declaration's schema, for both the
// upper-case OpenAPI-subset `parameters` and plain-JSON-Schema
// `parametersJsonSchema`.
func TestE2E_Gemini_ToolCallArgsFollowSchema(t *testing.T) {
	svc := startLoremService(t, "gemini")
	headers := []string{"x-goog-api-key: k", "Content-Type: application/json"}
	for name, decl := range map[string]string{
		"uppercase_parameters":   `{"name":"get_weather","parameters":{"type":"OBJECT","properties":{"city":{"type":"STRING"}},"required":["city"]}}`,
		"parameters_json_schema": `{"name":"get_weather","parametersJsonSchema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}},"tools":[{"functionDeclarations":[` + decl + `]}]}`
			resp, raw := doRequest(t, svc.baseURL, http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", body, headers...)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", resp.StatusCode, raw)
			}
			var out struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							FunctionCall struct {
								Args map[string]any `json:"args"`
							} `json:"functionCall"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			mustJSONUnmarshal(t, raw, &out)
			if city, _ := out.Candidates[0].Content.Parts[0].FunctionCall.Args["city"].(string); city == "" {
				t.Fatalf("args.city must be a non-empty string: %s", raw)
			}
		})
	}
}

// TestE2E_OpenAI_ToolCallArgsFollowSchema covers enum, const, nullable type
// arrays, anyOf, minItems and format on a forced OpenAI tool call.
func TestE2E_OpenAI_ToolCallArgsFollowSchema(t *testing.T) {
	svc := startLoremService(t, "openai")
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tool_choice":"required",` +
		`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object",` +
		`"required":["unit","n","u","tags","email","k"],"properties":{` +
		`"unit":{"type":"string","enum":["c","f"]},"n":{"type":["integer","null"]},` +
		`"u":{"anyOf":[{"type":"integer"},{"type":"null"}]},` +
		`"tags":{"type":"array","items":{"type":"string"},"minItems":1},` +
		`"email":{"type":"string","format":"email"},"k":{"const":"fixed"}}}}}]}`
	resp, raw := doRequest(t, svc.baseURL, http.MethodPost, "/v1/chat/completions", body,
		"Authorization: Bearer x", "Content-Type: application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	mustJSONUnmarshal(t, raw, &out)
	var args struct {
		Unit  string   `json:"unit"`
		N     *int     `json:"n"`
		U     *int     `json:"u"`
		Tags  []string `json:"tags"`
		Email string   `json:"email"`
		K     string   `json:"k"`
	}
	if err := json.Unmarshal([]byte(out.Choices[0].Message.ToolCalls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("arguments do not match the schema's types: %v: %s", err, raw)
	}
	if (args.Unit != "c" && args.Unit != "f") || args.N == nil || args.U == nil ||
		len(args.Tags) < 1 || !strings.Contains(args.Email, "@") || args.K != "fixed" {
		t.Fatalf("arguments violate schema: %+v", args)
	}
}
