package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/provider/anthropic"
	"github.com/ketang/zolem/internal/provider/gemini"
	"github.com/ketang/zolem/internal/provider/openai"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/specs"
	"github.com/ketang/zolem/internal/wasmgen"
	"github.com/ketang/zolem/internal/zolemerr"
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
	// setListenerAddr updates the bound address reported by the /_zolem/state
	// endpoint. It is called after the server binds, so state reflects the
	// resolved host:port rather than the pre-bind request addr (e.g. :0).
	setListenerAddr func(addr string)
}

type localTLSConfig struct {
	CertFile string
	KeyFile  string
}

type localOptions struct {
	Addr                       string
	Provider                   string
	Profile                    string
	Backend                    string
	ErrorType                  string
	FixturesDir                string
	TLS                        localTLSConfig
	CallsFile                  string
	RecordRequestBodyCapBytes  int
	RecordResponseBodyCapBytes int
	RecordStreamEventCap       int
}

// scanFetcher is a no-op specFetcher for scan/test startup deps. Every lookup
// returns no data and no error, so spec loading performs no network or
// filesystem access.
type scanFetcher struct{}

func (scanFetcher) Get(string, string) ([]byte, error) { return nil, nil }

// newScanStartupDeps returns startupDeps wired for scan/test execution: the
// listeners never bind a socket (they accept the handler and return nil), spec
// fetching is a bounded no-op, and the deterministic response generators are
// used. This lets Shatter exercise local runtime startup without starting real
// listeners or touching persistent resources. The remaining fields are left
// nil and filled by withDefaults with their in-memory production defaults
// (validator, fixture runner, file reads). Production startup is unaffected: it
// constructs startupDeps{} and relies on withDefaults.
func newScanStartupDeps() startupDeps {
	return startupDeps{
		newFetcher: func(string, map[string]string) specFetcher { return scanFetcher{} },
		newLorem:   response.NewLoremGenerator,
		newFaker:   response.NewFakerGenerator,
		listen:     func(string, http.Handler) error { return nil },
		listenTLS:  func(string, string, string, http.Handler) error { return nil },
		logf:       func(string, ...any) {},
	}
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

	var recorder Recorder = noopRecorder{}
	if opts.CallsFile != "" {
		jsonl, err := newJSONLRecorder(opts.CallsFile)
		if err != nil {
			return err
		}
		defer jsonl.Close()
		recorder = jsonl
	}

	caps := RecordCaps{
		RequestBodyCapBytes:  opts.RecordRequestBodyCapBytes,
		ResponseBodyCapBytes: opts.RecordResponseBodyCapBytes,
		StreamEventCap:       opts.RecordStreamEventCap,
	}

	app, warnings, err := buildLocalStartupAppWithRecorder(opts, recorder, caps, deps)
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

	return buildLocalStartupAppForRuntime(listenerRuntime, opts.FixturesDir, runtimecfg.NewProfileCounters(), nil, RecordCaps{}, deps)
}

func buildLocalStartupAppWithRecorder(opts localOptions, recorder Recorder, caps RecordCaps, deps startupDeps) (*startupApp, []string, error) {
	deps = deps.withDefaults()

	listenerRuntime, err := opts.runtime()
	if err != nil {
		return nil, nil, err
	}

	if caps.RequestBodyCapBytes == 0 && caps.ResponseBodyCapBytes == 0 && caps.StreamEventCap == 0 {
		caps = DefaultRecordCaps()
	} else {
		defaults := DefaultRecordCaps()
		if caps.RequestBodyCapBytes == 0 {
			caps.RequestBodyCapBytes = defaults.RequestBodyCapBytes
		}
		if caps.ResponseBodyCapBytes == 0 {
			caps.ResponseBodyCapBytes = defaults.ResponseBodyCapBytes
		}
		if caps.StreamEventCap == 0 {
			caps.StreamEventCap = defaults.StreamEventCap
		}
	}

	return buildLocalStartupAppForRuntime(listenerRuntime, opts.FixturesDir, runtimecfg.NewProfileCounters(), recorder, caps, deps)
}

