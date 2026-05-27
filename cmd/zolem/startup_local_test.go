package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/response"
	runtimecfg "zolem.dev/zolem/internal/runtime"
)

type fakeFetcher map[string]fetchResult

type fetchResult struct {
	data []byte
	err  error
}

var localAlwaysMatchWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01,
	0x7d, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x12, 0x02, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x6d,
	0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x0a, 0x09,
	0x01, 0x07, 0x00, 0x43, 0x00, 0x00, 0x80, 0x3f, 0x0b,
}

func (f fakeFetcher) Get(provider, version string) ([]byte, error) {
	if result, ok := f[provider+":"+version]; ok {
		return result.data, result.err
	}
	return nil, nil
}

func TestBuildLocalStartupAppForRuntime_FixtureNamespace(t *testing.T) {
	fixturesDir := t.TempDir()
	writeLocalFixture(t, filepath.Join(fixturesDir, "team-a"), "fixture-team-a", "anthropic", "v1", []byte(`{"id":"fixture-team-a","type":"message","role":"assistant","content":[{"type":"text","text":"fixture text"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), localAlwaysMatchWASM)

	app, _, err := buildLocalStartupAppForRuntime(runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     "anthropic-demo",
			Addr:     "127.0.0.1:12001",
			Provider: "anthropic",
			Profile:  "demo",
		},
		Profile: runtimecfg.RuntimeProfile{
			Name:             "demo",
			Backend:          runtimecfg.BackendFixture,
			FixtureNamespace: "team-a",
		},
	}, fixturesDir, runtimecfg.NewProfileCounters(), nil, RecordCaps{}, startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err != nil {
		t.Fatalf("buildLocalStartupAppForRuntime: %v", err)
	}
	defer app.close()

	req := httptestRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "test-key")
	resp := doRequest(t, app.handler, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var payload map[string]any
	decodeJSON(t, resp.Body, &payload)
	if payload["id"] != "fixture-team-a" {
		t.Fatalf("id: got %#v, want fixture-team-a", payload["id"])
	}
}

func TestBuildLocalStartupAppForRuntime_DeprecationWarnings(t *testing.T) {
	fixturesDir := t.TempDir()
	// Fixture with match.wasm only (per-fixture WASM matcher).
	writeLocalFixture(t, filepath.Join(fixturesDir, "team-a"), "wasm-only", "anthropic", "v1", []byte(`{"id":"wasm-only","type":"message","role":"assistant","content":[{"type":"text","text":"x"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), localAlwaysMatchWASM)
	// Fixture with match.cel only.
	celDir := filepath.Join(fixturesDir, "team-a", "cel-only")
	if err := os.MkdirAll(celDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	celMeta := []byte("id: cel-only\nprovider: anthropic\nversion: v1\nstatus: 200\nmatch:\n  cel: 'true'\n")
	if err := os.WriteFile(filepath.Join(celDir, "meta.yaml"), celMeta, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(celDir, "response.json"), []byte(`{"id":"cel-only","type":"message","role":"assistant","content":[{"type":"text","text":"y"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}

	app, warnings, err := buildLocalStartupAppForRuntime(runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     "anthropic-demo",
			Addr:     "127.0.0.1:12001",
			Provider: "anthropic",
			Profile:  "demo",
		},
		Profile: runtimecfg.RuntimeProfile{
			Name:             "demo",
			Backend:          runtimecfg.BackendFixture,
			FixtureNamespace: "team-a",
		},
	}, fixturesDir, runtimecfg.NewProfileCounters(), nil, RecordCaps{}, startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err != nil {
		t.Fatalf("buildLocalStartupAppForRuntime: %v", err)
	}
	defer app.close()

	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, `fixture "cel-only": match.cel is deprecated`) {
		t.Errorf("expected match.cel deprecation warning, got: %v", warnings)
	}
	if !strings.Contains(joined, `fixture "wasm-only": match.wasm is deprecated`) {
		t.Errorf("expected match.wasm deprecation warning, got: %v", warnings)
	}
	if !strings.Contains(joined, "docs/local-runtime.md") {
		t.Errorf("warning should reference docs/local-runtime.md, got: %v", warnings)
	}

	// Fixture still serves.
	req := httptestRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "test-key")
	resp := doRequest(t, app.handler, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestBuildLocalStartupAppForRuntime_NoDeprecationWithFixturesYAML(t *testing.T) {
	fixturesDir := t.TempDir()
	nsDir := filepath.Join(fixturesDir, "team-a")
	// Fixture with no per-fixture matcher (namespace has fixtures.yaml).
	dir := filepath.Join(nsDir, "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := []byte("id: plain\nprovider: anthropic\nversion: v1\nstatus: 200\n")
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), meta, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(`{"id":"plain","type":"message","role":"assistant","content":[{"type":"text","text":"z"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}
	yaml := []byte("provider: anthropic\nversion: v1\nfixtures:\n  - expression: 'true'\n    fixture: plain\n")
	if err := os.WriteFile(filepath.Join(nsDir, "fixtures.yaml"), yaml, 0o644); err != nil {
		t.Fatalf("write fixtures.yaml: %v", err)
	}

	app, warnings, err := buildLocalStartupAppForRuntime(runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     "anthropic-demo",
			Addr:     "127.0.0.1:12001",
			Provider: "anthropic",
			Profile:  "demo",
		},
		Profile: runtimecfg.RuntimeProfile{
			Name:             "demo",
			Backend:          runtimecfg.BackendFixture,
			FixtureNamespace: "team-a",
		},
	}, fixturesDir, runtimecfg.NewProfileCounters(), nil, RecordCaps{}, startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err != nil {
		t.Fatalf("buildLocalStartupAppForRuntime: %v", err)
	}
	defer app.close()

	for _, w := range warnings {
		if strings.Contains(w, "deprecated") {
			t.Errorf("did not expect deprecation warning, got: %q", w)
		}
	}
}

func TestBuildLocalStartupAppForRuntime_TemplatedFixtureUsesRuntimeAndCounters(t *testing.T) {
	fixturesDir := t.TempDir()
	writeLocalTemplateFixture(t, fixturesDir, "openai-template", "openai", "v1", `{
  "id": {{ json .Fixture.ID }},
  "object": "chat.completion",
  "created": 1,
  "model": "fixture-model",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": {{ json (printf "%s:%s:%d:%d:%s" .Runtime.ListenerName .Runtime.ProfileName .Sequence.ProfileRequest .Sequence.TemplateRender .Runtime.BackendModel) }}
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
}`, localAlwaysMatchWASM)

	counters := runtimecfg.NewProfileCounters()
	app, _, err := buildLocalStartupAppForRuntime(runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     "openai-demo",
			Addr:     "127.0.0.1:12001",
			Provider: "openai",
			Profile:  "demo",
			TLS:      true,
		},
		Profile: runtimecfg.RuntimeProfile{
			Name:         "demo",
			Backend:      runtimecfg.BackendFixture,
			BackendModel: "backend-template-model",
		},
	}, fixturesDir, counters, nil, RecordCaps{}, startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err != nil {
		t.Fatalf("buildLocalStartupAppForRuntime: %v", err)
	}
	defer app.close()

	assertTemplatedOpenAIContent := func(want string) {
		t.Helper()
		req := httptestRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer sk-test")
		resp := doRequest(t, app.handler, req)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}

		var payload struct {
			ID      string `json:"id"`
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		decodeJSON(t, resp.Body, &payload)
		if payload.ID != "openai-template" {
			t.Fatalf("id: got %q, want openai-template", payload.ID)
		}
		if len(payload.Choices) != 1 {
			t.Fatalf("choices: got %d, want 1", len(payload.Choices))
		}
		if payload.Choices[0].Message.Content != want {
			t.Fatalf("content: got %q, want %q", payload.Choices[0].Message.Content, want)
		}
	}

	assertTemplatedOpenAIContent("openai-demo:demo:1:1:backend-template-model")
	assertTemplatedOpenAIContent("openai-demo:demo:2:2:backend-template-model")
}

func TestBuildLocalStartupApp_LoremBackendIgnoresFixtures(t *testing.T) {
	fixturesDir := t.TempDir()
	writeLocalFixture(t, fixturesDir, "anthropic-fixture", "anthropic", "v1", []byte(`{"id":"fixture-msg","type":"message","role":"assistant","content":[{"type":"text","text":"fixture text"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), localAlwaysMatchWASM)

	app, _, err := buildLocalStartupApp(localOptions{
		Addr:        "127.0.0.1:12001",
		Provider:    "anthropic",
		Profile:     "demo",
		Backend:     "lorem",
		FixturesDir: fixturesDir,
	}, startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err != nil {
		t.Fatalf("buildLocalStartupApp: %v", err)
	}
	defer app.close()

	req := httptestRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "test-key")
	resp := doRequest(t, app.handler, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var payload map[string]any
	decodeJSON(t, resp.Body, &payload)
	if payload["id"] == "fixture-msg" {
		t.Fatal("expected lorem backend to ignore fixture match")
	}
}

func TestBuildLocalStartupApp_FixtureBackendRequiresFixturesDir(t *testing.T) {
	_, _, err := buildLocalStartupApp(localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "anthropic",
		Profile:  "demo",
		Backend:  runtimecfg.BackendFixture,
	}, startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err == nil || !strings.Contains(err.Error(), "-local-fixtures-dir") {
		t.Fatalf("expected fixture dir error, got %v", err)
	}
}

func TestBuildLocalHandler_StateEndpoint(t *testing.T) {
	app, _, err := buildLocalStartupApp(localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "openai",
		Profile:  "demo",
		Backend:  "faker",
	}, startupDeps{
		newFetcher: disabledTestFetcher,
		newFaker:   response.NewFakerGenerator,
	})
	if err != nil {
		t.Fatalf("buildLocalStartupApp: %v", err)
	}
	defer app.close()

	resp := doRequest(t, app.handler, httptestRequest(http.MethodGet, "/_zolem/state", bytes.NewBuffer(nil)))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var state map[string]any
	decodeJSON(t, resp.Body, &state)
	if state["provider"] != "openai" || state["backend"] != "faker" {
		t.Fatalf("state: got %#v", state)
	}
}

func TestRunLocal_UsesTLSWhenConfigured(t *testing.T) {
	var plainCalled bool
	var tlsCalled bool

	err := runLocal(localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "openai",
		Profile:  "demo",
		Backend:  "lorem",
		TLS: localTLSConfig{
			CertFile: "cert.pem",
			KeyFile:  "key.pem",
		},
	}, startupDeps{
		newFetcher: disabledTestFetcher,
		newRunner:  fixture.NewRunner,
		newLorem:   response.NewLoremGenerator,
		listen: func(string, http.Handler) error {
			plainCalled = true
			return nil
		},
		listenTLS: func(string, string, string, http.Handler) error {
			tlsCalled = true
			return nil
		},
		logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("runLocal: %v", err)
	}
	if plainCalled {
		t.Fatal("plain listener should not be called when local TLS is configured")
	}
	if !tlsCalled {
		t.Fatal("TLS listener should be called when local TLS is configured")
	}
}

func TestRunLocal_UsesPlainHTTPWhenTLSMissing(t *testing.T) {
	var plainCalled bool
	var tlsCalled bool

	err := runLocal(localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "openai",
		Profile:  "demo",
		Backend:  "lorem",
	}, startupDeps{
		newFetcher: disabledTestFetcher,
		newRunner:  fixture.NewRunner,
		newLorem:   response.NewLoremGenerator,
		listen: func(string, http.Handler) error {
			plainCalled = true
			return nil
		},
		listenTLS: func(string, string, string, http.Handler) error {
			tlsCalled = true
			return nil
		},
		logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("runLocal: %v", err)
	}
	if !plainCalled {
		t.Fatal("plain listener was not called")
	}
	if tlsCalled {
		t.Fatal("TLS listener should not be called without cert/key")
	}
}

func TestRunLocal_RejectsPartialTLSConfig(t *testing.T) {
	err := runLocal(localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "openai",
		Profile:  "demo",
		Backend:  "lorem",
		TLS: localTLSConfig{
			CertFile: "cert.pem",
		},
	}, startupDeps{})
	if err == nil || !strings.Contains(err.Error(), "both cert and key") {
		t.Fatalf("expected partial TLS config error, got %v", err)
	}
}

func TestLocalOptions_RejectsNonLoopbackAddr(t *testing.T) {
	_, _, err := buildLocalStartupApp(localOptions{
		Addr:     "0.0.0.0:18080",
		Provider: "anthropic",
		Profile:  "demo",
		Backend:  "lorem",
	}, startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err == nil {
		t.Fatal("expected non-loopback addr to be rejected")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error should mention loopback, got: %v", err)
	}
}

func TestLocalOptions_AcceptsLoopbackAddr(t *testing.T) {
	app, _, err := buildLocalStartupApp(localOptions{
		Addr:     "127.0.0.1:8080",
		Provider: "anthropic",
		Profile:  "demo",
		Backend:  "lorem",
	}, startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err != nil {
		t.Fatalf("loopback addr should be accepted: %v", err)
	}
	defer app.close()
}

func TestGeneratorForBackend_UsesFakerMode(t *testing.T) {
	got, err := generatorForBackend("faker", startupDeps{
		newLorem: func() *response.LoremGenerator {
			t.Fatal("lorem generator should not be selected")
			return nil
		},
		newFaker: response.NewFakerGenerator,
	})
	if err != nil {
		t.Fatalf("generatorForBackend: %v", err)
	}

	text := strings.Join(got.Generate(8), "")
	if strings.Contains(text, "lorem") {
		t.Fatalf("faker generator text should not use lorem vocabulary: %q", text)
	}
}

func TestSpecSourceMap_CanonicalSourceInvariants(t *testing.T) {
	sources := specSourceMap()
	if _, ok := sources["anthropic:v1"]; ok {
		t.Fatal("anthropic:v1 should not use a remote canonical source")
	}
	if got := sources["openai:v1"]; got == "" {
		t.Fatal("openai:v1 source should not be empty")
	}
}

func disabledTestFetcher(string, map[string]string) specFetcher {
	return fakeFetcher{
		"anthropic:v1":  {err: errors.New("fetch disabled")},
		"openai:v1":     {err: errors.New("fetch disabled")},
		"gemini:v1":     {err: errors.New("fetch disabled")},
		"gemini:v1beta": {err: errors.New("fetch disabled")},
	}
}

func writeLocalFixture(t *testing.T, root, id, provider, version string, responseJSON, wasm []byte) {
	t.Helper()

	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}

	meta := []byte("id: " + id + "\nprovider: " + provider + "\nversion: " + version + "\nstatus: 200\n")
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), meta, 0o644); err != nil {
		t.Fatalf("write meta.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), responseJSON, 0o644); err != nil {
		t.Fatalf("write response.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "match.wasm"), wasm, 0o644); err != nil {
		t.Fatalf("write match.wasm: %v", err)
	}
}

func writeLocalTemplateFixture(t *testing.T, root, id, provider, version, responseTemplate string, wasm []byte) {
	t.Helper()

	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}

	meta := []byte("id: " + id + "\nprovider: " + provider + "\nversion: " + version + "\nstatus: 200\n")
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), meta, 0o644); err != nil {
		t.Fatalf("write meta.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json.tmpl"), []byte(responseTemplate), 0o644); err != nil {
		t.Fatalf("write response.json.tmpl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "match.wasm"), wasm, 0o644); err != nil {
		t.Fatalf("write match.wasm: %v", err)
	}
}

func httptestRequest(method, target string, body *bytes.Buffer) *http.Request {
	req, _ := http.NewRequest(method, target, body)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func doRequest(t *testing.T, handler http.Handler, req *http.Request) *http.Response {
	t.Helper()

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr.Result()
}

func decodeJSON(t *testing.T, body io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
