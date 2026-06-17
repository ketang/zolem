package gemini_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCountTokens_MissingAuth(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:countTokens", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", rr.Code)
	}
}

func TestCountTokens_ReturnsEstimate(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hello world from zolem"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:countTokens", bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "test-key")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalTokens <= 0 {
		t.Fatalf("totalTokens: got %d, want > 0", resp.TotalTokens)
	}
}

func TestCountTokens_AcceptsWrappedRequest(t *testing.T) {
	h := newHandler(t)
	body := `{"generateContentRequest":{"contents":[{"role":"user","parts":[{"text":"hello world"}]}]}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:countTokens", bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalTokens <= 0 {
		t.Fatalf("totalTokens: got %d, want > 0", resp.TotalTokens)
	}
}

func TestCountTokens_MissingContents(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:countTokens", bytes.NewBufferString(body))
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}
