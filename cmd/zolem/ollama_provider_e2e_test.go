package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ndjsonObject is the subset of a native /api/chat response object the E2E
// assertions inspect.
type ndjsonObject struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason"`

	TotalDuration   int64 `json:"total_duration"`
	PromptEvalCount int   `json:"prompt_eval_count"`
	EvalCount       int   `json:"eval_count"`
}

// assertNDJSONStreamShape pins the native streaming contract over real HTTP:
// wantDeltas content objects followed by exactly one terminal object, with
// none of SSE's framing.
func assertNDJSONStreamShape(t *testing.T, raw []byte, wantDeltas int) {
	t.Helper()
	body := string(raw)

	if strings.Contains(body, "data: ") {
		t.Fatalf("NDJSON must not carry the SSE 'data: ' prefix:\n%s", body)
	}
	if strings.Contains(body, "[DONE]") {
		t.Fatalf("NDJSON must not carry an SSE [DONE] sentinel:\n%s", body)
	}
	if !strings.HasSuffix(body, "\n") {
		t.Fatalf("NDJSON must end with a newline: %q", body)
	}

	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != wantDeltas+1 {
		t.Fatalf("object count: got %d, want %d (%d deltas + 1 terminal)\n%s",
			len(lines), wantDeltas+1, wantDeltas, body)
	}

	for i, line := range lines[:len(lines)-1] {
		var obj ndjsonObject
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("delta %d is not a bare JSON object: %v (%q)", i, err, line)
		}
		if obj.Done {
			t.Errorf("delta %d must have done=false: %s", i, line)
		}
		if obj.EvalCount != 0 {
			t.Errorf("delta %d must not carry metrics: %s", i, line)
		}
	}

	final := lines[len(lines)-1]
	var last ndjsonObject
	if err := json.Unmarshal([]byte(final), &last); err != nil {
		t.Fatalf("terminal object: %v (%q)", err, final)
	}
	if !last.Done {
		t.Errorf("terminal object must have done=true: %s", final)
	}
	if last.DoneReason != "stop" {
		t.Errorf("terminal done_reason: got %q, want stop", last.DoneReason)
	}
	if last.EvalCount != wantDeltas {
		t.Errorf("eval_count: got %d, want %d", last.EvalCount, wantDeltas)
	}
	if last.PromptEvalCount <= 0 {
		t.Errorf("prompt_eval_count: got %d, want > 0", last.PromptEvalCount)
	}
	if last.TotalDuration <= 0 {
		t.Errorf("total_duration: got %d, want > 0", last.TotalDuration)
	}
}

