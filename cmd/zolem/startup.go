package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/ollama"
	"zolem.dev/zolem/internal/provider/anthropic"
	"zolem.dev/zolem/internal/provider/gemini"
	"zolem.dev/zolem/internal/provider/openai"
	"zolem.dev/zolem/internal/response"
	runtimecfg "zolem.dev/zolem/internal/runtime"
	"zolem.dev/zolem/internal/specs"
)

type ollamaHTTPAdapter struct{}

func (a *ollamaHTTPAdapter) NonStreaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string) (string, error) {
	return ollama.HTTPChatCompletion(ctx, upstream, messages, model)
}

func (a *ollamaHTTPAdapter) Streaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string, fn func(delta string) error) error {
	return ollama.HTTPChatCompletionStream(ctx, upstream, messages, model, fn)
}

type specFetcher interface {
	Get(provider, version string) ([]byte, error)
}

type startupDeps struct {
	newValidator func() *specs.Validator
	newFetcher   func(cacheDir string, sources map[string]string) specFetcher
	newRunner    func() *fixture.Runner
	newLorem     func() *response.LoremGenerator
	newFaker     func() *response.FakerGenerator
	readFile     func(string) ([]byte, error)
	listen       func(addr string, handler http.Handler) error
	listenTLS    func(addr, certFile, keyFile string, handler http.Handler) error
	logf         func(string, ...any)
}

type startupApp struct {
	handler http.Handler
	close   func()
}

type localTLSConfig struct {
	CertFile string
	KeyFile  string
}

type localOptions struct {
	Addr        string
	Provider    string
	Profile     string
	Backend     string
	FixturesDir string
	TLS         localTLSConfig
}

func (d startupDeps) withDefaults() startupDeps {
	if d.newValidator == nil {
		d.newValidator = specs.NewValidator
	}
	if d.newFetcher == nil {
		d.newFetcher = func(cacheDir string, sources map[string]string) specFetcher {
			return specs.NewFetcherWithFallback(cacheDir, sources, specs.VendoredFallbacks())
		}
	}
	if d.newRunner == nil {
		d.newRunner = fixture.NewRunner
	}
	if d.newLorem == nil {
		d.newLorem = response.NewLoremGenerator
	}
	if d.newFaker == nil {
		d.newFaker = response.NewFakerGenerator
	}
	if d.readFile == nil {
		d.readFile = os.ReadFile
	}
	if d.listen == nil {
		d.listen = http.ListenAndServe
	}
	if d.listenTLS == nil {
		d.listenTLS = http.ListenAndServeTLS
	}
	if d.logf == nil {
		d.logf = log.Printf
	}
	return d
}

func runLocal(opts localOptions, deps startupDeps) error {
	deps = deps.withDefaults()

	if err := opts.TLS.validate(); err != nil {
		return err
	}

	listenerRuntime, err := opts.runtime()
	if err != nil {
		return err
	}

	app, warnings, err := buildLocalStartupApp(opts, deps)
	if err != nil {
		return err
	}
	defer app.close()

	for _, warning := range warnings {
		deps.logf("warn: %s", warning)
	}

	deps.logf("zolem local listener on %s for %s/%s", listenerRuntime.Spec.Addr, listenerRuntime.Spec.Provider, listenerRuntime.Spec.Profile)
	if opts.TLS.enabled() {
		return deps.listenTLS(listenerRuntime.Spec.Addr, opts.TLS.CertFile, opts.TLS.KeyFile, app.handler)
	}
	return deps.listen(listenerRuntime.Spec.Addr, app.handler)
}

func buildLocalStartupApp(opts localOptions, deps startupDeps) (*startupApp, []string, error) {
	deps = deps.withDefaults()

	listenerRuntime, err := opts.runtime()
	if err != nil {
		return nil, nil, err
	}

	return buildLocalStartupAppForRuntime(listenerRuntime, opts.FixturesDir, deps)
}

func buildLocalStartupAppForRuntime(listenerRuntime runtimecfg.ListenerRuntime, fixturesDir string, deps startupDeps) (*startupApp, []string, error) {
	deps = deps.withDefaults()

	validator := deps.newValidator()
	warnings := loadSpecs(validator, deps.newFetcher(filepath.Join(os.TempDir(), "zolem-specs"), map[string]string{}))

	runner := deps.newRunner()
	fixtures, fixtureWarnings, err := loadLocalFixtures(listenerRuntime.Profile.Backend, fixturesDir, listenerRuntime.Profile.FixtureNamespace, runner, deps.readFile)
	warnings = append(warnings, fixtureWarnings...)
	if err != nil {
		runner.Close()
		return nil, warnings, err
	}

	matcher := fixture.NewMatcher(runner, fixtures)
	generator, err := generatorForBackend(listenerRuntime.Profile.Backend, deps)
	if err != nil {
		runner.Close()
		return nil, warnings, err
	}

	handler := buildLocalHandler(listenerRuntime, validator, matcher, generator)
	return &startupApp{
		handler: handler,
		close:   runner.Close,
	}, warnings, nil
}

func loadLocalFixtures(backend, fixturesDir, fixtureNamespace string, runner *fixture.Runner, readFile func(string) ([]byte, error)) ([]fixture.Fixture, []string, error) {
	if backend != runtimecfg.BackendFixture {
		return nil, nil, nil
	}
	if fixturesDir == "" {
		return nil, nil, fmt.Errorf("local fixture backend requires -local-fixtures-dir")
	}
	if fixtureNamespace != "" {
		fixturesDir = filepath.Join(fixturesDir, filepath.FromSlash(fixtureNamespace))
	}
	return loadFixtures(fixturesDir, runner, readFile)
}