func buildLocalStartupAppForRuntime(listenerRuntime runtimecfg.ListenerRuntime, fixturesDir string, counters *runtimecfg.ProfileCounters, recorder Recorder, caps RecordCaps, deps startupDeps) (*startupApp, []string, error) {
	deps = deps.withDefaults()
	if counters == nil {
		counters = runtimecfg.NewProfileCounters()
	}
	sequenceCounters := fixture.NewSequenceCounters()

	validator := deps.newValidator()
	warnings := loadSpecs(validator, deps.newFetcher(filepath.Join(os.TempDir(), "zolem-specs"), map[string]string{}), listenerRuntime.Spec.Provider)

	runner := deps.newRunner()
	fixtures, selector, fixtureWarnings, err := loadLocalFixtures(listenerRuntime, fixturesDir, runner, deps.readFile, sequenceCounters)
	warnings = append(warnings, fixtureWarnings...)
	if err != nil {
		runner.Close()
		return nil, warnings, err
	}

	matcher := fixture.NewMatcher(runner, fixtures, selector)
	generator, err := generatorForBackend(listenerRuntime.Profile.Backend, deps)
	if err != nil {
		runner.Close()
		return nil, warnings, err
	}
	wasmGenerator, err := wasmGeneratorForProfile(listenerRuntime.Profile)
	if err != nil {
		runner.Close()
		return nil, warnings, err
	}

	runtimePtr := &atomic.Pointer[runtimecfg.ListenerRuntime]{}
	rt := listenerRuntime
	runtimePtr.Store(&rt)

	handler := buildLocalHandler(runtimePtr, counters, validator, matcher, generator, wasmGenerator)
	if recorder != nil {
		handler = recordingMiddleware(recorder, caps)(handler)
	}
	return &startupApp{
		handler: handler,
		close: func() {
			runner.Close()
			if wasmGenerator != nil {
				_ = wasmGenerator.Close(context.Background())
			}
		},
		setListenerAddr: func(addr string) {
			updated := *runtimePtr.Load()
			updated.Spec.Addr = addr
			runtimePtr.Store(&updated)
		},
	}, warnings, nil
}

func loadLocalFixtures(listenerRuntime runtimecfg.ListenerRuntime, fixturesDir string, runner *fixture.Runner, readFile func(string) ([]byte, error), sequenceCounters *fixture.SequenceCounters) ([]fixture.Fixture, fixture.Selector, []string, error) {
	if listenerRuntime.Profile.Backend != runtimecfg.BackendFixture {
		return nil, nil, nil, nil
	}
	if fixturesDir == "" {
		return nil, nil, nil, fmt.Errorf("local fixture backend requires -local-fixtures-dir")
	}
	if listenerRuntime.Profile.FixtureNamespace != "" {
		fixturesDir = filepath.Join(fixturesDir, filepath.FromSlash(listenerRuntime.Profile.FixtureNamespace))
	}
	return loadFixtures(fixturesDir, listenerRuntime, runner, readFile, sequenceCounters)
}

// loadSpecs loads the request schema for each (provider, version) pair the
// served provider validates against. The vendored snapshots (see
// specs.VendoredFallbacks) cover every supported provider, so under zolem's
// no-egress posture this performs no network access. Only the served
// provider's schemas are loaded — an anthropic listener has no use for the
// gemini schema. Load failures are surfaced as warnings; missing schemas are
// also reflected by the /_zolem/state endpoint.
func loadSpecs(validator *specs.Validator, fetcher specFetcher, servedProvider string) []string {
	var warnings []string
	for _, key := range providerSpecKeys(servedProvider) {
		provider, version := splitKey(key)
		data, err := fetcher.Get(provider, version)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to load spec %s: %v", key, err))
			continue
		}
		if err := specs.LoadProviderSchema(validator, provider, version, data); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to load spec %s: %v", key, err))
		}
	}
	return warnings
}

