package openai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const toolsPayload = `[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}]`

func TestToolCallRequired_NonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":` + toolsPayload + `,"tool_choice":"required"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatal("expected choices")
	}
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason: got %v, want tool_calls", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	toolCalls, ok := msg["tool_calls"].([]any)
	if !ok || len(toolCalls) == 0 {
		t.Fatal("expected tool_calls in message")
	}
	tc := toolCalls[0].(map[string]any)
	if tc["type"] != "function" {
		t.Errorf("tool_calls[0].type: got %v, want function", tc["type"])
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("function name: got %v, want get_weather", fn["name"])
	}
	// arguments must be valid JSON containing "location"
	args := fn["arguments"].(string)
	var argMap map[string]any
	if err := json.Unmarshal([]byte(args), &argMap); err != nil {
		t.Errorf("arguments not valid JSON: %v; got %q", err, args)
	}
	if _, ok := argMap["location"]; !ok {
		t.Error("expected 'location' in synthesized arguments")
	}
}

func TestToolCallRequired_Streaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}],"tools":` + toolsPayload + `,"tool_choice":"required"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	raw := rr.Body.String()
	if !strings.Contains(raw, "tool_calls") {
		t.Errorf("expected tool_calls in streaming response; got:\n%s", raw)
	}
	if !strings.Contains(raw, "get_weather") {
		t.Errorf("expected function name in streaming response; got:\n%s", raw)
	}
}

func TestToolChoiceNone_ReturnsText(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":` + toolsPayload + `,"tool_choice":"none"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	choices := resp["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] == "tool_calls" {
		t.Error("tool_choice:none should return text, not tool_calls")
	}
}

func TestToolCallNamed_NonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":` + toolsPayload + `,"tool_choice":{"type":"function","function":{"name":"get_weather"}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	choices := resp["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason: got %v, want tool_calls", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	toolCalls := msg["tool_calls"].([]any)
	tc := toolCalls[0].(map[string]any)
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("expected get_weather, got %v", fn["name"])
	}
}

func TestToolCallRequired_ArgumentsConformToSchema(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tool_choice":"required",` +
		`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object",` +
		`"required":["unit","n","u","tags","email","k"],"properties":{` +
		`"unit":{"type":"string","enum":["c","f"]},"n":{"type":["integer","null"]},` +
		`"u":{"anyOf":[{"type":"integer"},{"type":"null"}]},` +
		`"tags":{"type":"array","items":{"type":"string"},"minItems":1},` +
		`"email":{"type":"string","format":"email"},"k":{"const":"fixed"}}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var resp struct {
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
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
		t.Fatalf("decode: %v; body: %s", err, rr.Body.String())
	}
	var args struct {
		Unit  string   `json:"unit"`
		N     *float64 `json:"n"`
		U     *float64 `json:"u"`
		Tags  []string `json:"tags"`
		Email string   `json:"email"`
		K     string   `json:"k"`
	}
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.ToolCalls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("arguments do not decode into the schema's types: %v\n%s", err, resp.Choices[0].Message.ToolCalls[0].Function.Arguments)
	}
	if args.Unit != "c" && args.Unit != "f" {
		t.Errorf("unit: got %q", args.Unit)
	}
	if args.N == nil || *args.N != float64(int(*args.N)) {
		t.Errorf("n: want integer, got %v", args.N)
	}
	if args.U == nil || *args.U != float64(int(*args.U)) {
		t.Errorf("u: want integer, got %v", args.U)
	}
	if len(args.Tags) < 1 || args.Tags[0] == "" {
		t.Errorf("tags: want >=1 string, got %v", args.Tags)
	}
	if !strings.Contains(args.Email, "@") {
		t.Errorf("email: got %q", args.Email)
	}
	if args.K != "fixed" {
		t.Errorf("k: got %q", args.K)
	}
}

// Hostile numeric literals in a request-supplied schema must neither crash,
// stall nor balloon synthesis.
func TestToolCallRequired_HostileNumbersInSchema(t *testing.T) {
	h := newHandler(t)
	for _, n := range []string{"1e999999", "-1e999999", "1e-999999"} {
		params := `{"type":"object","required":["a","b","c"],"properties":{` +
			`"a":{"type":"array","items":{"type":"integer"},"minItems":` + n + `,"maxItems":` + n + `},` +
			`"b":{"type":"number","minimum":` + n + `,"multipleOf":` + n + `,"enum":[` + n + `]},` +
			`"c":{"type":"string","minLength":` + n + `,"const":` + n + `}}}`
		body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tool_choice":"required","tools":[{"type":"function","function":{"name":"f","parameters":` + params + `}}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer sk-test")
		rr := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rr, req)
		if el := time.Since(start); el > time.Second {
			t.Errorf("%s: took %v", n, el)
		}
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", n, rr.Code, rr.Body.String())
		}
		var resp struct {
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
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil || len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
			t.Fatalf("%s: decode: %v", n, err)
		}
		if args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments; !json.Valid([]byte(args)) || len(args) > 4096 {
			t.Errorf("%s: bad arguments %q", n, args)
		}
	}
}
