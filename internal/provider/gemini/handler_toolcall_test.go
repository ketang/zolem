package gemini_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const geminiFuncDecl = `{"functionDeclarations":[{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]}`

func TestFunctionCallANY_NonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[` + geminiFuncDecl + `],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	candidates, ok := resp["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		t.Fatal("expected candidates")
	}
	candidate := candidates[0].(map[string]any)
	content := candidate["content"].(map[string]any)
	parts := content["parts"].([]any)
	if len(parts) == 0 {
		t.Fatal("expected parts")
	}
	part := parts[0].(map[string]any)
	fc, ok := part["functionCall"].(map[string]any)
	if !ok {
		t.Fatalf("expected functionCall in part; got: %v", part)
	}
	if fc["name"] != "get_weather" {
		t.Errorf("functionCall.name: got %v, want get_weather", fc["name"])
	}
	argsRaw, _ := json.Marshal(fc["args"])
	var args map[string]any
	if err := json.Unmarshal(argsRaw, &args); err != nil {
		t.Errorf("args not valid JSON object: %v", err)
	}
	if _, ok := args["location"]; !ok {
		t.Error("expected 'location' in synthesized args")
	}
}

func TestFunctionCallANY_Streaming(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[` + geminiFuncDecl + `],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:streamGenerateContent", bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	raw := rr.Body.String()
	if !strings.Contains(raw, "functionCall") {
		t.Errorf("expected functionCall in streaming response; got:\n%s", raw)
	}
	if !strings.Contains(raw, "get_weather") {
		t.Errorf("expected function name in streaming response; got:\n%s", raw)
	}
}

func TestFunctionCallModeNONE_ReturnsText(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[` + geminiFuncDecl + `],"toolConfig":{"functionCallingConfig":{"mode":"NONE"}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	candidates := resp["candidates"].([]any)
	candidate := candidates[0].(map[string]any)
	content := candidate["content"].(map[string]any)
	parts := content["parts"].([]any)
	part := parts[0].(map[string]any)
	if _, hasFc := part["functionCall"]; hasFc {
		t.Error("mode NONE should return text, not functionCall")
	}
	if _, hasText := part["text"]; !hasText {
		t.Error("mode NONE should return text part")
	}
}

func TestFunctionCallModeAUTO_ReturnsText(t *testing.T) {
	// mode AUTO lets the model decide whether to call a function. The local
	// runtime does not run a model, so it does not synthesize a function call:
	// only mode ANY (a mandatory call) is synthesized. AUTO therefore falls
	// through to the lorem/backend text path. An SDK expecting a function call
	// in AUTO mode will get a text response. See geminiToolCallRequired.
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[` + geminiFuncDecl + `],"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	candidates := resp["candidates"].([]any)
	candidate := candidates[0].(map[string]any)
	content := candidate["content"].(map[string]any)
	parts := content["parts"].([]any)
	part := parts[0].(map[string]any)
	if _, hasFc := part["functionCall"]; hasFc {
		t.Error("mode AUTO should return text, not a synthesized functionCall")
	}
	if _, hasText := part["text"]; !hasText {
		t.Error("mode AUTO should return a text part")
	}
}

func TestFunctionCallAllowedNames_Filtered(t *testing.T) {
	h := newHandler(t)
	twoFuncs := `[{"functionDeclarations":[{"name":"get_weather"},{"name":"search","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}]`
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":` + twoFuncs + `,"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["search"]}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	candidates := resp["candidates"].([]any)
	candidate := candidates[0].(map[string]any)
	content := candidate["content"].(map[string]any)
	parts := content["parts"].([]any)
	part := parts[0].(map[string]any)
	fc := part["functionCall"].(map[string]any)
	if fc["name"] != "search" {
		t.Errorf("expected 'search' (from allowedFunctionNames), got %v", fc["name"])
	}
}
