package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ketang/zolem/internal/admincli"
)

// Each binary is compiled once per test process (with the normal build cache)
// instead of paying a full `go run` rebuild on every invocation. The previous
// per-call `go run` with a fresh GOCACHE dominated the package wall time.
var (
	zolemcBinOnce sync.Once
	zolemcBinPath string
	zolemcBinErr  error

	zolemBinOnce sync.Once
	zolemBinPath string
	zolemBinErr  error
)

func buildZolemcBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	zolemcBinOnce.Do(func() {
		zolemcBinPath, zolemcBinErr = buildBinary(repoRoot, "./cmd/zolemc", "zolemc-e2e-test-bin")
	})
	if zolemcBinErr != nil {
		t.Fatalf("build zolemc binary: %v", zolemcBinErr)
	}
	return zolemcBinPath
}

func buildZolemBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	zolemBinOnce.Do(func() {
		zolemBinPath, zolemBinErr = buildBinary(repoRoot, "./cmd/zolem", "zolem-e2e-zolemc-bin")
	})
	if zolemBinErr != nil {
		t.Fatalf("build zolem binary: %v", zolemBinErr)
	}
	return zolemBinPath
}

func buildBinary(repoRoot, pkg, outName string) (string, error) {
	outPath := filepath.Join(os.TempDir(), outName)
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", outPath, pkg)
	cmd.Dir = repoRoot
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build %s: %w\n%s", pkg, err, out)
	}
	return outPath, nil
}

