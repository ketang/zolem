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
	"time"

	"zolem.dev/zolem/internal/config"
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/ollama"
	"zolem.dev/zolem/internal/provider/anthropic"
	"zolem.dev/zolem/internal/provider/gemini"
	"zolem.dev/zolem/internal/provider/openai"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/router"
	runtimecfg "zolem.dev/zolem/internal/runtime"
	"zolem.dev/zolem/internal/specs"
)

type contractLoader interface {
	LoadFallback(source specs.ContractSource) (specs.NormalizedSchema, error)
	Refresh(source specs.ContractSource) (specs.NormalizedSchema, error)
}

type specFetcher interface {
	Get(provider, version string) ([]byte, error)
}

type textGenerator interface {
	Generate(context.Context, string) (string, error)
}

type startupDeps struct {
	loadConfig        func(string) (*config.Config, error)
	newValidator      func() *specs.Validator
	contractRegistry  func() specs.Registry
	newContractLoader func(cacheDir string) contractLoader
	newFetcher        func(cacheDir string, sources map[string]string) specFetcher
	newRunner         func() *fixture.Runner
	newLorem          func() *response.LoremGenerator
	newFaker          func() *response.FakerGenerator
	newOllamaClient   func(context.Context, config.OllamaConfig) (textGenerator, []string)
	readFile          func(string) ([]byte, error)
	listen            func(addr string, handler http.Handler) error
	listenTLS         func(addr, certFile, keyFile string, handler http.Handler) error
	logf              func(string, ...any)
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
	if d.loadConfig == nil {
		d.loadConfig = config.Load
	}
	if d.newValidator == nil {
		d.newValidator = specs.NewValidator
	}
	if d.contractRegistry == nil {
		d.contractRegistry = specs.DefaultRegistry
	}
	if d.newContractLoader == nil {
		d.newContractLoader = func(cacheDir string) contractLoader {
			return specs.NewContractLoader(cacheDir)
		}
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
	if d.newOllamaClient == nil {
		d.newOllamaClient = func(ctx context.Context, cfg config.OllamaConfig) (textGenerator, []string) {
			if !cfg.IsEnabled() {
				return nil, nil
			}
			return ollama.Detect(ctx, ollama.Config{
				BinaryPath: cfg.BinaryPath,
				Model:      cfg.Model,
			})
		}
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

func run(cfgPath string, deps startupDeps) error {
	deps = deps.withDefaults()

	cfg, err := deps.loadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, warnings, err := buildStartupApp(cfg, deps)
	if err != nil {
		return err
	}
	defer app.close()

	for _, warning := range warnings {
		deps.logf("warn: %s", warning)
	}

	deps.logf("zolem listening on %s", cfg.Server.Addr)
	if cfg.Server.TLS.Cert != "" {
		return deps.listenTLS(cfg.Server.Addr, cfg.Server.TLS.Cert, cfg.Server.TLS.Key, app.handler)
	}
	return deps.listen(cfg.Server.Addr, app.handler)
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

func buildStartupApp(cfg *config.Config, deps startupDeps) (*startupApp, []string, error) {
	deps = deps.withDefaults()

	validator := deps.newValidator()
	warnings := loadContracts(validator, deps.contractRegistry(), deps.newContractLoader(cfg.Specs.CacheDir))

	runner := deps.newRunner()
	fixtures, fixtureWarnings, err := loadFixtures(cfg, runner, deps.readFile)
	warnings = append(warnings, fixtureWarnings...)
	if err != nil {
		runner.Close()
		return nil, warnings, err
	}

	matcher := fixture.NewMatcher(runner, fixtures)
	ollamaClient, ollamaWarnings := deps.newOllamaClient(context.Background(), cfg.Ollama)
	warnings = append(warnings, ollamaWarnings...)
	handler := buildHandler(cfg.Routes, validator, matcher, selectGenerator(cfg.Mode, deps), ollamaClient)

	ctx, cancel := context.WithCancel(context.Background())
	refreshDone := startRefreshLoop(ctx, cfg.Specs.RefreshInterval, deps.contractRegistry(), deps.newContractLoader(cfg.Specs.CacheDir), validator, deps.logf)

	var watchDone <-chan struct{}
	if cfg.Fixtures.Watch && cfg.Fixtures.Dir != "" {
		reloadFn := func() ([]fixture.Fixture, error) {
			reloaded, _, err := loadFixtures(cfg, runner, deps.readFile)
			return reloaded, err
		}
		var watchErr error
		watchDone, watchErr = fixture.StartWatcher(ctx, fixture.WatcherConfig{
			Dir:     cfg.Fixtures.Dir,
			Matcher: matcher,
			Reload:  reloadFn,
			Logf:    deps.logf,
		})
		if watchErr != nil {
			warnings = append(warnings, fmt.Sprintf("fixture watcher failed to start: %v", watchErr))
		}
	}

	return &startupApp{
		handler: handler,
		close: func() {
			cancel()
			<-refreshDone
			if watchDone != nil {
				<-watchDone
			}
			runner.Close()
		},
	}, warnings, nil
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
	cfg := &config.Config{
		Mode:     "fixture",
		Fixtures: config.FixturesConfig{Dir: fixturesDir},
	}
	return loadFixtures(cfg, runner, readFile)
}

func loadContracts(validator *specs.Validator, registry specs.Registry, loader contractLoader) []string {
	var warnings []string
	for _, source := range registry.List() {
		fallback, err := loader.LoadFallback(source)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to load fallback contract %s: %v", source.Key(), err))
		} else if err := validator.LoadNormalized(source.Provider, source.Version, fallback); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to compile fallback contract %s: %v", source.Key(), err))
		}

		if !source.HasRemote() {
			continue
		}

		normalized, err := loader.Refresh(source)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to refresh contract %s: %v", source.Key(), err))
			continue
		}
		if err := validator.LoadNormalized(source.Provider, source.Version, normalized); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to compile refreshed contract %s: %v", source.Key(), err))
		}
	}
	return warnings
}

