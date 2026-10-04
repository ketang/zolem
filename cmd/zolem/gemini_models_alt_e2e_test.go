package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const geminiStreamBody = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`

var geminiKey = []string{"Content-Type: application/json", "x-goog-api-key: k"}

func geminiModelNames(t *testing.T, base, path string) []string {
	t.Helper()
	resp, body := doRequest(t, base, http.MethodGet, path, "", geminiKey...)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	mustJSONUnmarshal(t, body, &payload)
	var names []string
	for _, m := range payload.Models {
		names = append(names, m.Name)
	}
	return names
}

func geminiJSONArray(t *testing.T, resp *http.Response, body []byte) []map[string]any {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type: got %q, want application/json", ct)
	}
	var arr []map[string]any
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("body is not a JSON array: %v\n%s", err, body)
	}
	if len(arr) == 0 {
		t.Fatalf("empty array")
	}
	return arr
}

func assertFinalFinishReason(t *testing.T, arr []map[string]any) {
	t.Helper()
	last := arr[len(arr)-1]
	cands, _ := last["candidates"].([]any)
	if len(cands) == 0 {
		t.Fatalf("last element has no candidates: %v", last)
	}
	if fr, _ := cands[0].(map[string]any)["finishReason"].(string); fr == "" {
		t.Fatalf("last element has no finishReason: %v", last)
	}
}

func TestE2E_GeminiModelsAndAlt(t *testing.T) {
	t.Run("fixed_listener", func(t *testing.T) {
		svc := startLoremService(t, "gemini")

		t.Run("list", func(t *testing.T) {
			for _, path := range []string{"/v1beta/models", "/v1/models"} {
				names := geminiModelNames(t, svc.baseURL, path)
				for _, want := range []string{"models/gemini-2.0-flash", "models/gemini-2.0-flash-lite", "models/gemini-2.5-flash", "models/gemini-2.5-pro"} {
					found := false
					for _, n := range names {
						found = found || n == want
					}
					if !found {
						t.Errorf("%s: missing %s in %v", path, want, names)
					}
				}
			}
		})

		t.Run("get_known", func(t *testing.T) {
			for _, path := range []string{"/v1beta/models/gemini-2.0-flash", "/v1beta/models/models/gemini-2.0-flash"} {
				resp, body := doRequest(t, svc.baseURL, http.MethodGet, path, "", geminiKey...)
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s: status %d: %s", path, resp.StatusCode, body)
				}
				var m struct {
					Name string `json:"name"`
				}
				mustJSONUnmarshal(t, body, &m)
				if m.Name != "models/gemini-2.0-flash" {
					t.Fatalf("%s: name %q", path, m.Name)
				}
			}
		})

		t.Run("get_unknown", func(t *testing.T) {
			resp, body := doRequest(t, svc.baseURL, http.MethodGet, "/v1beta/models/no-such-model", "", geminiKey...)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			var env struct {
				Error struct {
					Status string `json:"status"`
				} `json:"error"`
			}
			mustJSONUnmarshal(t, body, &env)
			if env.Error.Status != "NOT_FOUND" {
				t.Fatalf("error.status: %q", env.Error.Status)
			}
		})

		t.Run("wrong_method_405_json", func(t *testing.T) {
			for _, tc := range []struct{ method, path string }{
				{http.MethodDelete, "/v1beta/models"},
				{http.MethodPut, "/v1beta/models/gemini-2.0-flash"},
			} {
				resp, body := doRequest(t, svc.baseURL, tc.method, tc.path, "", geminiKey...)
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s: status %d", tc.method, tc.path, resp.StatusCode)
				}
				if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
					t.Fatalf("%s %s: content-type %q", tc.method, tc.path, ct)
				}
				var env struct {
					Error struct {
						Code int `json:"code"`
					} `json:"error"`
				}
				mustJSONUnmarshal(t, body, &env)
				if env.Error.Code != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s: envelope %s", tc.method, tc.path, body)
				}
			}
		})

		t.Run("stream_alt_sse", func(t *testing.T) {
			resp, body := doRequest(t, svc.baseURL, http.MethodPost, "/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse", geminiStreamBody, geminiKey...)
			defer resp.Body.Close()
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
				t.Fatalf("content-type %q: %s", ct, body)
			}
		})

		t.Run("stream_no_alt_and_alt_json", func(t *testing.T) {
			for _, q := range []string{"", "?alt=json"} {
				resp, body := doRequest(t, svc.baseURL, http.MethodPost, "/v1beta/models/gemini-2.0-flash:streamGenerateContent"+q, geminiStreamBody, geminiKey...)
				defer resp.Body.Close()
				assertFinalFinishReason(t, geminiJSONArray(t, resp, body))
			}
		})

		t.Run("stream_no_alt_tool_call", func(t *testing.T) {
			reqBody := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"get_weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`
			resp, body := doRequest(t, svc.baseURL, http.MethodPost, "/v1beta/models/gemini-2.0-flash:streamGenerateContent", reqBody, geminiKey...)
			defer resp.Body.Close()
			arr := geminiJSONArray(t, resp, body)
			if !strings.Contains(string(body), "get_weather") {
				t.Fatalf("expected functionCall in %v", arr)
			}
		})
	})

	t.Run("stream_no_alt_fixture", func(t *testing.T) {
		svc := startFixedService(t, "gemini")
		resp, body := doRequest(t, svc.baseURL, http.MethodPost, "/v1beta/models/gemini-2.0-flash:streamGenerateContent", geminiStreamBody, geminiKey...)
		defer resp.Body.Close()
		arr := geminiJSONArray(t, resp, body)
		var text strings.Builder
		for _, chunk := range arr {
			cands, _ := chunk["candidates"].([]any)
			if len(cands) == 0 {
				continue
			}
			content, _ := cands[0].(map[string]any)["content"].(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, p := range parts {
				s, _ := p.(map[string]any)["text"].(string)
				text.WriteString(s)
			}
		}
		if got := strings.Join(strings.Fields(text.String()), " "); got != "Fixture says hello from gemini." {
			t.Fatalf("fixture text: got %q", got)
		}
		assertFinalFinishReason(t, arr)
	})

	t.Run("control_plane", func(t *testing.T) {
		admin := startLocalAdminService(t, repoRoot(t))
		t.Cleanup(admin.Close)

		t.Run("list_with_forced_model", func(t *testing.T) {
			base := createRuntimeListener(t, admin, "gemini", map[string]any{
				"backend": "lorem", "response_model_policy": "force_literal", "response_model": "pinned-x",
			})
			if names := geminiModelNames(t, base, "/v1beta/models"); names[0] != "models/pinned-x" {
				t.Fatalf("first: %v", names)
			}
			resp, body := doRequest(t, base, http.MethodGet, "/v1beta/models/pinned-x", "", geminiKey...)
			defer resp.Body.Close()
			var m struct {
				Name string `json:"name"`
			}
			mustJSONUnmarshal(t, body, &m)
			if resp.StatusCode != http.StatusOK || m.Name != "models/pinned-x" {
				t.Fatalf("get pinned: %d %s", resp.StatusCode, body)
			}
		})

		t.Run("list_with_force_backend", func(t *testing.T) {
			base := createRuntimeListener(t, admin, "gemini", map[string]any{
				"backend": "lorem", "response_model_policy": "force_backend", "backend_model": "gemini-x",
			})
			if names := geminiModelNames(t, base, "/v1beta/models"); names[0] != "models/gemini-x" {
				t.Fatalf("first: %v", names)
			}
		})

		t.Run("list_with_existing_pinned", func(t *testing.T) {
			base := createRuntimeListener(t, admin, "gemini", map[string]any{
				"backend": "lorem", "response_model_policy": "force_literal", "response_model": "gemini-2.5-pro",
			})
			names := geminiModelNames(t, base, "/v1beta/models")
			count := 0
			for _, n := range names {
				if n == "models/gemini-2.5-pro" {
					count++
				}
			}
			if names[0] != "models/gemini-2.5-pro" || count != 1 {
				t.Fatalf("want gemini-2.5-pro first and once: %v", names)
			}
		})

		t.Run("forced_errors", func(t *testing.T) {
			for et, want := range map[string]int{"rate_limit": http.StatusTooManyRequests, "authentication": http.StatusForbidden} {
				base := createRuntimeListener(t, admin, "gemini", map[string]any{"backend": "error", "error_type": et})
				for _, path := range []string{"/v1beta/models", "/v1beta/models/gemini-2.0-flash"} {
					resp, body := doRequest(t, base, http.MethodGet, path, "") // no key: forced error wins
					resp.Body.Close()
					if resp.StatusCode != want {
						t.Fatalf("%s %s: got %d want %d: %s", et, path, resp.StatusCode, want, body)
					}
				}
			}
		})

		t.Run("alt_json_pre_output_failure", func(t *testing.T) {
			base := createRuntimeListener(t, admin, "gemini", map[string]any{
				"backend": "ollama", "backend_model": "m", "ollama_upstream": "http://127.0.0.1:1",
			})
			resp, body := doRequest(t, base, http.MethodPost, "/v1beta/models/gemini-2.0-flash:streamGenerateContent", geminiStreamBody, geminiKey...)
			defer resp.Body.Close()
			nsResp, _ := doRequest(t, base, http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", geminiStreamBody, geminiKey...)
			defer nsResp.Body.Close()
			if resp.StatusCode < 400 || resp.StatusCode != nsResp.StatusCode {
				t.Fatalf("status %d, want non-2xx equal to non-stream %d: %s", resp.StatusCode, nsResp.StatusCode, body)
			}
			if strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
				t.Fatalf("body must not be an array: %s", body)
			}
			var env struct {
				Error map[string]any `json:"error"`
			}
			mustJSONUnmarshal(t, body, &env)
			if env.Error == nil {
				t.Fatalf("no single error object: %s", body)
			}
		})
	})
}