func TestZolemcLocalRuntimeE2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "health")
	missing := runZolemcFail(t, repoRoot, "-admin-url", admin.baseURL, "profiles", "delete", "missing")
	if !strings.Contains(missing.stderr, "404") || !strings.Contains(missing.stderr, `"error":"profile not found"`) {
		t.Fatalf("missing profile error did not include status and body:\n%s", missing.stderr)
	}

	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "profiles", "create", "demo", "-backend", "lorem")

	profiles := runZolemc(t, repoRoot, "-json", "-admin-url", admin.baseURL, "profiles", "list")
	if !strings.Contains(profiles.stdout, `"name":"demo"`) {
		t.Fatalf("profiles list did not include demo:\n%s", profiles.stdout)
	}

	listener := runZolemc(t, repoRoot, "-json", "-admin-url", admin.baseURL, "listeners", "create", "openai-demo", "-addr", "127.0.0.1:0", "-provider", "openai", "-profile", "demo")
	var listenerPayload struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal([]byte(listener.stdout), &listenerPayload); err != nil {
		t.Fatalf("decode listener JSON: %v\n%s", err, listener.stdout)
	}
	if listenerPayload.BaseURL == "" {
		t.Fatalf("listener base_url missing:\n%s", listener.stdout)
	}

	runZolemc(t, repoRoot, "-base-url", listenerPayload.BaseURL, "listener", "health")
	state := runZolemc(t, repoRoot, "-json", "-base-url", listenerPayload.BaseURL, "listener", "state")
	if !strings.Contains(state.stdout, `"provider":"openai"`) {
		t.Fatalf("listener state did not include provider:\n%s", state.stdout)
	}

	request := runZolemc(t, repoRoot, "-base-url", listenerPayload.BaseURL, "request", "-method", "POST", "-path", "/v1/chat/completions", "-H", "Authorization: Bearer sk-test", "-json-body", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	var completion struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(request.stdout), &completion); err != nil {
		t.Fatalf("decode completion response: %v\n%s", err, request.stdout)
	}
	if len(completion.Choices) == 0 || completion.Choices[0].Message.Role != "assistant" || completion.Choices[0].Message.Content == "" {
		t.Fatalf("request output did not include assistant content:\n%#v", completion)
	}

	bodyFile := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(bodyFile, []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"from file"}]}`), 0o644); err != nil {
		t.Fatalf("write request body file: %v", err)
	}
	runZolemc(t, repoRoot, "-base-url", listenerPayload.BaseURL, "request", "-method", "POST", "-path", "/v1/chat/completions", "-H", "Authorization: Bearer sk-test", "-H", "Content-Type: application/json", "-body-file", bodyFile)

	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "listeners", "delete", "openai-demo")
	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "profiles", "delete", "demo")
}

func TestZolemcCallsE2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "profiles", "create", "demo", "-backend", "lorem")
	listener := runZolemc(t, repoRoot, "-json", "-admin-url", admin.baseURL, "listeners", "create", "calls-demo", "-addr", "127.0.0.1:0", "-provider", "openai", "-profile", "demo")
	var listenerPayload struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal([]byte(listener.stdout), &listenerPayload); err != nil {
		t.Fatalf("decode listener: %v\n%s", err, listener.stdout)
	}

	for i := 0; i < 3; i++ {
		runZolemc(t, repoRoot, "-base-url", listenerPayload.BaseURL, "request", "-method", "POST", "-path", "/v1/chat/completions", "-H", "Authorization: Bearer sk-test", "-json-body", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	}

	t.Run("calls_list_human", func(t *testing.T) {
		result := runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "listeners", "calls", "list", "calls-demo")
		out := result.stdout
		for _, want := range []string{"ID", "METHOD", "PATH", "STATUS"} {
			if !strings.Contains(out, want) {
				t.Fatalf("human output missing %q:\n%s", want, out)
			}
		}
		if !strings.Contains(out, "POST") || !strings.Contains(out, "/v1/chat/completions") || !strings.Contains(out, "200") {
			t.Fatalf("human output missing call data:\n%s", out)
		}
	})

	t.Run("calls_list_json", func(t *testing.T) {
		result := runZolemc(t, repoRoot, "-json", "-admin-url", admin.baseURL, "listeners", "calls", "list", "calls-demo")
		var envelope struct {
			Calls []struct {
				CallID int64 `json:"call_id"`
			} `json:"calls"`
		}
		if err := json.Unmarshal([]byte(result.stdout), &envelope); err != nil {
			t.Fatalf("decode JSON: %v\n%s", err, result.stdout)
		}
		if len(envelope.Calls) != 3 {
			t.Fatalf("expected 3 calls, got %d", len(envelope.Calls))
		}
	})

	t.Run("calls_list_since", func(t *testing.T) {
		result := runZolemc(t, repoRoot, "-json", "-admin-url", admin.baseURL, "listeners", "calls", "list", "calls-demo", "-since", "2")
		var envelope struct {
			Calls []struct {
				CallID int64 `json:"call_id"`
			} `json:"calls"`
		}
		if err := json.Unmarshal([]byte(result.stdout), &envelope); err != nil {
			t.Fatalf("decode JSON: %v\n%s", err, result.stdout)
		}
		if len(envelope.Calls) != 1 || envelope.Calls[0].CallID != 3 {
			t.Fatalf("-since 2 should return only call_id=3, got %v", envelope.Calls)
		}
	})

	t.Run("calls_clear", func(t *testing.T) {
		result := runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "listeners", "calls", "clear", "calls-demo")
		if !strings.Contains(result.stdout, "cleared 3 calls") {
			t.Fatalf("clear output: %q", result.stdout)
		}
	})

	t.Run("calls_clear_json", func(t *testing.T) {
		runZolemc(t, repoRoot, "-base-url", listenerPayload.BaseURL, "request", "-method", "POST", "-path", "/v1/chat/completions", "-H", "Authorization: Bearer sk-test", "-json-body", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
		result := runZolemc(t, repoRoot, "-json", "-admin-url", admin.baseURL, "listeners", "calls", "clear", "calls-demo")
		if strings.TrimSpace(result.stdout) != `{"cleared":1}` {
			t.Fatalf("clear -json output: %q", result.stdout)
		}
	})

	t.Run("nonexistent_listener", func(t *testing.T) {
		result := runZolemcFail(t, repoRoot, "-admin-url", admin.baseURL, "listeners", "calls", "list", "no-such-listener")
		if !strings.Contains(result.stderr, "zolem:") && !strings.Contains(result.stderr, "404") {
			t.Fatalf("expected zolem: prefixed error or 404, got:\n%s", result.stderr)
		}
	})
}

func TestProfilesCreateSendsWASMFields(t *testing.T) {
	wasmPath := filepath.Join(t.TempDir(), "generator.wasm")
	wasmBytes := []byte("test wasm module")
	if err := os.WriteFile(wasmPath, wasmBytes, 0o644); err != nil {
		t.Fatalf("write wasm module: %v", err)
	}

	var gotPath string
	var gotPayload map[string]any
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPut {
			t.Fatalf("method: got %s, want PUT", req.Method)
		}
		gotPath = req.URL.Path
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"name":"wasm-demo","backend":"wasm"}`)
	}))
	t.Cleanup(admin.Close)

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"-admin-url", admin.URL,
		"profiles", "create", "wasm-demo",
		"-wasm-module-file", wasmPath,
		"-wasm-timeout-ms", "250",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("profiles create failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if gotPath != "/_zolem/profiles/wasm-demo" {
		t.Fatalf("path: got %q", gotPath)
	}
	if gotPayload["backend"] != "wasm" {
		t.Fatalf("backend: got %#v, want wasm", gotPayload["backend"])
	}
	if gotPayload["wasm_module_base64"] != "dGVzdCB3YXNtIG1vZHVsZQ==" {
		t.Fatalf("wasm_module_base64: got %#v", gotPayload["wasm_module_base64"])
	}
	if gotPayload["wasm_generate_timeout_ms"] != float64(250) {
		t.Fatalf("wasm_generate_timeout_ms: got %#v", gotPayload["wasm_generate_timeout_ms"])
	}
}