func startRefreshLoop(ctx context.Context, interval time.Duration, registry specs.Registry, loader contractLoader, validator *specs.Validator, logf func(string, ...any)) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshContracts(registry, loader, validator, logf)
			}
		}
	}()
	return done
}

func refreshContracts(registry specs.Registry, loader contractLoader, validator *specs.Validator, logf func(string, ...any)) {
	for _, source := range registry.List() {
		if !source.HasRemote() {
			continue
		}
		normalized, err := loader.Refresh(source)
		if err != nil {
			logf("spec refresh failed for %s: %v", source.Key(), err)
			continue
		}
		if err := validator.LoadNormalized(source.Provider, source.Version, normalized); err != nil {
			logf("spec refresh compile failed for %s: %v", source.Key(), err)
			continue
		}
		logf("spec refreshed: %s", source.Key())
	}
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

func loadFixtures(cfg *config.Config, runner *fixture.Runner, readFile func(string) ([]byte, error)) ([]fixture.Fixture, []string, error) {
	if cfg.Mode != "fixture" {
		return nil, nil, nil
	}

	if cfg.Fixtures.Dir == "" {
		return nil, nil, nil
	}

	loader := fixture.NewLoader(cfg.Fixtures.Dir)
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

func buildHandler(routes []config.RouteConfig, validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, ollamaClient textGenerator) http.Handler {
	anthropicH := anthropic.NewHandler(validator, matcher, generator, ollamaClient)
	openaiH := openai.NewHandler(validator, matcher, generator, ollamaClient)
	geminiH := gemini.NewHandler(validator, matcher, generator, ollamaClient)
	r := router.New(routes)

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		routeCtx, ok := r.Match(req.Host)
		if !ok {
			writeZolemError(w, "no route matched host: "+req.Host)
			return
		}

		ctx := context.WithValue(req.Context(), router.LabelsKey{}, routeCtx.Labels)
		req = req.WithContext(ctx)

		switch routeCtx.Provider {
		case "anthropic":
			anthropicH.ServeHTTP(w, req)
		case "openai":
			openaiH.ServeHTTP(w, req)
		case "gemini":
			geminiH.ServeHTTP(w, req)
		default:
			writeZolemError(w, "unknown provider: "+routeCtx.Provider)
		}
	})
}

func buildLocalHandler(listenerRuntime runtimecfg.ListenerRuntime, validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator) http.Handler {
	anthropicH := anthropic.NewHandler(validator, matcher, generator, nil)
	openaiH := openai.NewHandler(validator, matcher, generator, nil)
	geminiH := gemini.NewHandler(validator, matcher, generator, nil)

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

func selectGenerator(mode string, deps startupDeps) response.Generator {
	generator, err := generatorForBackend(mode, deps)
	if err != nil {
		return deps.newLorem()
	}
	return generator
}

func generatorForBackend(backend string, deps startupDeps) (response.Generator, error) {
	switch backend {
	case "", "lorem":
		return deps.newLorem(), nil
	case "faker":
		return deps.newFaker(), nil
	case runtimecfg.BackendFixture:
		return deps.newLorem(), nil
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