func loadSpecs(validator *specs.Validator, fetcher specFetcher) []string {
	var warnings []string
	for _, key := range specKeys() {
		provider, version := splitKey(key)
		data, err := fetcher.Get(provider, version)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to fetch spec %s: %v", key, err))
			continue
		}
		if err := specs.LoadProviderSchema(validator, provider, version, data); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to load spec %s: %v", key, err))
		}
	}
	return warnings
}

func loadFixtures(fixturesDir string, runner *fixture.Runner, readFile func(string) ([]byte, error)) ([]fixture.Fixture, []string, error) {
	if fixturesDir == "" {
		return nil, nil, nil
	}

	loader := fixture.NewLoader(fixturesDir)
	fixtures, err := loader.Load()
	if err != nil {
		return nil, nil, err
	}

	var warnings []string
	for i := range fixtures {
		if fixtures[i].WASMPath == "" {
			warnings = append(warnings, fmt.Sprintf("fixture %q has no match.wasm - will never match", fixtures[i].ID))
			continue
		}

		wasmBytes, err := readFile(fixtures[i].WASMPath)
		if err != nil {
			return nil, warnings, fmt.Errorf("read wasm for fixture %q: %w", fixtures[i].ID, err)
		}

		mod, err := runner.CompileWASM(context.Background(), wasmBytes)
		if err != nil {
			return nil, warnings, fmt.Errorf("compile wasm for fixture %q: %w", fixtures[i].ID, err)
		}
		fixtures[i].Module = &mod
		warnings = append(warnings, fmt.Sprintf("loaded fixture: %s", fixtures[i].ID))
	}

	return fixtures, warnings, nil
}

func buildLocalHandler(listenerRuntime runtimecfg.ListenerRuntime, validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator) http.Handler {
	anthropicH := anthropic.NewHandler(validator, matcher, generator, nil, &ollamaHTTPAdapter{})
	openaiH := openai.NewHandler(validator, matcher, generator, nil, &ollamaHTTPAdapter{})
	geminiH := gemini.NewHandler(validator, matcher, generator, nil, &ollamaHTTPAdapter{})

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == "/_zolem/health" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == "/_zolem/state" {
			writeLocalState(w, listenerRuntime)
			return
		}

		req = req.WithContext(runtimecfg.WithListenerRuntime(req.Context(), listenerRuntime))

		switch listenerRuntime.Spec.Provider {
		case "anthropic":
			anthropicH.ServeHTTP(w, req)
		case "openai":
			openaiH.ServeHTTP(w, req)
		case "gemini":
			geminiH.ServeHTTP(w, req)
		default:
			writeZolemError(w, "unknown provider: "+listenerRuntime.Spec.Provider)
		}
	})
}

func generatorForBackend(backend string, deps startupDeps) (response.Generator, error) {
	switch backend {
	case "", "lorem":
		return deps.newLorem(), nil
	case "faker":
		return deps.newFaker(), nil
	case runtimecfg.BackendFixture, runtimecfg.BackendError:
		return deps.newLorem(), nil
	case runtimecfg.BackendOllama:
		return deps.newLorem(), nil // generator unused for ollama backend; handler dispatches to HTTP client
	default:
		return nil, fmt.Errorf("unsupported local backend %q", backend)
	}
}

func writeZolemError(w http.ResponseWriter, message string) {
	w.Header().Set("X-Zolem-Error", "true")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"zolem_error": message})
}

func splitKey(key string) (string, string) {
	if i := strings.IndexByte(key, ':'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

func specSourceMap() map[string]string {
	return map[string]string{
		"openai:v1":     "https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml",
		"openrouter:v1": "https://openrouter.ai/openapi.yaml",
		"gemini:v1":     "https://generativelanguage.googleapis.com/$discovery/rest?version=v1",
		"gemini:v1beta": "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta",
	}
}

func specKeys() []string {
	return []string{"anthropic:v1", "openai:v1", "openrouter:v1", "gemini:v1", "gemini:v1beta"}
}

func (o localOptions) runtime() (runtimecfg.ListenerRuntime, error) {
	if err := o.TLS.validate(); err != nil {
		return runtimecfg.ListenerRuntime{}, err
	}
	if o.Provider != "anthropic" && o.Provider != "openai" && o.Provider != "gemini" {
		return runtimecfg.ListenerRuntime{}, fmt.Errorf("invalid local provider %q: must be anthropic, openai, or gemini", o.Provider)
	}

	addr := o.Addr
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	profile := o.Profile
	if profile == "" {
		profile = "default"
	}
	backend := o.Backend
	if backend == "" {
		backend = "lorem"
	}

	return runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     providerListenerName(o.Provider, profile),
			Addr:     addr,
			Provider: o.Provider,
			Profile:  profile,
			TLS:      o.TLS.enabled(),
		},
		Profile: runtimecfg.RuntimeProfile{
			Name:    profile,
			Backend: backend,
		},
	}, nil
}

func providerListenerName(provider, profile string) string {
	return provider + "-" + profile
}

func writeLocalState(w http.ResponseWriter, listenerRuntime runtimecfg.ListenerRuntime) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"provider": listenerRuntime.Spec.Provider,
		"profile":  listenerRuntime.Spec.Profile,
		"backend":  listenerRuntime.Profile.Backend,
		"listener": listenerRuntime.Spec.Addr,
		"tls":      listenerRuntime.Spec.TLS,
	})
}

func (c localTLSConfig) enabled() bool {
	return c.CertFile != "" || c.KeyFile != ""
}

func (c localTLSConfig) validate() error {
	if c.CertFile == "" && c.KeyFile == "" {
		return nil
	}
	if c.CertFile == "" || c.KeyFile == "" {
		return fmt.Errorf("local TLS requires both cert and key")
	}
	return nil
}