func TestListenersCallsList(t *testing.T) {
	respBody := `{"calls":[` +
		`{"call_id":1,"received_at":"2026-05-22T16:34:12Z","latency_ms":12,"request":{"method":"POST","path":"/v1/chat/completions"},"response":{"status":200,"stream":null}},` +
		`{"call_id":2,"received_at":"2026-05-22T16:34:14Z","latency_ms":8,"request":{"method":"POST","path":"/v1/chat/completions"},"response":{"status":200,"stream":{"event_count":3}}},` +
		`{"call_id":3,"received_at":"2026-05-22T16:34:18Z","latency_ms":4,"request":{"method":"GET","path":"/v1/models"},"response":{"status":404,"stream":null}}` +
		`]}`
	var gotPath, gotMethod string
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotMethod = req.Method
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respBody)
	}))
	t.Cleanup(admin.Close)

	// Human table output.
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "listeners", "calls", "list", "openai-demo"}, &stdout, &stderr); err != nil {
		t.Fatalf("list failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if gotMethod != http.MethodGet || gotPath != "/_zolem/listeners/openai-demo/calls" {
		t.Fatalf("request: got %s %s", gotMethod, gotPath)
	}
	out := stdout.String()
	for _, want := range []string{"ID", "METHOD", "PATH", "STATUS", "LATENCY_MS", "RECEIVED_AT", "200", "~200", "404", "POST", "GET"} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}

	// JSON passthrough.
	stdout.Reset()
	stderr.Reset()
	if err := run(context.Background(), []string{"-json", "-admin-url", admin.URL, "listeners", "calls", "list", "openai-demo"}, &stdout, &stderr); err != nil {
		t.Fatalf("list -json failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `"calls"`) || !strings.Contains(stdout.String(), `"call_id":1`) {
		t.Fatalf("json output missing fields:\n%s", stdout.String())
	}

	// -since filter (human).
	stdout.Reset()
	stderr.Reset()
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "listeners", "calls", "list", "openai-demo", "-since", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("list -since failed: %v", err)
	}
	out = stdout.String()
	if strings.Contains(out, "POST    /v1/chat/completions") && strings.Count(out, "POST") != 1 {
		// expect only call_id 2 to remain (POST/streaming)
	}
	if !strings.Contains(out, "~200") || strings.Contains(out, "  1  ") {
		t.Fatalf("-since 1 should filter to call_id>1:\n%s", out)
	}

	// -since filter (json) keeps wrapping shape with filtered list.
	stdout.Reset()
	stderr.Reset()
	if err := run(context.Background(), []string{"-json", "-admin-url", admin.URL, "listeners", "calls", "list", "openai-demo", "-since", "2"}, &stdout, &stderr); err != nil {
		t.Fatalf("list -json -since failed: %v", err)
	}
	var parsed struct {
		Calls []struct {
			CallID int64 `json:"call_id"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("decode -json -since: %v\n%s", err, stdout.String())
	}
	if len(parsed.Calls) != 1 || parsed.Calls[0].CallID != 3 {
		t.Fatalf("-since 2 filter: got %#v", parsed.Calls)
	}
}

// TestJSONOutputIsFieldComplete verifies that -json output passes through every
// field the admin API returned, including fields the narrow human-readable view
// structs do not model (e.g. stream_delay, ollama_upstream). Re-marshaling
// through the view structs would silently drop them.
func TestJSONOutputIsFieldComplete(t *testing.T) {
	profileJSON := `{"name":"demo","backend":"ollama","stream_delay":"250ms","ollama_upstream":"http://127.0.0.1:11434","seed":7}`
	listenerJSON := `{"name":"openai-demo","provider":"openai","profile":"demo","backend":"ollama","base_url":"http://127.0.0.1:9000","extra_field":"keep-me"}`
	stateJSON := `{"provider":"openai","profile":"demo","backend":"ollama","listener":"openai-demo","tls":false,"stream_delay":"250ms"}`
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/_zolem/profiles":
			fmt.Fprintf(w, "[%s]", profileJSON)
		case req.Method == http.MethodPut && req.URL.Path == "/_zolem/profiles/demo":
			fmt.Fprint(w, profileJSON)
		case req.Method == http.MethodGet && req.URL.Path == "/_zolem/listeners":
			fmt.Fprintf(w, "[%s]", listenerJSON)
		case req.Method == http.MethodPut && req.URL.Path == "/_zolem/listeners/openai-demo":
			fmt.Fprint(w, listenerJSON)
		case req.Method == http.MethodGet && req.URL.Path == "/_zolem/state":
			fmt.Fprint(w, stateJSON)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(admin.Close)

	cases := []struct {
		name        string
		args        []string
		mustContain []string
	}{
		{
			name:        "profiles list",
			args:        []string{"-json", "-admin-url", admin.URL, "profiles", "list"},
			mustContain: []string{`"stream_delay":"250ms"`, `"ollama_upstream":"http://127.0.0.1:11434"`, `"name":"demo"`},
		},
		{
			name:        "profiles create",
			args:        []string{"-json", "-admin-url", admin.URL, "profiles", "create", "demo", "-backend", "ollama"},
			mustContain: []string{`"stream_delay":"250ms"`, `"ollama_upstream":"http://127.0.0.1:11434"`},
		},
		{
			name:        "listeners list",
			args:        []string{"-json", "-admin-url", admin.URL, "listeners", "list"},
			mustContain: []string{`"extra_field":"keep-me"`, `"base_url":"http://127.0.0.1:9000"`},
		},
		{
			name:        "listeners create",
			args:        []string{"-json", "-admin-url", admin.URL, "listeners", "create", "openai-demo", "-provider", "openai", "-profile", "demo"},
			mustContain: []string{`"extra_field":"keep-me"`, `"base_url":"http://127.0.0.1:9000"`},
		},
		{
			name:        "listener state",
			args:        []string{"-json", "-admin-url", admin.URL, "-base-url", admin.URL, "listener", "state"},
			mustContain: []string{`"stream_delay":"250ms"`, `"listener":"openai-demo"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("%s -json failed: %v\nstderr:\n%s", tc.name, err, stderr.String())
			}
			// Output must be exactly one JSON document on one line.
			if !json.Valid(bytes.TrimSpace(stdout.Bytes())) {
				t.Fatalf("%s output is not valid JSON:\n%s", tc.name, stdout.String())
			}
			for _, want := range tc.mustContain {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("%s -json dropped field %q:\n%s", tc.name, want, stdout.String())
				}
			}
		})
	}
}

