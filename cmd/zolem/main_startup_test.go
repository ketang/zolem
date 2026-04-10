package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zolem.dev/zolem/internal/config"
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/response"
	runtimecfg "zolem.dev/zolem/internal/runtime"
	"zolem.dev/zolem/internal/specs"
)

type fakeGenerator struct {
	tokens []string
}

func (g *fakeGenerator) Generate(n int) []string {
	if n <= 0 || len(g.tokens) == 0 {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = g.tokens[i%len(g.tokens)]
	}
	return out
}

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

type fakeContractLoader struct {
	fallbacks map[string]loadResult
	refreshes map[string]loadResult
}

type loadResult struct {
	schema specs.NormalizedSchema
	err    error
}

func (f fakeContractLoader) LoadFallback(source specs.ContractSource) (specs.NormalizedSchema, error) {
	if result, ok := f.fallbacks[source.Key()]; ok {
		return result.schema, result.err
	}
	return specs.NormalizedSchema{}, errors.New("unexpected fallback contract request")
}

func (f fakeContractLoader) Refresh(source specs.ContractSource) (specs.NormalizedSchema, error) {
	if result, ok := f.refreshes[source.Key()]; ok {
		return result.schema, result.err
	}
	return specs.NormalizedSchema{}, errors.New("unexpected refresh contract request")
}

func fakeLoaderForTests() fakeContractLoader {
	return fakeContractLoader{
		fallbacks: map[string]loadResult{
			"anthropic:v1":  {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalAnthropicSchema)}},
			"openai:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}},
			"openrouter:v1": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalOpenAISchema)}},
			"gemini:v1":     {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
			"gemini:v1beta": {schema: specs.NormalizedSchema{Bytes: []byte(startupMinimalGeminiSchema)}},
		},
		refreshes: map[string]loadResult{
			"openai:v1":     {err: errors.New("fetch failed")},
			"openrouter:v1": {err: errors.New("fetch failed")},
			"gemini:v1":     {err: errors.New("fetch failed")},
			"gemini:v1beta": {err: errors.New("fetch failed")},
		},
	}
}

func disabledOllamaClient(context.Context, config.OllamaConfig) (textGenerator, []string) {
	return nil, nil
}

type stubTextGenerator string

func (g stubTextGenerator) Generate(context.Context, string) (string, error) {
	return string(g), nil
}

func TestRun_ConfigLoadFailure(t *testing.T) {
	var listenCalled bool
	err := run("does-not-matter", startupDeps{
		loadConfig: func(string) (*config.Config, error) {
			return nil, errors.New("boom")
		},
		listen: func(string, http.Handler) error {
			listenCalled = true
			return nil
		},
		listenTLS: func(string, string, string, http.Handler) error {
			listenCalled = true
			return nil
		},
	})

	if err == nil || !strings.Contains(err.Error(), "load config") {
		t.Fatalf("expected load config error, got %v", err)
	}
	if listenCalled {
		t.Fatal("listener should not be called on config load failure")
	}
}

func TestBuildStartupApp_RefreshWarningsKeepFallbackSchema(t *testing.T) {
	cfg := &config.Config{
		Specs: config.SpecsConfig{CacheDir: t.TempDir()},
	}

	validator := specs.NewValidator()
	app, warnings, err := buildStartupApp(cfg, startupDeps{
		newContractLoader: func(string) contractLoader {
			loader := fakeLoaderForTests()
			loader.refreshes["openai:v1"] = loadResult{err: errors.New("normalize failed")}
			loader.refreshes["openrouter:v1"] = loadResult{err: errors.New("normalize failed")}
			return loader
		},
		newValidator: func() *specs.Validator {
			return validator
		},
		newRunner:       fixture.NewRunner,
		newLorem:        response.NewLoremGenerator,
		newOllamaClient: disabledOllamaClient,
		readFile:        os.ReadFile,
	})
	if app != nil {
		defer app.close()
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) < 4 {
		t.Fatalf("expected warnings, got %v", warnings)
	}
	if !containsWarning(warnings, "failed to refresh contract openai:v1") {
		t.Fatalf("missing openai refresh warning: %v", warnings)
	}
	if !containsWarning(warnings, "failed to refresh contract openrouter:v1") {
		t.Fatalf("missing openrouter refresh warning: %v", warnings)
	}
	if err := validator.Validate("openai", "v1", []byte(`{"model":"gpt-4o","messages":[]}`)); err != nil {
		t.Fatalf("expected fallback openai schema to remain loaded, got %v", err)
	}
	if err := validator.Validate("openai", "v1", []byte(`{"messages":[]}`)); err == nil {
		t.Fatal("expected fallback openai schema to reject missing model")
	}
}

