package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"zolem.dev/zolem/internal/config"
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/provider/anthropic"
	"zolem.dev/zolem/internal/provider/gemini"
	"zolem.dev/zolem/internal/provider/openai"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/router"
	"zolem.dev/zolem/internal/specs"
)

type specFetcher interface {
	Get(provider, version string) ([]byte, error)
}

type startupDeps struct {
	loadConfig   func(string) (*config.Config, error)
	newValidator func() *specs.Validator
	newFetcher   func(cacheDir string, sources map[string]string) specFetcher
	newRunner    func() *fixture.Runner
	newLorem     func() *response.LoremGenerator
	readFile     func(string) ([]byte, error)
	listen       func(addr string, handler http.Handler) error
	listenTLS    func(addr, certFile, keyFile string, handler http.Handler) error
	logf         func(string, ...any)
}

type startupApp struct {
	handler http.Handler
	close   func()
}

func (d startupDeps) withDefaults() startupDeps {
	if d.loadConfig == nil {
		d.loadConfig = config.Load
	}
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

func buildStartupApp(cfg *config.Config, deps startupDeps) (*startupApp, []string, error) {
	deps = deps.withDefaults()

	validator := deps.newValidator()
	warnings := loadSpecs(validator, deps.newFetcher(cfg.Specs.CacheDir, specSourceMap()))

	runner := deps.newRunner()
	fixtures, fixtureWarnings, err := loadFixtures(cfg, runner, deps.readFile)
	warnings = append(warnings, fixtureWarnings...)
	if err != nil {
		runner.Close()
		return nil, warnings, err
	}

	matcher := fixture.NewMatcher(runner, fixtures)
	handler := buildHandler(cfg.Routes, validator, matcher, deps.newLorem())

	return &startupApp{
		handler: handler,
		close:   runner.Close,
	}, warnings, nil
}

func loadSpecs(validator *specs.Validator, fetcher specFetcher) []string {
	var warnings []string
	for _, key := range specKeys() {
		provider, version := splitKey(key)
		data, err := fetcher.Get(provider, version)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to fetch spec %s: %v (validation disabled)", key, err))
			continue
		}
		if err := specs.LoadProviderSchema(validator, provider, version, data); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to load spec %s: %v", key, err))
		}
	}
	return warnings
}

func loadFixtures(cfg *config.Config, runner *fixture.Runner, readFile func(string) ([]byte, error)) ([]fixture.Fixture, []string, error) {
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

func buildHandler(routes []config.RouteConfig, validator *specs.Validator, matcher *fixture.Matcher, lorem *response.LoremGenerator) http.Handler {
	anthropicH := anthropic.NewHandler(validator, matcher, lorem)
	openaiH := openai.NewHandler(validator, matcher, lorem)
	geminiH := gemini.NewHandler(validator, matcher, lorem)
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
		"gemini:v1":     "https://generativelanguage.googleapis.com/$discovery/rest?version=v1",
		"gemini:v1beta": "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta",
	}
}

func specKeys() []string {
	return []string{"anthropic:v1", "openai:v1", "gemini:v1", "gemini:v1beta"}
}