// TestCallsListJSONSinceShapeParity verifies that "calls list -json -since N"
// produces the same record shape as "calls list -json" (every field of each
// retained call, including request/response headers and bodies), differing only
// by which calls survive the filter. The pre-fix code re-marshaled the -since
// path through recordedCallView, keeping only id/method/path/status.
func TestCallsListJSONSinceShapeParity(t *testing.T) {
	call := func(id int64) string {
		return fmt.Sprintf(`{"call_id":%d,"received_at":"2026-05-22T16:34:1%dZ","latency_ms":%d,`+
			`"request":{"method":"POST","path":"/v1/chat/completions","headers":{"Authorization":"Bearer sk-test"},"body":"{\"model\":\"gpt-4\"}"},`+
			`"response":{"status":200,"headers":{"Content-Type":"application/json"},"body":"{\"id\":\"resp-%d\"}","stream":null}}`, id, id, id, id)
	}
	respBody := fmt.Sprintf(`{"calls":[%s,%s,%s]}`, call(1), call(2), call(3))
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respBody)
	}))
	t.Cleanup(admin.Close)

	decodeCalls := func(t *testing.T, args ...string) []map[string]any {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), args, &stdout, &stderr); err != nil {
			t.Fatalf("run %v failed: %v\nstderr:\n%s", args, err, stderr.String())
		}
		var env struct {
			Calls []map[string]any `json:"calls"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("decode %v output: %v\n%s", args, err, stdout.String())
		}
		return env.Calls
	}

	full := decodeCalls(t, "-json", "-admin-url", admin.URL, "listeners", "calls", "list", "openai-demo")
	if len(full) != 3 {
		t.Fatalf("unfiltered -json: got %d calls, want 3", len(full))
	}

	filtered := decodeCalls(t, "-json", "-admin-url", admin.URL, "listeners", "calls", "list", "openai-demo", "-since", "2")
	if len(filtered) != 1 {
		t.Fatalf("-since 2 -json: got %d calls, want 1", len(filtered))
	}

	// The single retained call (call_id=3) must be byte-identical in both the
	// filtered and unfiltered outputs: same keys, same nested headers/bodies.
	want, err := json.Marshal(full[2])
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(filtered[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Fatalf("-since dropped fields from retained call:\n with -since: %s\nwithout -since: %s", got, want)
	}

	// Sanity: the complete record carries headers and bodies the narrow view
	// would have dropped.
	for _, key := range []string{"request", "response"} {
		sub, ok := filtered[0][key].(map[string]any)
		if !ok {
			t.Fatalf("retained call missing %q object: %#v", key, filtered[0])
		}
		if _, ok := sub["headers"]; !ok {
			t.Fatalf("retained call %q dropped headers: %#v", key, sub)
		}
		if _, ok := sub["body"]; !ok {
			t.Fatalf("retained call %q dropped body: %#v", key, sub)
		}
	}
}

// TestCallsListJSONSinceNullBody guards against a nil-map panic: a JSON null
// body unmarshals into a nil map, and filtering must not crash assigning to it.
func TestCallsListJSONSinceNullBody(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `null`)
	}))
	t.Cleanup(admin.Close)

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-json", "-admin-url", admin.URL, "listeners", "calls", "list", "openai-demo", "-since", "1"}, &stdout, &stderr); err != nil {
		t.Fatalf("calls list -json -since with null body failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if !json.Valid(bytes.TrimSpace(stdout.Bytes())) {
		t.Fatalf("output is not valid JSON:\n%s", stdout.String())
	}
	var env struct {
		Calls []json.RawMessage `json:"calls"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if len(env.Calls) != 0 {
		t.Fatalf("null body should yield zero calls, got %d", len(env.Calls))
	}
}