func TestLocalRuntimeOllamaProvider_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	listenerBaseURL := createRuntimeListener(t, admin, "ollama", map[string]any{
		"backend": "lorem",
	})

	// Ollama is unauthenticated: every request below deliberately omits any
	// Authorization or x-api-key header.

	t.Run("non-streaming", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/api/chat",
			`{"model":"llama3.2","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("content-type: got %q, want application/json", ct)
		}
		if strings.Contains(strings.TrimSpace(string(body)), "\n") {
			t.Errorf("non-streaming body must be one object:\n%s", body)
		}

		var obj ndjsonObject
		if err := json.Unmarshal([]byte(body), &obj); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if !obj.Done || obj.DoneReason != "stop" {
			t.Errorf("done=%v done_reason=%q, want true/stop", obj.Done, obj.DoneReason)
		}
		if !strings.Contains(strings.ToLower(obj.Message.Content), "lorem") {
			t.Errorf("lorem backend response lacked a lorem token: %q", obj.Message.Content)
		}
		if obj.EvalCount <= 0 || obj.PromptEvalCount <= 0 {
			t.Errorf("token counters: eval=%d prompt_eval=%d, want both > 0", obj.EvalCount, obj.PromptEvalCount)
		}
	})

	// THE critical case over real HTTP: omitting `stream` must stream, because
	// Ollama defaults it to true. The lorem generator emits 30 tokens.
	t.Run("stream-defaults-to-true-when-absent", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/api/chat",
			`{"model":"llama3.2","messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-ndjson") {
			t.Fatalf("omitting `stream` must stream NDJSON; content-type was %q\n%s", ct, body)
		}
		assertNDJSONStreamShape(t, body, 30)
	})

	t.Run("streaming-explicit", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/api/chat",
			`{"model":"llama3.2","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		assertNDJSONStreamShape(t, body, 30)
	})

	t.Run("tags", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodGet, "/api/tags", "")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		var tags struct {
			Models []struct {
				Name         string   `json:"name"`
				Capabilities []string `json:"capabilities"`
			} `json:"models"`
		}
		if err := json.Unmarshal([]byte(body), &tags); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if len(tags.Models) == 0 {
			t.Fatal("expected a non-empty catalogue")
		}
		for _, m := range tags.Models {
			if len(m.Capabilities) == 0 {
				t.Errorf("model %q missing capabilities", m.Name)
			}
		}
	})

	t.Run("version", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodGet, "/api/version", "")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "version") {
			t.Errorf("expected a version field: %s", body)
		}
	})

	t.Run("show-unknown-model-is-flat-404", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/api/show",
			`{"model":"no-such-model"}`, "Content-Type: application/json")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status: got %d, want 404: %s", resp.StatusCode, body)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if _, isString := env["error"].(string); !isString {
			t.Errorf("error must be a flat string, got %s", body)
		}
	})

	t.Run("schema-rejects-malformed-request", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/api/chat",
			`{"messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("missing model must 400; got %d: %s", resp.StatusCode, body)
		}
	})

	// Fixture bodies must go through template rendering, exactly as they do for
	// the other three providers. Without it a .tmpl fixture is served with its
	// {{ }} actions intact.
	t.Run("templated-fixture-is-rendered", func(t *testing.T) {
		fixturesDir := t.TempDir()
		copyTestdataFixtures(t, repoRoot, fixturesDir)
		fixtureAdmin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
		t.Cleanup(fixtureAdmin.Close)

		fixtureURL := createRuntimeListener(t, fixtureAdmin, "ollama", map[string]any{
			"backend": "fixture",
		})

		// createRuntimeListener names the profile "<provider>-<backend>-demo".
		want := "Templated fixture for profile ollama-fixture-demo."

		t.Run("non-streaming", func(t *testing.T) {
			resp, body := doRequest(t, fixtureURL, http.MethodPost, "/api/chat",
				`{"model":"llama3.2","stream":false,"messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json")
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
			}
			var obj ndjsonObject
			if err := json.Unmarshal(body, &obj); err != nil {
				t.Fatalf("decode: %v (%s)", err, body)
			}
			if obj.Message.Content != want {
				t.Fatalf("rendered fixture content: got %q, want %q", obj.Message.Content, want)
			}
		})

		t.Run("streaming", func(t *testing.T) {
			resp, body := doRequest(t, fixtureURL, http.MethodPost, "/api/chat",
				`{"model":"llama3.2","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json")
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200: %s", resp.StatusCode, body)
			}

			lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
			var joined strings.Builder
			for _, line := range lines[:len(lines)-1] {
				var obj ndjsonObject
				if err := json.Unmarshal([]byte(line), &obj); err != nil {
					t.Fatalf("decode %q: %v", line, err)
				}
				joined.WriteString(obj.Message.Content)
			}
			if got := joined.String(); got != want {
				t.Fatalf("rendered streamed content: got %q, want %q", got, want)
			}
			// The rendered string is 5 words, so 5 deltas plus the terminal object.
			if len(lines) != 6 {
				t.Fatalf("object count: got %d, want 6\n%s", len(lines), body)
			}
		})
	})

	// Fields a real Ollama accepts must not be rejected by the schema.
	t.Run("schema-accepts-native-optional-fields", func(t *testing.T) {
		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/api/chat",
			`{"model":"llama3.2","stream":false,"messages":[{"role":"user","content":"hi","images":["aGk="]}],`+
				`"options":{"num_ctx":4096,"seed":1},"keep_alive":"10m","format":"json","think":true,"invented_later":1}`,
			"Content-Type: application/json")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("native optional fields must be accepted; got %d: %s", resp.StatusCode, body)
		}
	})
}