func TestBuildStartupApp_OllamaClientEnabled(t *testing.T) {
	cfg := &config.Config{
		Specs: config.SpecsConfig{CacheDir: t.TempDir()},
		Ollama: config.OllamaConfig{},
	}

	app, warnings, err := buildStartupApp(cfg, startupDeps{
		newContractLoader: func(string) contractLoader {
			return fakeLoaderForTests()
		},
		newRunner: fixture.NewRunner,
		newLorem:  response.NewLoremGenerator,
		newOllamaClient: func(_ context.Context, _ config.OllamaConfig) (textGenerator, []string) {
			return stubTextGenerator("ollama response"), []string{"ollama: using model gemma4:e4b"}
		},
		readFile: os.ReadFile,
	})
	if app != nil {
		defer app.close()
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsWarning(warnings, "ollama: using model gemma4:e4b") {
		t.Fatalf("expected ollama warning, got %v", warnings)
	}
}

func TestSpecSourceMap_CanonicalSourceInvariants(t *testing.T) {
	sources := specSourceMap()
	if _, ok := sources["anthropic:v1"]; ok {
		t.Fatal("anthropic:v1 should not use a remote canonical source")
	}
	if got := sources["openai:v1"]; got != "https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml" {
		t.Fatalf("unexpected openai:v1 source: %q", got)
	}
	if got := sources["openrouter:v1"]; got != "https://openrouter.ai/openapi.yaml" {
		t.Fatalf("unexpected openrouter:v1 source: %q", got)
	}
	if got := sources["gemini:v1"]; got != "https://generativelanguage.googleapis.com/$discovery/rest?version=v1" {
		t.Fatalf("unexpected gemini:v1 source: %q", got)
	}
	if got := sources["gemini:v1beta"]; got != "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta" {
		t.Fatalf("unexpected gemini:v1beta source: %q", got)
	}
}

func TestBuildStartupApp_FixtureDirLoadFailure(t *testing.T) {
	cfg := &config.Config{
		Mode:     "fixture",
		Specs:    config.SpecsConfig{CacheDir: t.TempDir()},
		Fixtures: config.FixturesConfig{Dir: filepath.Join(t.TempDir(), "missing-fixtures")},
	}

	_, _, err := buildStartupApp(cfg, startupDeps{
		newContractLoader: func(string) contractLoader {
			return fakeLoaderForTests()
		},
		newRunner:       fixture.NewRunner,
		newLorem:        response.NewLoremGenerator,
		newOllamaClient: disabledOllamaClient,
		readFile:        os.ReadFile,
	})

	if err == nil || !strings.Contains(err.Error(), "read fixture dir") {
		t.Fatalf("expected fixture dir load error, got %v", err)
	}
}

func TestBuildStartupApp_WASMReadFailure(t *testing.T) {
	fixturesDir := t.TempDir()
	writeFixtureDir(t, fixturesDir, "broken-read", []byte("not-used"))

	cfg := &config.Config{
		Mode:     "fixture",
		Specs:    config.SpecsConfig{CacheDir: t.TempDir()},
		Fixtures: config.FixturesConfig{Dir: fixturesDir},
	}

	_, _, err := buildStartupApp(cfg, startupDeps{
		newContractLoader: func(string) contractLoader {
			return fakeLoaderForTests()
		},
		newRunner:       fixture.NewRunner,
		newLorem:        response.NewLoremGenerator,
		newOllamaClient: disabledOllamaClient,
		readFile: func(string) ([]byte, error) {
			return nil, errors.New("read denied")
		},
	})

	if err == nil || !strings.Contains(err.Error(), "read wasm for fixture \"broken-read\"") {
		t.Fatalf("expected wasm read error, got %v", err)
	}
}

func TestBuildStartupApp_WASMCompileFailure(t *testing.T) {
	fixturesDir := t.TempDir()
	writeFixtureDir(t, fixturesDir, "broken-compile", []byte{0x00, 0x01, 0x02})

	cfg := &config.Config{
		Mode:     "fixture",
		Specs:    config.SpecsConfig{CacheDir: t.TempDir()},
		Fixtures: config.FixturesConfig{Dir: fixturesDir},
	}

	_, _, err := buildStartupApp(cfg, startupDeps{
		newContractLoader: func(string) contractLoader {
			return fakeLoaderForTests()
		},
		newRunner:       fixture.NewRunner,
		newLorem:        response.NewLoremGenerator,
		newOllamaClient: disabledOllamaClient,
		readFile:        os.ReadFile,
	})

	if err == nil || !strings.Contains(err.Error(), "compile wasm for fixture \"broken-compile\"") {
		t.Fatalf("expected wasm compile error, got %v", err)
	}
}

func TestBuildHandler_ZolemErrorResponses(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	handler := buildHandler([]config.RouteConfig{
		{Host: "*.api.example.dev", Provider: "bogus", Labels: map[string]string{"tenant": "{1}"}},
	}, specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator(), nil)

	t.Run("unmatched host", func(t *testing.T) {
		req := httptestRequest(http.MethodPost, "/anything", bytes.NewBufferString("{}"))
		req.Host = "missing.other.example.dev"
		resp := doRequest(t, handler, req)
		defer resp.Body.Close()

		if resp.Header.Get("X-Zolem-Error") != "true" {
			t.Fatal("expected X-Zolem-Error header")
		}
		var payload map[string]string
		decodeJSON(t, resp.Body, &payload)
		if payload["zolem_error"] != "no route matched host: missing.other.example.dev" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		req := httptestRequest(http.MethodPost, "/anything", bytes.NewBufferString("{}"))
		req.Host = "tenant.api.example.dev"
		resp := doRequest(t, handler, req)
		defer resp.Body.Close()

		if resp.Header.Get("X-Zolem-Error") != "true" {
			t.Fatal("expected X-Zolem-Error header")
		}
		var payload map[string]string
		decodeJSON(t, resp.Body, &payload)
		if payload["zolem_error"] != "unknown provider: bogus" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
	})
}

func TestBuildLocalHandler_StateResponse(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	listenerRuntime, err := (localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "openai",
		Profile:  "demo",
		Backend:  "lorem",
	}).runtime()
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}

	handler := buildLocalHandler(listenerRuntime, specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator())
	req := httptestRequest(http.MethodGet, "/_zolem/state", bytes.NewBuffer(nil))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var payload map[string]any
	decodeJSON(t, resp.Body, &payload)
	if payload["provider"] != "openai" {
		t.Fatalf("provider: got %#v, want openai", payload["provider"])
	}
	if payload["profile"] != "demo" {
		t.Fatalf("profile: got %#v, want demo", payload["profile"])
	}
	if payload["backend"] != "lorem" {
		t.Fatalf("backend: got %#v, want lorem", payload["backend"])
	}
	if payload["listener"] != "127.0.0.1:12001" {
		t.Fatalf("listener: got %#v, want 127.0.0.1:12001", payload["listener"])
	}
	if payload["tls"] != false {
		t.Fatalf("tls: got %#v, want false", payload["tls"])
	}
}

