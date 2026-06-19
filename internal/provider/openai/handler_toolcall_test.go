package openai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
