package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// verbatimCase describes one provider's fixed-listener fixture set: an error
// fixture, a 200 fixture carrying fields the provider's response struct does
// not model, and the request shape that selects each.
type verbatimCase struct {
	provider    string
	version     string
	path        string
	headers     []string
	errStatus   int
	errBody     string
	okBody      string
	modelKey    string
	reqModel    string
	matchExpr   string // CEL selecting the error fixture
	requestBody func(marker string) string
}

func verbatimCases() []verbatimCase {
	return []verbatimCase{
		{
			provider:  "openai",
			version:   "v1",
			path:      "/v1/chat/completions",
			headers:   []string{"Authorization: Bearer sk-test"},
			errStatus: 429,
			errBody:   `{"error":{"message":"Rate limit reached for gpt-4o","type":"requests","param":null,"code":"rate_limit_exceeded"}}`,
			okBody: `{"id":"chatcmpl-fix","object":"chat.completion","created":1,"model":"x","system_fingerprint":"fp_abc",` +
				`"choices":[{"index":0,"message":{"role":"assistant","content":"fixture ok","refusal":null},"logprobs":null,"finish_reason":"stop"}],` +
				`"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
			modelKey:  "model",
			reqModel:  "gpt-4o",
			matchExpr: `body["messages"][0]["content"] == "ratelimit"`,
			requestBody: func(m string) string {
				return `{"model":"gpt-4o","messages":[{"role":"user","content":"` + m + `"}]}`
			},
		},
		{
			provider:  "anthropic",
			version:   "v1",
			path:      "/v1/messages",
			headers:   []string{"x-api-key: sk-test"},
			errStatus: 529,
			errBody:   `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			okBody: `{"id":"msg_fix","type":"message","role":"assistant","model":"x",` +
				`"content":[{"type":"thinking","thinking":"let me think","signature":"sig123"},{"type":"text","text":"fixture ok"}],` +
				`"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":7}}`,
			modelKey:  "model",
			reqModel:  "claude-3-5-sonnet-20241022",
			matchExpr: `body["messages"][0]["content"] == "ratelimit"`,
			requestBody: func(m string) string {
				return `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"` + m + `"}]}`
			},
		},
		{
			provider:  "gemini",
			version:   "v1beta",
			path:      "/v1beta/models/gemini-2.0-flash:generateContent",
			headers:   []string{"x-goog-api-key: test-key"},
			errStatus: 429,
			errBody:   `{"error":{"code":429,"message":"Resource exhausted","status":"RESOURCE_EXHAUSTED"}}`,
			okBody: `{"candidates":[{"content":{"role":"model","parts":[{"text":"fixture ok"}]},"finishReason":"STOP","index":0,` +
				`"safetyRatings":[{"category":"HARM_CATEGORY_HARASSMENT","probability":"NEGLIGIBLE"}]}],` +
				`"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3},` +
				`"modelVersion":"x","responseId":"resp-abc"}`,
			modelKey:  "modelVersion",
			reqModel:  "gemini-2.0-flash",
			matchExpr: `body["contents"][0]["parts"][0]["text"] == "ratelimit"`,
			requestBody: func(m string) string {
				return `{"contents":[{"role":"user","parts":[{"text":"` + m + `"}]}]}`
			},
		},
		{
			provider:  "ollama",
			version:   "v1",
			path:      "/api/chat",
			errStatus: 429,
			errBody:   `{"error":"rate limit exceeded"}`,
			okBody: `{"model":"x","created_at":"2024-01-01T00:00:00Z","message":{"role":"assistant","content":"fixture ok"},` +
				`"done":true,"done_reason":"stop","extra_key":{"kept":true}}`,
			modelKey:  "model",
			reqModel:  "llama3.2",
			matchExpr: `body["messages"][0]["content"] == "ratelimit"`,
			requestBody: func(m string) string {
				return `{"model":"llama3.2","stream":false,"messages":[{"role":"user","content":"` + m + `"}]}`
			},
		},
	}
}

func writeVerbatimNamespace(t *testing.T, c verbatimCase, extra map[string]verbatimFixture) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixtures")
	mustMkdir(t, root)
	fixtures := map[string]verbatimFixture{
		"rl": {status: c.errStatus, body: c.errBody},
		"ok": {status: 200, body: c.okBody},
	}
	for k, v := range extra {
		fixtures[k] = v
	}
	for id, f := range fixtures {
		dir := filepath.Join(root, id)
		mustMkdir(t, dir)
		meta := "id: " + id + "\nprovider: " + c.provider + "\nversion: " + c.version + "\nstatus: " + strconv.Itoa(f.status) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	yaml := "provider: " + c.provider + "\nversion: " + c.version + "\nfixtures:\n" +
		"  - expression: '" + c.matchExpr + "'\n    fixture: rl\n" +
		"  - expression: 'true'\n    fixture: ok\n"
	if err := os.WriteFile(filepath.Join(root, "fixtures.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

type verbatimFixture struct {
	status int
	body   string
}

func jsonMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return m
}

func TestE2E_FixtureBodiesServedVerbatim(t *testing.T) {
	for _, c := range verbatimCases() {
		t.Run(c.provider, func(t *testing.T) {
			dir := writeVerbatimNamespace(t, c, nil)
			svc := startProviderService(t, c.provider, "-local-backend", "fixture", "-local-fixtures-dir", dir)

			t.Run("error_envelope_verbatim", func(t *testing.T) {
				resp, body := doRequest(t, svc.baseURL, http.MethodPost, c.path, c.requestBody("ratelimit"),
					append([]string{"Content-Type: application/json"}, c.headers...)...)
				if resp.StatusCode != c.errStatus {
					t.Fatalf("status: got %d, want %d: %s", resp.StatusCode, c.errStatus, body)
				}
				if !bytes.Equal(bytes.TrimSpace(body), []byte(c.errBody)) {
					t.Fatalf("error body altered:\n got: %s\nwant: %s", body, c.errBody)
				}
			})

			t.Run("extra_fields_kept_model_patched", func(t *testing.T) {
				resp, body := doRequest(t, svc.baseURL, http.MethodPost, c.path, c.requestBody("hello"),
					append([]string{"Content-Type: application/json"}, c.headers...)...)
				if resp.StatusCode != 200 {
					t.Fatalf("status: got %d: %s", resp.StatusCode, body)
				}
				got, want := jsonMap(t, body), jsonMap(t, []byte(c.okBody))
				if got[c.modelKey] != c.reqModel {
					t.Fatalf("%s: got %v, want %q", c.modelKey, got[c.modelKey], c.reqModel)
				}
				want[c.modelKey] = c.reqModel
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("200 body altered beyond model:\n got: %s\nwant: %v", body, want)
				}
			})
		})
	}

	t.Run("no_model_key_unpatched", func(t *testing.T) {
		c := verbatimCases()[0]
		noModel := `{"id":"x","object":"chat.completion","choices":[]}`
		dir := writeVerbatimNamespace(t, c, map[string]verbatimFixture{"ok": {status: 200, body: noModel}})
		svc := startProviderService(t, "openai", "-local-backend", "fixture", "-local-fixtures-dir", dir)
		resp, body := doRequest(t, svc.baseURL, http.MethodPost, c.path, c.requestBody("hello"),
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		if resp.StatusCode != 200 {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if !bytes.Equal(bytes.TrimSpace(body), []byte(noModel)) {
			t.Fatalf("body not byte-equal:\n got: %s\nwant: %s", body, noModel)
		}
	})
}
