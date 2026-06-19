package anthropic_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const anthropicToolsPayload = `[{"name":"get_weather","description":"Get weather","input_schema":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]`

func TestToolCallAny_NonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":` + anthropicToolsPayload + `,"tool_choice":{"type":"any"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("x-api-key", "sk-test")
	req.Header.Set("anthropic-version", "2023-06-01")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason: got %v, want tool_use", resp["stop_reason"])
	}
	content, ok := resp["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	block := content[0].(map[string]any)
	if block["type"] != "tool_use" {
		t.Errorf("content[0].type: got %v, want tool_use", block["type"])
	}
	if block["name"] != "get_weather" {
		t.Errorf("tool name: got %v, want get_weather", block["name"])
	}
	inputRaw, _ := json.Marshal(block["input"])
	var inputMap map[string]any
	if err := json.Unmarshal(inputRaw, &inputMap); err != nil {
		t.Errorf("input not valid JSON object: %v", err)
	}
	if _, ok := inputMap["location"]; !ok {
		t.Error("expected 'location' in synthesized input")
	}
}

func TestToolCallTool_NonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":` + anthropicToolsPayload + `,"tool_choice":{"type":"tool","name":"get_weather"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("x-api-key", "sk-test")
	req.Header.Set("anthropic-version", "2023-06-01")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason: got %v, want tool_use", resp["stop_reason"])
	}
}

func TestToolCallAny_Streaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}],"tools":` + anthropicToolsPayload + `,"tool_choice":{"type":"any"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("x-api-key", "sk-test")
	req.Header.Set("anthropic-version", "2023-06-01")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	raw := rr.Body.String()
	if !strings.Contains(raw, "tool_use") {
		t.Errorf("expected tool_use in streaming response; got:\n%s", raw)
	}
	if !strings.Contains(raw, "get_weather") {
		t.Errorf("expected function name in streaming response; got:\n%s", raw)
	}
}

func TestToolChoiceAuto_ReturnsText(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":` + anthropicToolsPayload + `,"tool_choice":{"type":"auto"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("x-api-key", "sk-test")
	req.Header.Set("anthropic-version", "2023-06-01")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["stop_reason"] == "tool_use" {
		t.Error("tool_choice auto should return text, not tool_use")
	}
}
