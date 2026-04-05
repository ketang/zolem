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

	"zolem.dev/zolem/internal/config"
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/specs"
)

type fakeFetcher map[string]fetchResult

type fetchResult struct {
	data []byte
	err  error
}

func (f fakeFetcher) Get(provider, version string) ([]byte, error) {
	if result, ok := f[provider+":"+version]; ok {
		return result.data, result.err
	}
	return nil, errors.New("unexpected spec request")
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

func TestBuildStartupApp_SpecWarnings(t *testing.T) {
	cfg := &config.Config{
		Specs: config.SpecsConfig{CacheDir: t.TempDir()},
	}

	validator := specs.NewValidator()
	app, warnings, err := buildStartupApp(cfg, startupDeps{
		newFetcher: func(string, map[string]string) specFetcher {
			return fakeFetcher{
				"anthropic:v1":  {err: errors.New("fetch failed")},
				"openai:v1":     {data: []byte("not-json")},
				"gemini:v1":     {data: []byte(`{"type":"object"}`)},
				"gemini:v1beta": {data: []byte(`{"type":"object"}`)},
			}
		},
		newValidator: func() *specs.Validator {
			return validator
		},
		newRunner: fixture.NewRunner,
		newLorem:  response.NewLoremGenerator,
		readFile:  os.ReadFile,
	})
	if app != nil {
		defer app.close()
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) < 2 {
		t.Fatalf("expected warnings, got %v", warnings)
	}
	if !containsWarning(warnings, "failed to fetch spec anthropic:v1") {
		t.Fatalf("missing fetch warning: %v", warnings)
	}
	if !containsWarning(warnings, "failed to load spec openai:v1") {
		t.Fatalf("missing validation warning: %v", warnings)
	}
}

func TestBuildStartupApp_LoadsGeminiDiscoverySchemas(t *testing.T) {
	cfg := &config.Config{
		Specs: config.SpecsConfig{CacheDir: t.TempDir()},
	}

	v1 := readRepoFile(t, filepath.Join("testdata", "specs", "gemini-discovery-v1.json"))
	v1beta := readRepoFile(t, filepath.Join("testdata", "specs", "gemini-discovery-v1beta.json"))
	validator := specs.NewValidator()

	app, warnings, err := buildStartupApp(cfg, startupDeps{
		newFetcher: func(string, map[string]string) specFetcher {
			return fakeFetcher{
				"anthropic:v1":  {err: errors.New("fetch failed")},
				"openai:v1":     {err: errors.New("fetch failed")},
				"gemini:v1":     {data: v1},
				"gemini:v1beta": {data: v1beta},
			}
		},
		newValidator: func() *specs.Validator {
			return validator
		},
		newRunner: fixture.NewRunner,
		newLorem:  response.NewLoremGenerator,
		readFile:  os.ReadFile,
	})
	if app != nil {
		defer app.close()
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsWarning(warnings, "failed to load spec gemini:v1") || containsWarning(warnings, "failed to load spec gemini:v1beta") {
		t.Fatalf("unexpected gemini load warning: %v", warnings)
	}

	valid := []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`)
	if err := validator.Validate("gemini", "v1", valid); err != nil {
		t.Fatalf("gemini v1 should validate loaded schema: %v", err)
	}
	if err := validator.Validate("gemini", "v1beta", valid); err != nil {
		t.Fatalf("gemini v1beta should validate loaded schema: %v", err)
	}

	invalid := []byte(`{"contents":[{"parts":[{}]}]}`)
	if err := validator.Validate("gemini", "v1", invalid); err == nil {
		t.Fatal("expected invalid gemini request to fail after discovery load")
	}
}

func TestBuildStartupApp_LoadsVendoredAnthropicSnapshot(t *testing.T) {
	cfg := &config.Config{
		Specs: config.SpecsConfig{CacheDir: t.TempDir()},
	}
	validator := specs.NewValidator()

	app, warnings, err := buildStartupApp(cfg, startupDeps{
		newFetcher: func(cacheDir string, sources map[string]string) specFetcher {
			return specs.NewFetcherWithFallback(cacheDir, sources, specs.VendoredFallbacks())
		},
		newValidator: func() *specs.Validator {
			return validator
		},
		newRunner: fixture.NewRunner,
		newLorem:  response.NewLoremGenerator,
		readFile:  os.ReadFile,
	})
	if app != nil {
		defer app.close()
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsWarning(warnings, "failed to fetch spec anthropic:v1") || containsWarning(warnings, "failed to load spec anthropic:v1") {
		t.Fatalf("unexpected anthropic warning: %v", warnings)
	}

	valid := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
	if err := validator.Validate("anthropic", "v1", valid); err != nil {
		t.Fatalf("anthropic v1 should validate vendored snapshot: %v", err)
	}

	invalid := []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"system","content":"hello"}]}`)
	if err := validator.Validate("anthropic", "v1", invalid); err == nil {
		t.Fatal("expected invalid anthropic request to fail after vendored load")
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
	if got := sources["gemini:v1"]; got != "https://generativelanguage.googleapis.com/$discovery/rest?version=v1" {
		t.Fatalf("unexpected gemini:v1 source: %q", got)
	}
	if got := sources["gemini:v1beta"]; got != "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta" {
		t.Fatalf("unexpected gemini:v1beta source: %q", got)
	}
}

func TestBuildStartupApp_FixtureDirLoadFailure(t *testing.T) {
	cfg := &config.Config{
		Specs:    config.SpecsConfig{CacheDir: t.TempDir()},
		Fixtures: config.FixturesConfig{Dir: filepath.Join(t.TempDir(), "missing-fixtures")},
	}

	_, _, err := buildStartupApp(cfg, startupDeps{
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
		readFile:  os.ReadFile,
	})

	if err == nil || !strings.Contains(err.Error(), "read fixture dir") {
		t.Fatalf("expected fixture dir load error, got %v", err)
	}
}

func TestBuildStartupApp_WASMReadFailure(t *testing.T) {
	fixturesDir := t.TempDir()
	writeFixtureDir(t, fixturesDir, "broken-read", []byte("not-used"))

	cfg := &config.Config{
		Specs:    config.SpecsConfig{CacheDir: t.TempDir()},
		Fixtures: config.FixturesConfig{Dir: fixturesDir},
	}

	_, _, err := buildStartupApp(cfg, startupDeps{
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
		Specs:    config.SpecsConfig{CacheDir: t.TempDir()},
		Fixtures: config.FixturesConfig{Dir: fixturesDir},
	}

	_, _, err := buildStartupApp(cfg, startupDeps{
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
		readFile:  os.ReadFile,
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
	}, specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator())

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
		readFile:  os.ReadFile,
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
		readFile:  os.ReadFile,
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