func TestBuildLocalHandler_HealthResponse(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	listenerRuntime := runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     "openai-demo",
			Addr:     "127.0.0.1:12001",
			Provider: "openai",
			Profile:  "demo",
		},
		Profile: runtimecfg.RuntimeProfile{Name: "demo", Backend: "lorem"},
	}
	handler := buildLocalHandler(listenerRuntime, specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator())
	req := httptestRequest(http.MethodGet, "/_zolem/health", bytes.NewBuffer(nil))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	var payload map[string]any
	decodeJSON(t, resp.Body, &payload)
	if payload["status"] != "ok" {
		t.Fatalf("status payload: got %#v, want ok", payload["status"])
	}
}

func TestBuildLocalHandler_DispatchesConfiguredProvider(t *testing.T) {
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	listenerRuntime, err := (localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "openai",
		Profile:  "demo",
		Backend:  "lorem",
	}).runtime()
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}

	handler := buildLocalHandler(listenerRuntime, specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator())
	req := httptestRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Zolem-Error") == "true" {
		t.Fatal("did not expect zolem error header")
	}
}

func TestBuildLocalStartupApp_RejectsUnknownProvider(t *testing.T) {
	_, _, err := buildLocalStartupApp(localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "bogus",
		Profile:  "demo",
		Backend:  "lorem",
	}, startupDeps{})
	if err == nil || !strings.Contains(err.Error(), "invalid local provider") {
		t.Fatalf("expected invalid local provider error, got %v", err)
	}
}