func loadFixtures(fixturesDir string, listenerRuntime runtimecfg.ListenerRuntime, runner *fixture.Runner, readFile func(string) ([]byte, error), sequenceCounters *fixture.SequenceCounters) ([]fixture.Fixture, fixture.Selector, []string, error) {
	if fixturesDir == "" {
		return nil, nil, nil, nil
	}

	loader := fixture.NewLoader(fixturesDir).WithSequenceCounters(sequenceCounters).WithRunner(runner)
	fixtures, selector, err := loader.Load()
	if err != nil {
		return nil, nil, nil, err
	}

	legacy, isLegacy := selector.(*fixture.LegacySelector)
	var warnings []string
	for i := range fixtures {
		if err := fixture.ValidateTemplate(fixtures[i], fixture.ValidationInput{Runtime: fixture.RuntimeContext(listenerRuntime)}); err != nil {
			return nil, nil, warnings, fmt.Errorf("validate response for fixture %q: %w", fixtures[i].ID, err)
		}
		if isLegacy && !legacy.HasMatcher(fixtures[i]) {
			warnings = append(warnings, fmt.Sprintf("fixture %q has no matcher - will never match", fixtures[i].ID))
		}
		if isLegacy {
			if legacy.HasCEL(fixtures[i].ID) {
				warnings = append(warnings, fmt.Sprintf(
					"fixture %q: match.cel is deprecated; migrate to fixtures.yaml — see docs/fixture-authoring.md", fixtures[i].ID))
			}
			if fixtures[i].WASMPath != "" {
				warnings = append(warnings, fmt.Sprintf(
					"fixture %q: match.wasm is deprecated; migrate to fixtures.yaml or a namespace-level selector.wasm — see docs/fixture-authoring.md", fixtures[i].ID))
			}
		}
		if fixtures[i].WASMPath == "" {
			continue
		}

		wasmBytes, err := readFile(fixtures[i].WASMPath)
		if err != nil {
			return nil, nil, warnings, fmt.Errorf("read wasm for fixture %q: %w", fixtures[i].ID, err)
		}

		mod, err := runner.CompileWASM(context.Background(), wasmBytes)
		if err != nil {
			return nil, nil, warnings, fmt.Errorf("compile wasm for fixture %q: %w", fixtures[i].ID, err)
		}
		fixtures[i].Module = &mod
	}

	return fixtures, selector, warnings, nil
}

func buildLocalHandler(runtimePtr *atomic.Pointer[runtimecfg.ListenerRuntime], counters *runtimecfg.ProfileCounters, validator *specs.Validator, matcher *fixture.Matcher, generator response.Generator, wasmGenerator *wasmgen.Generator) http.Handler {
	anthropicH := anthropic.NewHandler(validator, matcher, generator, &ollamaHTTPAdapter{}, wasmGenerator)
	openaiH := openai.NewHandler(validator, matcher, generator, &ollamaHTTPAdapter{}, wasmGenerator)
	geminiH := gemini.NewHandler(validator, matcher, generator, &ollamaHTTPAdapter{}, wasmGenerator)

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		listenerRuntime := *runtimePtr.Load()
		if req.Method == http.MethodGet && req.URL.Path == "/_zolem/health" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == "/_zolem/state" {
			writeLocalState(w, listenerRuntime, validator)
			return
		}

		ctx := runtimecfg.WithListenerRuntime(req.Context(), listenerRuntime)
		ctx = runtimecfg.WithProfileCounters(ctx, counters)
		profileRequest := counters.IncrementProfileRequest(listenerRuntime.Profile.Name)
		ctx = runtimecfg.WithProfileRequestSequence(ctx, profileRequest)
		req = req.WithContext(ctx)

		switch listenerRuntime.Spec.Provider {
		case "anthropic":
			anthropicH.ServeHTTP(w, req)
		case "openai":
			openaiH.ServeHTTP(w, req)
		case "gemini":
			geminiH.ServeHTTP(w, req)
		default:
			zolemerr.Write(w, "unknown provider: "+listenerRuntime.Spec.Provider)
		}
	})
}