// generateObject is the subset of a native /api/generate response object the
// E2E assertions inspect.
type generateObject struct {
	Model           string `json:"model"`
	CreatedAt       string `json:"created_at"`
	Response        string `json:"response"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	Context         []int  `json:"context"`
	TotalDuration   int64  `json:"total_duration"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

// writeOllamaFixture writes one always-matching Ollama fixture directory.
func writeOllamaFixture(t *testing.T, root, id, version, responseJSON string) {
	t.Helper()
	dir := filepath.Join(root, id)
	mustMkdir(t, dir)
	meta := "id: " + id + "\nprovider: ollama\nversion: " + version + "\nstatus: 200\n"
	for name, data := range map[string][]byte{
		"meta.yaml":     []byte(meta),
		"response.json": []byte(responseJSON),
		"match.wasm":    alwaysMatchWASM,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write %s/%s: %v", id, name, err)
		}
	}
}

func decodeNDJSONGenerate(t *testing.T, body []byte) []generateObject {
	t.Helper()
	var objs []generateObject
	for _, line := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
		var obj generateObject
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		objs = append(objs, obj)
	}
	return objs
}

func TestE2E_OllamaGenerate(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)
	base := createRuntimeListener(t, admin, "ollama", map[string]any{"backend": "lorem"})
	hdr := "Content-Type: application/json"

	t.Run("non_stream", func(t *testing.T) {
		resp, body := doRequest(t, base, http.MethodPost, "/api/generate",
			`{"model":"m","prompt":"hi","stream":false}`, hdr)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d: %s", resp.StatusCode, body)
		}
		var obj generateObject
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if !obj.Done || obj.Response == "" || obj.DoneReason != "stop" {
			t.Errorf("done=%v response=%q reason=%q", obj.Done, obj.Response, obj.DoneReason)
		}
		if obj.EvalCount <= 0 || obj.PromptEvalCount <= 0 || obj.TotalDuration <= 0 || len(obj.Context) == 0 {
			t.Errorf("metrics/context missing: %s", body)
		}
	})

	t.Run("stream_default", func(t *testing.T) {
		resp, body := doRequest(t, base, http.MethodPost, "/api/generate", `{"model":"m","prompt":"hi"}`, hdr)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d: %s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-ndjson") {
			t.Fatalf("content-type %q, want NDJSON", ct)
		}
		objs := decodeNDJSONGenerate(t, body)
		last := objs[len(objs)-1]
		if !last.Done || last.Response != "" || last.EvalCount != len(objs)-1 {
			t.Errorf("bad terminal object: %+v", last)
		}
		var joined strings.Builder
		for _, o := range objs[:len(objs)-1] {
			if o.Done {
				t.Errorf("delta with done=true: %+v", o)
			}
			joined.WriteString(o.Response)
		}
		if joined.String() == "" {
			t.Error("concatenated deltas are empty")
		}
	})

	t.Run("empty_prompt_load", func(t *testing.T) {
		resp, body := doRequest(t, base, http.MethodPost, "/api/generate", `{"model":"m"}`, hdr)
		defer resp.Body.Close()
		var obj generateObject
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		if resp.StatusCode != http.StatusOK || !obj.Done || obj.DoneReason != "load" || obj.EvalCount != 0 {
			t.Errorf("status=%d body=%s", resp.StatusCode, body)
		}
	})

	t.Run("empty_prompt_unload", func(t *testing.T) {
		for _, ka := range []string{`0`, `"0"`, `"0s"`} {
			resp, body := doRequest(t, base, http.MethodPost, "/api/generate",
				`{"model":"m","keep_alive":`+ka+`}`, hdr)
			var obj generateObject
			if err := json.Unmarshal(body, &obj); err != nil {
				t.Fatalf("decode: %v (%s)", err, body)
			}
			resp.Body.Close()
			if obj.DoneReason != "unload" {
				t.Errorf("keep_alive %s: done_reason %q, want unload", ka, obj.DoneReason)
			}
		}
	})

	t.Run("missing_model", func(t *testing.T) {
		resp, body := doRequest(t, base, http.MethodPost, "/api/generate", `{"prompt":"hi"}`, hdr)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d: %s", resp.StatusCode, body)
		}
		var env map[string]any
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := env["error"].(string); !ok {
			t.Errorf("error must be a flat string: %s", body)
		}
	})

	t.Run("backend_failure_pre_first_token", func(t *testing.T) {
		failBase := createRuntimeListener(t, admin, "ollama", map[string]any{
			"backend":         "ollama",
			"backend_model":   "x",
			"ollama_upstream": "http://127.0.0.1:1",
		})
		for _, req := range []string{
			`{"model":"m","prompt":"hi","stream":false}`,
			`{"model":"m","prompt":"hi"}`,
		} {
			resp, body := doRequest(t, failBase, http.MethodPost, "/api/generate", req, hdr)
			resp.Body.Close()
			if resp.StatusCode < 400 {
				t.Errorf("%s: status %d, want non-2xx: %s", req, resp.StatusCode, body)
			}
		}
	})

	t.Run("fixtures", func(t *testing.T) {
		fixturesDir := t.TempDir()
		const multiline = "line one\n\n- item\n  code"
		writeOllamaFixture(t, fixturesDir, "gen-fx", "v1-generate",
			`{"model":"fixture-model","created_at":"2026-01-15T10:00:00Z","response":"generate fixture reply","done":true,"done_reason":"stop","context":[9],"eval_count":3}`)
		writeOllamaFixture(t, fixturesDir, "chat-fx", "v1",
			`{"model":"fixture-model","created_at":"2026-01-15T10:00:00Z","message":{"role":"assistant","content":"chat fixture reply"},"done":true,"done_reason":"stop"}`)
		fxAdmin := startLocalAdminServiceWithFixtures(t, repoRoot, fixturesDir)
		t.Cleanup(fxAdmin.Close)
		fx := createRuntimeListener(t, fxAdmin, "ollama", map[string]any{"backend": "fixture"})

		t.Run("fixture", func(t *testing.T) {
			resp, body := doRequest(t, fx, http.MethodPost, "/api/generate",
				`{"model":"m","prompt":"hi","stream":false}`, hdr)
			defer resp.Body.Close()
			var got, want map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode: %v (%s)", err, body)
			}
			_ = json.Unmarshal([]byte(`{"model":"m","created_at":"2026-01-15T10:00:00Z","response":"generate fixture reply","done":true,"done_reason":"stop","context":[9],"eval_count":3}`), &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body %s, want %v", body, want)
			}
		})

		t.Run("fixture_version_isolation", func(t *testing.T) {
			_, gen := doRequest(t, fx, http.MethodPost, "/api/generate", `{"model":"m","prompt":"hi","stream":false}`, hdr)
			_, chat := doRequest(t, fx, http.MethodPost, "/api/chat",
				`{"model":"m","stream":false,"messages":[{"role":"user","content":"hi"}]}`, hdr)
			if !strings.Contains(string(gen), "generate fixture reply") || strings.Contains(string(gen), "chat fixture") {
				t.Errorf("generate served wrong fixture: %s", gen)
			}
			if !strings.Contains(string(chat), "chat fixture reply") || strings.Contains(string(chat), "generate fixture") {
				t.Errorf("chat served wrong fixture: %s", chat)
			}
		})

		t.Run("fixture_stream", func(t *testing.T) {
			resp, body := doRequest(t, fx, http.MethodPost, "/api/generate", `{"model":"m","prompt":"hi"}`, hdr)
			defer resp.Body.Close()
			objs := decodeNDJSONGenerate(t, body)
			var joined strings.Builder
			for _, o := range objs[:len(objs)-1] {
				joined.WriteString(o.Response)
			}
			if !objs[len(objs)-1].Done {
				t.Errorf("stream must end in done:true: %s", body)
			}
			if got := joined.String(); got != "generate fixture reply" {
				t.Errorf("streamed %q", got)
			}
		})

		t.Run("fixture_stream_multiline", func(t *testing.T) {
			t.Skip("blocked on zolem-jmk (tokenizer must preserve whitespace)")
			mlDir := t.TempDir()
			writeOllamaFixture(t, mlDir, "gen-ml", "v1-generate",
				`{"model":"x","response":`+strconvQuote(multiline)+`,"done":true}`)
			mlAdmin := startLocalAdminServiceWithFixtures(t, repoRoot, mlDir)
			t.Cleanup(mlAdmin.Close)
			ml := createRuntimeListener(t, mlAdmin, "ollama", map[string]any{"backend": "fixture"})
			resp, body := doRequest(t, ml, http.MethodPost, "/api/generate", `{"model":"m","prompt":"hi"}`, hdr)
			defer resp.Body.Close()
			objs := decodeNDJSONGenerate(t, body)
			var joined strings.Builder
			for _, o := range objs {
				joined.WriteString(o.Response)
			}
			if joined.String() != multiline {
				t.Errorf("streamed %q, want %q", joined.String(), multiline)
			}
		})
	})
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