func TestListenersCallsListEmpty(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"calls":[]}`)
	}))
	t.Cleanup(admin.Close)

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "listeners", "calls", "list", "openai-demo"}, &stdout, &stderr); err != nil {
		t.Fatalf("list empty failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "no calls" {
		t.Fatalf("empty human output: got %q, want \"no calls\"", stdout.String())
	}
}

func TestListenersCallsListMissingName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"-admin-url", "http://127.0.0.1:1", "listeners", "calls", "list"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("list without name unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "calls list requires exactly one listener name") {
		t.Fatalf("error: got %q", err)
	}
}

func TestListenersCallsClear(t *testing.T) {
	var gotMethod, gotPath string
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"cleared":7}`)
	}))
	t.Cleanup(admin.Close)

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "listeners", "calls", "clear", "openai-demo"}, &stdout, &stderr); err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/_zolem/listeners/openai-demo/calls" {
		t.Fatalf("request: got %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(stdout.String(), "cleared 7 calls from listener openai-demo") {
		t.Fatalf("clear human output: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := run(context.Background(), []string{"-json", "-admin-url", admin.URL, "listeners", "calls", "clear", "openai-demo"}, &stdout, &stderr); err != nil {
		t.Fatalf("clear -json failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != `{"cleared":7}` {
		t.Fatalf("clear json output: %q", stdout.String())
	}
}

func TestListenersCallsClearMissingName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"-admin-url", "http://127.0.0.1:1", "listeners", "calls", "clear"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("clear without name unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "calls clear requires exactly one listener name") {
		t.Fatalf("error: got %q", err)
	}
}