func generatorForBackend(backend string, deps startupDeps) (response.Generator, error) {
	switch backend {
	case "", "lorem":
		return deps.newLorem(), nil
	case "faker":
		return deps.newFaker(), nil
	case runtimecfg.BackendFixture, runtimecfg.BackendError, runtimecfg.BackendWASM:
		return deps.newLorem(), nil
	case runtimecfg.BackendOllama:
		return deps.newLorem(), nil // generator unused for ollama backend; handler dispatches to HTTP client
	default:
		return nil, fmt.Errorf("unsupported local backend %q", backend)
	}
}

func wasmGeneratorForProfile(profile runtimecfg.RuntimeProfile) (*wasmgen.Generator, error) {
	if profile.Backend != runtimecfg.BackendWASM {
		return nil, nil
	}
	timeout := wasmGenerateTimeout(profile)
	wasmBytes, err := base64.StdEncoding.DecodeString(profile.WASMModuleBase64)
	if err != nil {
		return nil, fmt.Errorf("decode wasm_module_base64: %w", err)
	}
	return wasmgen.Compile(wasmBytes, timeout)
}

func wasmGenerateTimeout(profile runtimecfg.RuntimeProfile) time.Duration {
	if profile.WASMGenerateTimeoutMS == 0 {
		return 100 * time.Millisecond
	}
	return time.Duration(profile.WASMGenerateTimeoutMS) * time.Millisecond
}

func splitKey(key string) (string, string) {
	provider, version, _ := strings.Cut(key, ":")
	return provider, version
}

// providerSpecKeys returns the "provider:version" schema keys a listener for
// the given provider validates requests against. Gemini serves both the v1 and
// v1beta API surfaces, so it validates against both.
func providerSpecKeys(provider string) []string {
	switch provider {
	case "anthropic":
		return []string{"anthropic:v1"}
	case "openai":
		return []string{"openai:v1"}
	case "gemini":
		return []string{"gemini:v1", "gemini:v1beta"}
	default:
		return nil
	}
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
	if err := runtimecfg.ValidateLoopbackAddr(addr); err != nil {
		return runtimecfg.ListenerRuntime{}, fmt.Errorf("invalid local addr %q: %w", addr, err)
	}
	profile := o.Profile
	if profile == "" {
		profile = "default"
	}
	backend := o.Backend
	if backend == "" {
		backend = "lorem"
	}

	runtimeProfile := runtimecfg.RuntimeProfile{
		Name:      profile,
		Backend:   backend,
		ErrorType: o.ErrorType,
	}
	if err := runtimecfg.ValidateProfile(runtimeProfile); err != nil {
		return runtimecfg.ListenerRuntime{}, fmt.Errorf("invalid local profile: %w", err)
	}

	return runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     providerListenerName(o.Provider, profile),
			Addr:     addr,
			Provider: o.Provider,
			Profile:  profile,
			TLS:      o.TLS.enabled(),
		},
		Profile: runtimeProfile,
	}, nil
}

func providerListenerName(provider, profile string) string {
	return provider + "-" + profile
}

func writeLocalState(w http.ResponseWriter, listenerRuntime runtimecfg.ListenerRuntime, validator *specs.Validator) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"provider":          listenerRuntime.Spec.Provider,
		"profile":           listenerRuntime.Spec.Profile,
		"backend":           listenerRuntime.Profile.Backend,
		"listener":          listenerRuntime.Spec.Addr,
		"tls":               listenerRuntime.Spec.TLS,
		"schemas_loaded":    validator.Schemas(),
		"schema_validation": schemaValidationStatus(validator, listenerRuntime.Spec.Provider),
	})
}

// schemaValidationStatus reports whether request-schema validation is active
// for the served provider: "enabled" when every schema it needs is loaded,
// "unavailable: <keys>" when one or more are missing, or "unsupported" for a
// provider with no known schema. It backs the missing-schema visibility
// requirement of the /_zolem/state endpoint.
func schemaValidationStatus(validator *specs.Validator, provider string) string {
	keys := providerSpecKeys(provider)
	if len(keys) == 0 {
		return "unsupported"
	}
	var missing []string
	for _, key := range keys {
		p, v := splitKey(key)
		if !validator.Has(p, v) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return "unavailable: " + strings.Join(missing, ", ")
	}
	return "enabled"
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
