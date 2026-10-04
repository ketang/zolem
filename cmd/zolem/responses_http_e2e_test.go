package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const responsesWASMBase64 = "AGFzbQEAAAABFQRgAX8Bf2ACf38AYAJ/fwF/YAF/AAMHBgABAgAAAwUDAQABB08HBm1lbW9yeQIABWFsbG9jAAAHZGVhbGxvYwABCGdlbmVyYXRlAAIKcmVzdWx0X3B0cgADCnJlc3VsdF9sZW4ABAtyZXN1bHRfZnJlZQAFCh0GBQBBgAgLAgALBABBAQsFAEGAEAsEAEEXCwIACwseAQBBgBALF1siSGVsbG8gIiwiZnJvbSBXQVNNLiJd"

type responsesPayload struct {
	ID     string `json:"id"`
	Object string `json:"object"`
	Status string `json:"status"`
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func postResponses(t *testing.T, baseURL, body string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	if len(headers) == 0 {
		headers = []string{"Authorization: Bearer sk-test"}
	}
	headers = append(headers, "Content-Type: application/json")
	return doRequest(t, baseURL, http.MethodPost, "/v1/responses", body, headers...)
}

func assertOpenAIErrorEnvelope(t *testing.T, body []byte, wantType string) {
	t.Helper()
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	mustJSONUnmarshal(t, body, &payload)
	if payload.Error.Type != wantType {
		t.Fatalf("error type: got %q, want %q: %s", payload.Error.Type, wantType, body)
	}
}

func TestE2E_ResponsesHTTP(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	t.Run("lorem", func(t *testing.T) {
		base := createRuntimeListener(t, admin, "openai", map[string]any{"backend": "lorem"})
		resp, body := postResponses(t, base, `{"model":"gpt-4o","input":"hello there"}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type: got %q", got)
		}
		var p responsesPayload
		mustJSONUnmarshal(t, body, &p)
		if p.Object != "response" || p.Status != "completed" || !strings.HasPrefix(p.ID, "resp_zolem") {
			t.Fatalf("identity: %s", body)
		}
		if len(p.Output) != 1 || p.Output[0].Type != "message" || p.Output[0].Role != "assistant" ||
			len(p.Output[0].Content) != 1 || p.Output[0].Content[0].Type != "output_text" || p.Output[0].Content[0].Text == "" {
			t.Fatalf("output: %s", body)
		}
		if p.Usage.OutputTokens == 0 || p.Usage.TotalTokens != p.Usage.InputTokens+p.Usage.OutputTokens {
			t.Fatalf("usage: %s", body)
		}
		if p.Model != "gpt-4o" {
			t.Fatalf("model: got %q", p.Model)
		}
	})

	t.Run("error_backend_rate_limit", func(t *testing.T) {
		base := createRuntimeListener(t, admin, "openai", map[string]any{"backend": "error", "error_type": "rate_limit"})
		resp, body := postResponses(t, base, `{"model":"gpt-4o","input":"hi"}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429: %s", resp.StatusCode, body)
		}
		assertOpenAIErrorEnvelope(t, body, "rate_limit_error")
	})

	t.Run("wasm_backend", func(t *testing.T) {
		base := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend":                  "wasm",
			"wasm_module_base64":       responsesWASMBase64,
			"wasm_generate_timeout_ms": 100,
		})
		resp, body := postResponses(t, base, `{"model":"gpt-4o","input":"hi"}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d: %s", resp.StatusCode, body)
		}
		var p responsesPayload
		mustJSONUnmarshal(t, body, &p)
		if len(p.Output) != 1 || len(p.Output[0].Content) != 1 || p.Output[0].Content[0].Text != "Hello from WASM." {
			t.Fatalf("output: %s", body)
		}
	})

	t.Run("response_model_override", func(t *testing.T) {
		base := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend":               "lorem",
			"response_model_policy": "force_literal",
			"response_model":        "pinned-x",
		})
		resp, body := postResponses(t, base, `{"model":"gpt-4o","input":"hi"}`)
		defer resp.Body.Close()
		var p responsesPayload
		mustJSONUnmarshal(t, body, &p)
		if resp.StatusCode != http.StatusOK || p.Model != "pinned-x" {
			t.Fatalf("status %d model %q: %s", resp.StatusCode, p.Model, body)
		}
	})

	t.Run("missing_auth", func(t *testing.T) {
		base := createRuntimeListener(t, admin, "openai", map[string]any{"backend": "lorem"})
		resp, body := doRequest(t, base, http.MethodPost, "/v1/responses", `{"model":"gpt-4o","input":"hi"}`, "Content-Type: application/json")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "invalid_api_key") {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("missing_model", func(t *testing.T) {
		base := createRuntimeListener(t, admin, "openai", map[string]any{"backend": "lorem"})
		resp, body := postResponses(t, base, `{"input":"hi"}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		assertOpenAIErrorEnvelope(t, body, "invalid_request_error")
	})

	t.Run("stream_true_interim", func(t *testing.T) {
		base := createRuntimeListener(t, admin, "openai", map[string]any{"backend": "lorem"})
		resp, body := postResponses(t, base, `{"model":"gpt-4o","input":"hi","stream":true}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "streaming is not supported yet") {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
	})

	t.Run("put_405_json", func(t *testing.T) {
		base := createRuntimeListener(t, admin, "openai", map[string]any{"backend": "lorem"})
		resp, body := doRequest(t, base, http.MethodPut, "/v1/responses", `{}`, "Content-Type: application/json")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type: got %q", got)
		}
		if got := resp.Header.Get("Allow"); !strings.Contains(got, "POST") || !strings.Contains(got, "GET") {
			t.Fatalf("Allow: got %q", got)
		}
		assertOpenAIErrorEnvelope(t, body, "invalid_request_error")
	})

	t.Run("input_conversion", func(t *testing.T) {
		var mu sync.Mutex
		var last []map[string]string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var req struct {
				Messages []map[string]string `json:"messages"`
			}
			_ = json.Unmarshal(raw, &req)
			mu.Lock()
			last = req.Messages
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "upstream-reply"}}},
			})
		}))
		t.Cleanup(upstream.Close)

		base := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend":         "ollama",
			"ollama_upstream": upstream.URL,
		})

		cases := []struct {
			name string
			body string
			want []map[string]string
		}{
			{
				name: "array",
				body: `{"model":"gpt-4o","instructions":"sys","input":[` +
					`{"role":"user","content":"a"},` +
					`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"b"}]},` +
					`{"type":"function_call_output","call_id":"c1","output":"42"},` +
					`{"type":"input_image","image_url":"x"}]}`,
				want: []map[string]string{
					{"role": "system", "content": "sys"},
					{"role": "user", "content": "a"},
					{"role": "assistant", "content": "b"},
					{"role": "tool", "content": "42"},
				},
			},
			{
				name: "string",
				body: `{"model":"gpt-4o","input":"hi"}`,
				want: []map[string]string{{"role": "user", "content": "hi"}},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resp, body := postResponses(t, base, tc.body)
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status %d: %s", resp.StatusCode, body)
				}
				var p responsesPayload
				mustJSONUnmarshal(t, body, &p)
				if p.Output[0].Content[0].Text != "upstream-reply" {
					t.Fatalf("output: %s", body)
				}
				mu.Lock()
				defer mu.Unlock()
				if !reflect.DeepEqual(last, tc.want) {
					t.Fatalf("upstream messages: got %v, want %v", last, tc.want)
				}
			})
		}
	})
}

func TestE2E_ResponsesHTTPFixtures(t *testing.T) {
	repoRoot := repoRoot(t)
	fixturesDir := t.TempDir()
	writeResponsesHTTPFixture(t, fixturesDir, "fx-ok", `[
  {"type":"response.created","sequence_number":0,"response":{"id":"resp_fx","status":"in_progress","output":[]}},
  {"type":"response.completed","sequence_number":1,"response":{"id":"resp_fx","object":"response","status":"completed","model":"fixture-model","output":[{"type":"message","id":"msg_fx","role":"assistant","status":"completed","content":[{"type":"output_text","text":"from fixture","annotations":[]}]}]}}
]`)
	writeResponsesHTTPFixture(t, fixturesDir, "fx-bad", `[{"type":"response.created","sequence_number":0,"response":{"id":"resp_bad","status":"in_progress","output":[]}}]`)
	yaml := `provider: openai
version: v1-responses
fixtures:
  - expression: 'body["input"] == "use-ok"'
    fixture: fx-ok
  - expression: 'body["input"] == "use-bad"'
    fixture: fx-bad
`
	if err := os.WriteFile(filepath.Join(fixturesDir, "fixtures.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write fixtures.yaml: %v", err)
	}
	admin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
	t.Cleanup(admin.Close)
	base := createRuntimeListener(t, admin, "openai", map[string]any{"backend": "fixture"})

	t.Run("fixture", func(t *testing.T) {
		resp, body := postResponses(t, base, `{"model":"gpt-4o","input":"use-ok"}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		var p responsesPayload
		mustJSONUnmarshal(t, body, &p)
		if p.ID != "resp_fx" || p.Model != "gpt-4o" || p.Output[0].Content[0].Text != "from fixture" {
			t.Fatalf("fixture response: %s", body)
		}
	})

	t.Run("fixture_missing_completed", func(t *testing.T) {
		resp, body := postResponses(t, base, `{"model":"gpt-4o","input":"use-bad"}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway || resp.Header.Get("X-Zolem-Error") != "true" {
			t.Fatalf("status %d header %q: %s", resp.StatusCode, resp.Header.Get("X-Zolem-Error"), body)
		}
		if !strings.Contains(string(body), "has no response.completed event") {
			t.Fatalf("body: %s", body)
		}
	})
}

func writeResponsesHTTPFixture(t *testing.T, root, id, events string) {
	t.Helper()
	dir := filepath.Join(root, id)
	mustMkdir(t, dir)
	meta := "id: " + id + "\nprovider: openai\nversion: v1-responses\nstatus: 200\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(events), 0o644); err != nil {
		t.Fatalf("write response.json: %v", err)
	}
}