func TestProfilesCreateRejectsWASMFieldsWithNonWASMBackend(t *testing.T) {
	wasmPath := filepath.Join(t.TempDir(), "generator.wasm")
	if err := os.WriteFile(wasmPath, []byte("test wasm module"), 0o644); err != nil {
		t.Fatalf("write wasm module: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"-admin-url", "http://127.0.0.1:1",
		"profiles", "create", "bad-demo",
		"-backend", "lorem",
		"-wasm-module-file", wasmPath,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("profiles create unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(err.Error(), "-wasm-module-file requires -backend wasm") {
		t.Fatalf("error: got %q", err)
	}
}

func TestRunRootUsageAndUnknownCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing", args: nil, want: "missing command"},
		{name: "unknown", args: []string{"bogus"}, want: `unknown command "bogus"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), tt.args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if !strings.Contains(stderr.String(), "usage: zolemc") {
				t.Fatalf("stderr missing usage:\n%s", stderr.String())
			}
		})
	}

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "profiles create") {
		t.Fatalf("help output missing commands:\n%s", stdout.String())
	}

	for _, args := range [][]string{{"-h"}, {"--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout.Reset()
			stderr.Reset()
			if err := run(context.Background(), args, &stdout, &stderr); err != nil {
				t.Fatalf("root help failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "profiles create") || !strings.Contains(stdout.String(), "listeners create") {
				t.Fatalf("root help output missing commands:\n%s", stdout.String())
			}
			if strings.Contains(stderr.String(), "flag: help requested") {
				t.Fatalf("root help wrote flag package error to stderr:\n%s", stderr.String())
			}
		})
	}
}

func TestAdminHealthOutputModes(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/_zolem/health" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	t.Cleanup(admin.Close)

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "health"}, &stdout, &stderr); err != nil {
		t.Fatalf("health failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "admin health: ok" {
		t.Fatalf("human health output = %q", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-json", "-admin-url", admin.URL, "health"}, &stdout, &stderr); err != nil {
		t.Fatalf("health -json failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != `{"status":"ok"}` {
		t.Fatalf("json health output = %q", stdout.String())
	}
}

func TestNoArgCommandsRejectTrailingArgs(t *testing.T) {
	// The admin server fails the test if any request reaches it: a command that
	// is rejected for trailing args must error before issuing any HTTP request.
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Errorf("unexpected request reached admin: %s %s", req.Method, req.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(admin.Close)

	cases := []struct {
		name string
		args []string
	}{
		// A global flag placed after the command is the common trap: it is never
		// parsed as the global flag and was previously ignored silently.
		{name: "health -json", args: []string{"-admin-url", admin.URL, "health", "-json"}},
		{name: "health extra", args: []string{"-admin-url", admin.URL, "health", "bogus"}},
		{name: "profiles list extra", args: []string{"-admin-url", admin.URL, "profiles", "list", "-json"}},
		{name: "listeners list extra", args: []string{"-admin-url", admin.URL, "listeners", "list", "extra"}},
		{name: "listener health extra", args: []string{"-base-url", admin.URL, "listener", "health", "-json"}},
		{name: "listener state extra", args: []string{"-base-url", admin.URL, "listener", "state", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
				t.Fatalf("err = %v, want 'unexpected arguments'", err)
			}
		})
	}
}

func TestProfilesListAndDeleteOutputModes(t *testing.T) {
	var gotMethod, gotPath string
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod, gotPath = req.Method, req.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/_zolem/profiles":
			fmt.Fprint(w, `[{"name":"demo","backend":"lorem"},{"name":"fixture","backend":"fixture"}]`)
		case req.Method == http.MethodDelete && req.URL.Path == "/_zolem/profiles/demo":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	}))
	t.Cleanup(admin.Close)

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "profiles", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("profiles list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "demo\tbackend=lorem") || !strings.Contains(stdout.String(), "fixture\tbackend=fixture") {
		t.Fatalf("profiles human output:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-json", "-admin-url", admin.URL, "profiles", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("profiles list -json failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `"name":"demo"`) {
		t.Fatalf("profiles json output:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "profiles", "delete", "demo"}, &stdout, &stderr); err != nil {
		t.Fatalf("profiles delete failed: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/_zolem/profiles/demo" {
		t.Fatalf("delete request = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(stdout.String(), "profile demo deleted") {
		t.Fatalf("delete human output = %q", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-json", "-admin-url", admin.URL, "profiles", "delete", "demo"}, &stdout, &stderr); err != nil {
		t.Fatalf("profiles delete -json failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != `{"deleted":"demo"}` {
		t.Fatalf("delete json output = %q", stdout.String())
	}
}

func TestProfilesListEmptyAndCreateValidation(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(admin.Close)

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "profiles", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("profiles list empty failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "no profiles" {
		t.Fatalf("empty profiles output = %q", stdout.String())
	}

	err := run(context.Background(), []string{"profiles"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "profiles requires list") {
		t.Fatalf("profiles missing subcommand error = %v", err)
	}
	err = run(context.Background(), []string{"profiles", "bogus"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), `unknown profiles command "bogus"`) {
		t.Fatalf("profiles unknown error = %v", err)
	}
	err = run(context.Background(), []string{"profiles", "create"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "requires exactly one profile name") {
		t.Fatalf("profiles create missing name error = %v", err)
	}
	err = run(context.Background(), []string{"profiles", "create", "demo", "-wasm-timeout-ms", "-1", "-backend", "wasm"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "must be non-negative") {
		t.Fatalf("negative wasm timeout error = %v", err)
	}
	err = run(context.Background(), []string{"profiles", "create", "demo", "-wasm-timeout-ms", "10"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "requires -backend wasm") {
		t.Fatalf("wasm timeout backend error = %v", err)
	}
}

func TestListenersListCreateDeleteAndValidation(t *testing.T) {
	var createPayload localListenerPayload
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/_zolem/listeners":
			fmt.Fprint(w, `[{"name":"openai-demo","provider":"openai","profile":"demo","backend":"lorem","base_url":"http://127.0.0.1:19001"}]`)
		case req.Method == http.MethodPut && req.URL.Path == "/_zolem/listeners/openai-demo":
			if err := json.NewDecoder(req.Body).Decode(&createPayload); err != nil {
				t.Fatalf("decode create payload: %v", err)
			}
			fmt.Fprint(w, `{"name":"openai-demo","provider":"openai","profile":"demo","backend":"lorem","base_url":"http://127.0.0.1:19001","tls":true}`)
		case req.Method == http.MethodDelete && req.URL.Path == "/_zolem/listeners/openai-demo":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	}))
	t.Cleanup(admin.Close)

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "listeners", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("listeners list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "openai-demo\topenai\tprofile=demo") {
		t.Fatalf("listeners list output:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-admin-url", admin.URL, "listeners", "create", "openai-demo", "-provider", "openai", "-profile", "demo", "-addr", "127.0.0.1:0", "-tls"}, &stdout, &stderr); err != nil {
		t.Fatalf("listeners create failed: %v", err)
	}
	if createPayload.Provider != "openai" || createPayload.Profile != "demo" || !createPayload.TLS {
		t.Fatalf("create payload = %+v", createPayload)
	}
	if !strings.Contains(stdout.String(), "listener openai-demo created") {
		t.Fatalf("listeners create output = %q", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-json", "-admin-url", admin.URL, "listeners", "delete", "openai-demo"}, &stdout, &stderr); err != nil {
		t.Fatalf("listeners delete failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != `{"deleted":"openai-demo"}` {
		t.Fatalf("listeners delete json output = %q", stdout.String())
	}

	for _, args := range [][]string{
		{"listeners"},
		{"listeners", "bogus"},
		{"listeners", "create"},
		{"listeners", "delete"},
		{"listeners", "calls"},
		{"listeners", "calls", "bogus"},
	} {
		stdout.Reset()
		stderr.Reset()
		err := run(context.Background(), append([]string{"-admin-url", admin.URL}, args...), &stdout, &stderr)
		if err == nil {
			t.Fatalf("run %v unexpectedly succeeded", args)
		}
	}
}

func TestListenerHealthStateAndRequestValidation(t *testing.T) {
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/_zolem/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/_zolem/state":
			fmt.Fprint(w, `{"provider":"openai","profile":"demo","backend":"lorem","listener":"openai-demo","tls":false}`)
		case "/v1/models":
			if req.Header.Get("Authorization") != "Bearer sk-test" {
				t.Fatalf("Authorization header = %q", req.Header.Get("Authorization"))
			}
			fmt.Fprint(w, `{"object":"list","data":[]}`)
		case "/v1/error":
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":"bad"}`)
		default:
			t.Fatalf("unexpected listener path: %s", req.URL.Path)
		}
	}))
	t.Cleanup(listener.Close)

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"-base-url", listener.URL, "listener", "health"}, &stdout, &stderr); err != nil {
		t.Fatalf("listener health failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "listener health: ok" {
		t.Fatalf("listener health output = %q", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-json", "-base-url", listener.URL, "listener", "state"}, &stdout, &stderr); err != nil {
		t.Fatalf("listener state failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `"listener":"openai-demo"`) {
		t.Fatalf("listener state json = %q", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-base-url", listener.URL, "request", "-path", "/v1/models", "-H", "Authorization: Bearer sk-test"}, &stdout, &stderr); err != nil {
		t.Fatalf("provider request failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `"object":"list"`) {
		t.Fatalf("provider request output = %q", stdout.String())
	}

	stdout.Reset()
	if err := run(context.Background(), []string{"-json", "-base-url", listener.URL, "request", "-path", "/v1/models", "-H", "Authorization: Bearer sk-test"}, &stdout, &stderr); err != nil {
		t.Fatalf("provider request json failed: %v", err)
	}
	if !strings.Contains(stdout.String(), `"status":200`) {
		t.Fatalf("provider request json output = %q", stdout.String())
	}

	err := run(context.Background(), []string{"listener", "health"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "commands require -base-url") {
		t.Fatalf("listener missing base-url error = %v", err)
	}
	err = run(context.Background(), []string{"-base-url", listener.URL, "listener", "bogus"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), `unknown listener command "bogus"`) {
		t.Fatalf("listener unknown error = %v", err)
	}
	err = run(context.Background(), []string{"request"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "request requires -base-url") {
		t.Fatalf("request missing base-url error = %v", err)
	}
	err = run(context.Background(), []string{"-base-url", listener.URL, "request"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "request requires -path") {
		t.Fatalf("request missing path error = %v", err)
	}
	err = run(context.Background(), []string{"-base-url", listener.URL, "request", "-path", "/v1/models", "-H", "bad-header"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid header") {
		t.Fatalf("bad header error = %v", err)
	}
	err = run(context.Background(), []string{"-base-url", listener.URL, "request", "-path", "/v1/error"}, &stdout, &stderr)
	var apiErr *admincli.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Error(), "502") || !strings.Contains(apiErr.Error(), "bad") {
		t.Fatalf("api error = %v", err)
	}
}

func TestRequestBodyJoinBaseAndRepeatedStrings(t *testing.T) {
	bodyFile := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(bodyFile, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	body, err := requestBody("", bodyFile)
	if err != nil {
		t.Fatalf("requestBody file: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("file body = %s", body)
	}
	body, err = requestBody(`{"inline":true}`, "")
	if err != nil || string(body) != `{"inline":true}` {
		t.Fatalf("inline body = %s err=%v", body, err)
	}
	if body, err = requestBody("", ""); err != nil || body != nil {
		t.Fatalf("empty body = %s err=%v, want nil nil", body, err)
	}
	if _, err = requestBody("{}", bodyFile); err == nil {
		t.Fatal("requestBody with both sources unexpectedly succeeded")
	}

	got, err := admincli.JoinBaseAndPath("http://example.test/api/", "v1/models?limit=1")
	if err != nil {
		t.Fatalf("joinBaseAndPath: %v", err)
	}
	if got != "http://example.test/api/v1/models?limit=1" {
		t.Fatalf("joined URL = %q", got)
	}
	for _, bad := range []struct {
		base string
		path string
	}{
		{base: "", path: "/v1/models"},
		{base: "://bad", path: "/v1/models"},
		{base: "http://example.test", path: "https://evil.test/v1/models"},
	} {
		if _, err := admincli.JoinBaseAndPath(bad.base, bad.path); err == nil {
			t.Fatalf("JoinBaseAndPath(%q, %q) unexpectedly succeeded", bad.base, bad.path)
		}
	}

	var values repeatedStrings
	if err := values.Set("A: 1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_ = values.Set("B: 2")
	if values.String() != "A: 1, B: 2" {
		t.Fatalf("String = %q", values.String())
	}
}

type commandResult struct {
	stdout string
	stderr string
}

func runZolemc(t *testing.T, repoRoot string, args ...string) commandResult {
	t.Helper()

	result, err := runZolemcRaw(t, repoRoot, args...)
	if err != nil {
		t.Fatalf("zolemc %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, result.stdout, result.stderr)
	}
	return result
}

func runZolemcFail(t *testing.T, repoRoot string, args ...string) commandResult {
	t.Helper()

	result, err := runZolemcRaw(t, repoRoot, args...)
	if err == nil {
		t.Fatalf("zolemc %s unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), result.stdout, result.stderr)
	}
	return result
}

func runZolemcRaw(t *testing.T, repoRoot string, args ...string) (commandResult, error) {
	t.Helper()

	bin := buildZolemcBinary(t, repoRoot)
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return commandResult{stdout: stdout.String(), stderr: stderr.String()}, err
	}
	return commandResult{stdout: stdout.String(), stderr: stderr.String()}, nil
}

type localAdminService struct {
	baseURL string
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	errCh   chan error
	logs    *bytes.Buffer
}

func startLocalAdminService(t *testing.T, repoRoot string) *localAdminService {
	t.Helper()

	bin := buildZolemBinary(t, repoRoot)
	port := pickPort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", port)

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-local-admin-addr", adminAddr)
	cmd.Env = os.Environ()
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	configureProcReaping(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start local admin: %v", err)
	}

	svc := &localAdminService{
		baseURL: "http://" + adminAddr,
		cmd:     cmd,
		cancel:  cancel,
		done:    make(chan struct{}),
		errCh:   make(chan error, 1),
		logs:    &logs,
	}

	go func() {
		svc.errCh <- cmd.Wait()
		close(svc.done)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-svc.done:
			err := <-svc.errCh
			t.Fatalf("local admin exited before readiness: %v\nlogs:\n%s", err, logs.String())
		default:
		}

		resp, err := client.Get(svc.baseURL + "/_zolem/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return svc
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for local admin readiness\nlogs:\n%s", logs.String())
	return nil
}

func (s *localAdminService) Close() {
	s.cancel()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

func pickPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listeners are not permitted in this environment: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