func TestBuildLocalStartupApp_FixtureBackendRequiresFixturesDir(t *testing.T) {
	_, _, err := buildLocalStartupApp(localOptions{
		Addr:     "127.0.0.1:12001",
		Provider: "anthropic",
		Profile:  "demo",
		Backend:  "fixture",
	}, startupDeps{})
	if err == nil || !strings.Contains(err.Error(), "-local-fixtures-dir") {
		t.Fatalf("expected missing fixtures dir error, got %v", err)
	}
}

func TestBuildLocalStartupApp_FixtureBackendServesFixture(t *testing.T) {
	fixturesDir := t.TempDir()
	writeLocalFixture(t, fixturesDir, "anthropic-fixture", "anthropic", "v1", []byte(`{"id":"fixture-msg","type":"message","role":"assistant","content":[{"type":"text","text":"fixture text"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), localAlwaysMatchWASM)

	app, _, err := buildLocalStartupApp(localOptions{
		Addr:        "127.0.0.1:12001",
		Provider:    "anthropic",
		Profile:     "demo",
		Backend:     "fixture",
		FixturesDir: fixturesDir,
	}, startupDeps{
		newFetcher: func(string, map[string]string) specFetcher {
			return fakeFetcher{
				"anthropic:v1":  {err: errors.New("fetch failed")},
				"openai:v1":     {err: errors.New("fetch failed")},
				"gemini:v1":     {err: errors.New("fetch failed")},
				"gemini:v1beta": {err: errors.New("fetch failed")},
			}
		},
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
	if payload["id"] != "fixture-msg" {
		t.Fatalf("id: got %#v, want fixture-msg", payload["id"])
	}
}

func TestBuildLocalStartupApp_FixtureNamespaceScopesFixtures(t *testing.T) {
	fixturesDir := t.TempDir()
	writeLocalFixture(t, filepath.Join(fixturesDir, "team-a"), "anthropic-team-a", "anthropic", "v1", []byte(`{"id":"fixture-team-a","type":"message","role":"assistant","content":[{"type":"text","text":"fixture team a"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), localAlwaysMatchWASM)
	writeLocalFixture(t, filepath.Join(fixturesDir, "team-b"), "anthropic-team-b", "anthropic", "v1", []byte(`{"id":"fixture-team-b","type":"message","role":"assistant","content":[{"type":"text","text":"fixture team b"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), localAlwaysMatchWASM)

	app, _, err := buildLocalStartupAppForRuntime(runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     "anthropic-team-a",
			Addr:     "127.0.0.1:12001",
			Provider: "anthropic",
			Profile:  "demo",
		},
		Profile: runtimecfg.RuntimeProfile{
			Name:             "demo",
			Backend:          runtimecfg.BackendFixture,
			FixtureNamespace: "team-a",
		},
	}, fixturesDir, startupDeps{
		newFetcher: func(string, map[string]string) specFetcher {
			return fakeFetcher{
				"anthropic:v1":  {err: errors.New("fetch failed")},
				"openai:v1":     {err: errors.New("fetch failed")},
				"gemini:v1":     {err: errors.New("fetch failed")},
				"gemini:v1beta": {err: errors.New("fetch failed")},
			}
		},
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
		newFetcher: func(string, map[string]string) specFetcher {
			return fakeFetcher{
				"anthropic:v1":  {err: errors.New("fetch failed")},
				"openai:v1":     {err: errors.New("fetch failed")},
				"gemini:v1":     {err: errors.New("fetch failed")},
				"gemini:v1beta": {err: errors.New("fetch failed")},
			}
		},
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
		t.Fatalf("expected lorem backend to ignore fixture match")
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
		newFetcher: func(string, map[string]string) specFetcher {
			return fakeFetcher{
				"anthropic:v1":  {err: errors.New("fetch failed")},
				"openai:v1":     {err: errors.New("fetch failed")},
				"gemini:v1":     {err: errors.New("fetch failed")},
				"gemini:v1beta": {err: errors.New("fetch failed")},
			}
		},
		newRunner: fixture.NewRunner,
		newLorem:  response.NewLoremGenerator,
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

func TestLoadFixtures_SkipsOutsideFixtureMode(t *testing.T) {
	fixturesDir := t.TempDir()
	writeFixtureDir(t, fixturesDir, "ignored", []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})

	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)

	fixtures, warnings, err := loadFixtures(&config.Config{
		Mode:     "faker",
		Fixtures: config.FixturesConfig{Dir: fixturesDir},
	}, runner, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fixtures) != 0 {
		t.Fatalf("fixtures: got %d, want 0", len(fixtures))
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: got %v, want none", warnings)
	}
}

func TestSelectGenerator_UsesFakerMode(t *testing.T) {
	got := selectGenerator("faker", startupDeps{
		newLorem: func() *response.LoremGenerator {
			t.Fatal("lorem generator should not be selected")
			return nil
		},
		newFaker: func() *response.FakerGenerator {
			return response.NewFakerGenerator()
		},
	})

	text := strings.Join(got.Generate(8), "")
	if strings.Contains(text, "lorem") {
		t.Fatalf("faker generator text should not use lorem vocabulary: %q", text)
	}
	if !strings.Contains(text, "Summit Labs") {
		t.Fatalf("faker generator text: got %q, want faker-style output", text)
	}
}

func TestRun_UsesTLSWhenConfigured(t *testing.T) {
	var plainCalled bool
	var tlsCalled bool
	cfg := &config.Config{
		Server: config.ServerConfig{
			Addr: ":443",
			TLS:  config.TLSConfig{Cert: "cert.pem", Key: "key.pem"},
		},
		Specs: config.SpecsConfig{CacheDir: t.TempDir()},
	}

	err := run("ignored", startupDeps{
		loadConfig: func(string) (*config.Config, error) {
			return cfg, nil
		},
		newContractLoader: func(string) contractLoader {
			return fakeLoaderForTests()
		},
		newRunner:       fixture.NewRunner,
		newLorem:        response.NewLoremGenerator,
		newOllamaClient: disabledOllamaClient,
		readFile:        os.ReadFile,
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
		t.Fatalf("unexpected error: %v", err)
	}
	if plainCalled {
		t.Fatal("plain listener should not be called when TLS is configured")
	}
	if !tlsCalled {
		t.Fatal("TLS listener was not called")
	}
}

func readRepoFile(t *testing.T, elems ...string) []byte {
	t.Helper()

	path := filepath.Join(append([]string{"..", ".."}, elems...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repo file %s: %v", path, err)
	}
	return data
}

func TestRun_UsesPlainHTTPWhenTLSMissing(t *testing.T) {
	var plainCalled bool
	var tlsCalled bool
	cfg := &config.Config{
		Server: config.ServerConfig{Addr: ":8080"},
		Specs:  config.SpecsConfig{CacheDir: t.TempDir()},
	}

	err := run("ignored", startupDeps{
		loadConfig: func(string) (*config.Config, error) {
			return cfg, nil
		},
		newContractLoader: func(string) contractLoader {
			return fakeLoaderForTests()
		},
		newRunner:       fixture.NewRunner,
		newLorem:        response.NewLoremGenerator,
		newOllamaClient: disabledOllamaClient,
		readFile:        os.ReadFile,
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
		t.Fatalf("unexpected error: %v", err)
	}
	if !plainCalled {
		t.Fatal("plain listener was not called")
	}
	if tlsCalled {
		t.Fatal("TLS listener should not be called without cert/key")
	}
}

func writeFixtureDir(t *testing.T, root, id string, wasm []byte) {
	t.Helper()

	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}

	meta := []byte("id: " + id + "\nprovider: anthropic\nversion: v1\nstatus: 200\n")
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), meta, 0o644); err != nil {
		t.Fatalf("write meta.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "response.json"), []byte(`{"content":[]}`), 0o644); err != nil {
		t.Fatalf("write response.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "match.wasm"), wasm, 0o644); err != nil {
		t.Fatalf("write match.wasm: %v", err)
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

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
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

const startupMinimalAnthropicSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model", "max_tokens", "messages"],
  "properties": {
    "model": {"type": "string"},
    "max_tokens": {"type": "integer"},
    "messages": {"type": "array"},
    "stream": {"type": "boolean"}
  },
  "additionalProperties": true
}`

const startupMinimalOpenAISchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model", "messages"],
  "properties": {
    "model": {"type": "string"},
    "messages": {"type": "array"},
    "stream": {"type": "boolean"}
  },
  "additionalProperties": true
}`

const startupMinimalGeminiSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["contents"],
  "properties": {
    "contents": {"type": "array"},
    "generationConfig": {"type": "object"}
  },
  "additionalProperties": true
}`
