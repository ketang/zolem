# Zolem Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go service that faithfully mocks Anthropic, OpenAI, and Gemini APIs — swappable by hostname change alone — supporting lorem ipsum and fixture-based responses with full SSE streaming fidelity.

**Architecture:** Single binary; virtual host dispatch routes requests to provider packages based on the `Host` header. Shared core handles fixture matching (WASM/wazero), runtime OpenAPI spec validation, and SSE utilities. Each provider package owns its own route registration, request validation, response serialization, and SSE wire format.

**Tech Stack:** Go 1.22+, `go-chi/chi/v5` (routing), `tetratelabs/wazero` (WASM runtime), `santhosh-tekuri/jsonschema/v6` (JSON Schema validation), `fsnotify/fsnotify` (fixture hot-reload), `gopkg.in/yaml.v3` (config).

**Design spec:** `docs/2026-04-02-llm-mock-service-design.md`

---

## File Map

```
zolem/
├── cmd/zolem/
│   └── main.go                          # binary entrypoint
├── internal/
│   ├── config/
│   │   └── config.go                    # Config struct, YAML load, validate
│   ├── router/
│   │   └── router.go                    # virtual host pattern match + label extraction
│   ├── specs/
│   │   ├── fetcher.go                   # HTTP fetch, disk cache, refresh loop
│   │   └── validator.go                 # jsonschema integration, per-(provider,version)
│   ├── response/
│   │   ├── lorem.go                     # lorem ipsum token list + Generate()
│   │   └── sse.go                       # SSE chunk writer, Flusher wrapper, token pacing
│   ├── fixture/
│   │   ├── fixture.go                   # Fixture struct, meta.yaml parsing
│   │   ├── loader.go                    # directory scan, fsnotify hot-reload
│   │   ├── wasm.go                      # wazero runtime, ABI, score execution
│   │   └── matcher.go                   # scoring, candidate selection, fallback
│   └── provider/
│       ├── anthropic/
│       │   ├── handler.go               # chi routes, request dispatch
│       │   ├── types.go                 # request/response Go structs
│       │   ├── errors.go                # provider-exact error helpers
│       │   └── sse.go                   # Anthropic SSE event serialization
│       ├── openai/
│       │   ├── handler.go
│       │   ├── types.go
│       │   ├── errors.go
│       │   └── sse.go
│       └── gemini/
│           ├── handler.go
│           ├── types.go
│           ├── errors.go
│           └── sse.go
├── testdata/
│   └── fixtures/
│       └── sample-anthropic/            # reference fixture used in tests
│           ├── match.wasm               # compiled from testdata/wasm/always_match/main.go
│           ├── response.json
│           └── meta.yaml
├── specs/                               # bundled fallback OpenAPI specs (embedded via go:embed)
│   ├── anthropic-v1.yaml
│   ├── openai-v1.yaml
│   └── gemini-v1beta.yaml
└── go.mod
```

---

## Phase 1 — Foundation

### Task 1: Module scaffold

**Files:**
- Create: `go.mod`
- Create: `cmd/zolem/main.go`

- [x] **Step 1: Initialize the module**

```bash
cd /home/ketan/project/zolem
go mod init github.com/ketang/zolem
```

Expected output: `go: creating new go.mod: module github.com/ketang/zolem`

- [x] **Step 2: Add dependencies**

```bash
go get github.com/go-chi/chi/v5@latest
go get github.com/tetratelabs/wazero@latest
go get github.com/santhosh-tekuri/jsonschema/v6@latest
go get gopkg.in/yaml.v3@latest
go mod tidy
```

- [x] **Step 3: Write the minimal main.go**

```go
// cmd/zolem/main.go
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/ketang/zolem/internal/config"
)

func main() {
	cfgPath := flag.String("config", "zolem.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	log.Printf("zolem listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, nil); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
```

- [x] **Step 4: Verify it compiles (will fail at runtime — config doesn't exist yet)**

```bash
go build ./cmd/zolem/
```

Expected: binary produced, no compile errors.

- [x] **Step 5: Commit**

```bash
git add go.mod go.sum cmd/zolem/main.go
git commit -m "feat: project scaffold with module and main entrypoint"
```

---

### Task 2: Config

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [x] **Step 1: Write the failing test**

```go
// internal/config/config_test.go
package config_test

import (
	"os"
	"testing"

	"github.com/ketang/zolem/internal/config"
)

func TestLoad(t *testing.T) {
	yaml := `
server:
  addr: ":9090"
mode: fixture
specs:
  cache_dir: /tmp/zolem-specs
  refresh_interval: 6h
fixtures:
  dir: /tmp/fixtures
  watch: false
routes:
  - host: "*.api.anthropic.zolem.dev"
    provider: anthropic
    labels:
      tenant: "{1}"
  - host: "*.*.api.anthropic.zolem.dev"
    provider: anthropic
    labels:
      env: "{1}"
      tenant: "{2}"
`
	f, _ := os.CreateTemp(t.TempDir(), "*.yaml")
	f.WriteString(yaml)
	f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("addr: got %q, want %q", cfg.Server.Addr, ":9090")
	}
	if cfg.Mode != "fixture" {
		t.Errorf("mode: got %q, want %q", cfg.Mode, "fixture")
	}
	if len(cfg.Routes) != 2 {
		t.Errorf("routes: got %d, want 2", len(cfg.Routes))
	}
	if cfg.Routes[1].Labels["env"] != "{1}" {
		t.Errorf("label env: got %q, want {1}", cfg.Routes[1].Labels["env"])
	}
}

func TestLoadDefaults(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "*.yaml")
	f.WriteString("server:\n  addr: \":8080\"\n")
	f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Mode != "lorem" {
		t.Errorf("default mode: got %q, want lorem", cfg.Mode)
	}
}
```

- [x] **Step 2: Run to confirm failure**

```bash
go test ./internal/config/ -v
```

Expected: compile error — package does not exist yet.

- [x] **Step 3: Implement config.go**

```go
// internal/config/config.go
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Mode     string         `yaml:"mode"`
	Specs    SpecsConfig    `yaml:"specs"`
	Fixtures FixturesConfig `yaml:"fixtures"`
	Routes   []RouteConfig  `yaml:"routes"`
}

type ServerConfig struct {
	Addr string    `yaml:"addr"`
	TLS  TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

type SpecsConfig struct {
	CacheDir        string        `yaml:"cache_dir"`
	RefreshInterval time.Duration `yaml:"refresh_interval"`
}

type FixturesConfig struct {
	Dir   string `yaml:"dir"`
	Watch bool   `yaml:"watch"`
}

type RouteConfig struct {
	Host     string            `yaml:"host"`
	Provider string            `yaml:"provider"`
	Labels   map[string]string `yaml:"labels"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	cfg := &Config{
		Mode: "lorem",
		Server: ServerConfig{Addr: ":8080"},
		Specs:  SpecsConfig{RefreshInterval: 6 * time.Hour},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Mode != "lorem" && cfg.Mode != "fixture" {
		return nil, fmt.Errorf("invalid mode %q: must be lorem or fixture", cfg.Mode)
	}
	return cfg, nil
}
```

- [x] **Step 4: Run tests**

```bash
go test ./internal/config/ -v
```

Expected: `PASS`

- [x] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: config struct and YAML loader with defaults"
```

---

### Task 3: Virtual host router

**Files:**
- Create: `internal/router/router.go`
- Create: `internal/router/router_test.go`

- [x] **Step 1: Write the failing tests**

```go
// internal/router/router_test.go
package router_test

import (
	"testing"

	"github.com/ketang/zolem/internal/config"
	"github.com/ketang/zolem/internal/router"
)

func routes() []config.RouteConfig {
	return []config.RouteConfig{
		{
			Host:     "*.api.anthropic.zolem.dev",
			Provider: "anthropic",
			Labels:   map[string]string{"tenant": "{1}"},
		},
		{
			Host:     "*.*.api.anthropic.zolem.dev",
			Provider: "anthropic",
			Labels:   map[string]string{"env": "{1}", "tenant": "{2}"},
		},
		{
			Host:     "*.api.openai.zolem.dev",
			Provider: "openai",
			Labels:   map[string]string{"tenant": "{1}"},
		},
	}
}

func TestMatch_SingleWildcard(t *testing.T) {
	r := router.New(routes())
	ctx, ok := r.Match("acme.api.anthropic.zolem.dev")
	if !ok {
		t.Fatal("expected match")
	}
	if ctx.Provider != "anthropic" {
		t.Errorf("provider: got %q, want anthropic", ctx.Provider)
	}
	if ctx.Labels["tenant"] != "acme" {
		t.Errorf("tenant: got %q, want acme", ctx.Labels["tenant"])
	}
}

func TestMatch_DoubleWildcard(t *testing.T) {
	r := router.New(routes())
	ctx, ok := r.Match("prod.acme.api.anthropic.zolem.dev")
	if !ok {
		t.Fatal("expected match")
	}
	if ctx.Labels["env"] != "prod" {
		t.Errorf("env: got %q, want prod", ctx.Labels["env"])
	}
	if ctx.Labels["tenant"] != "acme" {
		t.Errorf("tenant: got %q, want acme", ctx.Labels["tenant"])
	}
}

func TestMatch_NoMatch(t *testing.T) {
	r := router.New(routes())
	_, ok := r.Match("unknown.zolem.dev")
	if ok {
		t.Fatal("expected no match")
	}
}

func TestMatch_FirstWins(t *testing.T) {
	// single wildcard must not match a two-segment host; order matters
	r := router.New(routes())
	ctx, ok := r.Match("acme.api.openai.zolem.dev")
	if !ok {
		t.Fatal("expected match")
	}
	if ctx.Provider != "openai" {
		t.Errorf("provider: got %q, want openai", ctx.Provider)
	}
}
```

- [x] **Step 2: Run to confirm failure**

```bash
go test ./internal/router/ -v
```

Expected: compile error.

- [x] **Step 3: Implement router.go**

```go
// internal/router/router.go
package router

import (
	"strings"

	"github.com/ketang/zolem/internal/config"
)

// LabelsKey is the context key for virtual host labels.
// Defined here so all packages use the same type for context value lookup.
type LabelsKey struct{}

// RouteContext carries the resolved provider and extracted labels for a request.
type RouteContext struct {
	Provider string
	Labels   map[string]string
}

// Router matches incoming Host header values against a configured routing table.
type Router struct {
	routes []config.RouteConfig
}

func New(routes []config.RouteConfig) *Router {
	return &Router{routes: routes}
}

// Match evaluates host against the routing table in order; first match wins.
// Returns the resolved RouteContext and true on match, zero value and false otherwise.
func (r *Router) Match(host string) (RouteContext, bool) {
	// strip port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	hostParts := strings.Split(host, ".")

	for _, route := range r.routes {
		patternParts := strings.Split(route.Host, ".")
		if captures, ok := matchParts(hostParts, patternParts); ok {
			labels := make(map[string]string, len(route.Labels))
			for k, v := range route.Labels {
				// resolve capture references: {1}, {2}, ...
				resolved := v
				for i, cap := range captures {
					placeholder := "{" + string(rune('0'+i+1)) + "}"
					resolved = strings.ReplaceAll(resolved, placeholder, cap)
				}
				labels[k] = resolved
			}
			return RouteContext{Provider: route.Provider, Labels: labels}, true
		}
	}
	return RouteContext{}, false
}

// matchParts attempts to match hostParts against patternParts where "*" is a
// single-label wildcard. Returns captured wildcard values in order.
func matchParts(host, pattern []string) ([]string, bool) {
	if len(host) != len(pattern) {
		return nil, false
	}
	var captures []string
	for i, p := range pattern {
		if p == "*" {
			captures = append(captures, host[i])
		} else if p != host[i] {
			return nil, false
		}
	}
	return captures, true
}
```

- [x] **Step 4: Run tests**

```bash
go test ./internal/router/ -v
```

Expected: `PASS`

- [x] **Step 5: Commit**

```bash
git add internal/router/
git commit -m "feat: virtual host router with wildcard label extraction"
```

---

## Phase 2 — Spec Validation

### Task 4: Spec fetcher

**Files:**
- Create: `internal/specs/fetcher.go`
- Create: `internal/specs/fetcher_test.go`
- Create: `specs/anthropic-v1.yaml` (minimal placeholder — replace with real content from spec sources below)
- Create: `specs/openai-v1.yaml`
- Create: `specs/gemini-v1beta.yaml`

**Spec source URLs** (verify these during implementation — they may redirect or move):
- Anthropic: `https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/main/api.json`
- OpenAI: `https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml`
- Gemini v1beta: `https://raw.githubusercontent.com/googleapis/googleapis/master/google/ai/generativelanguage/v1beta/generativelanguage_v1beta.yaml` (if not found, use Google's discovery endpoint: `https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta`)

- [x] **Step 1: Write failing tests**

```go
// internal/specs/fetcher_test.go
package specs_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ketang/zolem/internal/specs"
)

func TestFetcher_FetchAndCache(t *testing.T) {
	content := `{"openapi":"3.0.0","info":{"title":"Test"},"paths":{}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	sources := map[string]string{
		"test:v1": srv.URL,
	}
	f := specs.NewFetcher(cacheDir, sources)

	data, err := f.Get("test", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != content {
		t.Errorf("data mismatch")
	}

	// verify written to disk
	cached := filepath.Join(cacheDir, "test-v1.json")
	if _, err := os.Stat(cached); os.IsNotExist(err) {
		t.Error("expected cache file to exist")
	}
}

func TestFetcher_FallbackOnNetworkError(t *testing.T) {
	cacheDir := t.TempDir()
	sources := map[string]string{
		"test:v1": "http://localhost:0", // unreachable
	}
	fallback := []byte(`{"openapi":"3.0.0","paths":{}}`)
	f := specs.NewFetcherWithFallback(cacheDir, sources, map[string][]byte{
		"test:v1": fallback,
	})

	data, err := f.Get("test", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != string(fallback) {
		t.Error("expected fallback data")
	}
}

func TestFetcher_ServesFromCacheOnSecondCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	f := specs.NewFetcher(cacheDir, map[string]string{"p:v1": srv.URL})

	f.Get("p", "v1")
	f.Get("p", "v1")

	if calls != 1 {
		t.Errorf("expected 1 HTTP call, got %d", calls)
	}
}
```

- [x] **Step 2: Run to confirm failure**

```bash
go test ./internal/specs/ -run TestFetcher -v
```

Expected: compile error.

- [x] **Step 3: Implement fetcher.go**

```go
// internal/specs/fetcher.go
package specs

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// Fetcher retrieves and caches OpenAPI spec content per (provider, version) pair.
type Fetcher struct {
	cacheDir  string
	sources   map[string]string // key: "provider:version"
	fallbacks map[string][]byte
	mu        sync.RWMutex
	cache     map[string][]byte // in-memory cache
}

func NewFetcher(cacheDir string, sources map[string]string) *Fetcher {
	return NewFetcherWithFallback(cacheDir, sources, nil)
}

func NewFetcherWithFallback(cacheDir string, sources map[string]string, fallbacks map[string][]byte) *Fetcher {
	return &Fetcher{
		cacheDir:  cacheDir,
		sources:   sources,
		fallbacks: fallbacks,
		cache:     make(map[string][]byte),
	}
}

// Get returns the spec for (provider, version). Order: in-memory cache → disk cache → HTTP → fallback.
func (f *Fetcher) Get(provider, version string) ([]byte, error) {
	key := provider + ":" + version

	f.mu.RLock()
	if data, ok := f.cache[key]; ok {
		f.mu.RUnlock()
		return data, nil
	}
	f.mu.RUnlock()

	// disk cache
	diskPath := filepath.Join(f.cacheDir, provider+"-"+version+".json")
	if data, err := os.ReadFile(diskPath); err == nil {
		f.store(key, data)
		return data, nil
	}

	// HTTP fetch
	url, ok := f.sources[key]
	if ok {
		if data, err := fetchURL(url); err == nil {
			os.MkdirAll(f.cacheDir, 0o755)
			os.WriteFile(diskPath, data, 0o644)
			f.store(key, data)
			return data, nil
		}
	}

	// fallback
	if f.fallbacks != nil {
		if data, ok := f.fallbacks[key]; ok {
			f.store(key, data)
			return data, nil
		}
	}

	return nil, fmt.Errorf("no spec available for %s/%s", provider, version)
}

func (f *Fetcher) store(key string, data []byte) {
	f.mu.Lock()
	f.cache[key] = data
	f.mu.Unlock()
}

func fetchURL(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}
```

- [x] **Step 4: Run tests**

```bash
go test ./internal/specs/ -run TestFetcher -v
```

Expected: `PASS`

- [x] **Step 5: Create minimal bundled spec stubs** (these get replaced with real spec content — for now just valid JSON/YAML skeletons so the build doesn't break)

```bash
mkdir -p specs
echo '{"openapi":"3.1.0","info":{"title":"Anthropic API","version":"v1"},"paths":{}}' > specs/anthropic-v1.json
echo '{"openapi":"3.1.0","info":{"title":"OpenAI API","version":"v1"},"paths":{}}' > specs/openai-v1.json
echo '{"openapi":"3.1.0","info":{"title":"Gemini API","version":"v1beta"},"paths":{}}' > specs/gemini-v1beta.json
```

- [x] **Step 6: Commit**

```bash
git add internal/specs/fetcher.go internal/specs/fetcher_test.go specs/
git commit -m "feat: spec fetcher with disk cache and fallback"
```

---

### Task 5: Spec validator

**Files:**
- Create: `internal/specs/validator.go`
- Create: `internal/specs/validator_test.go`

- [x] **Step 1: Write failing tests**

```go
// internal/specs/validator_test.go
package specs_test

import (
	"testing"

	"github.com/ketang/zolem/internal/specs"
)

// minimalSchema is a JSON Schema that requires a "model" string field.
const minimalSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["model"],
  "properties": {
    "model": {"type": "string"},
    "stream": {"type": "boolean"}
  },
  "additionalProperties": true
}`

func TestValidator_Valid(t *testing.T) {
	v := specs.NewValidator()
	v.LoadRaw("test", "v1", []byte(minimalSchema))

	err := v.Validate("test", "v1", []byte(`{"model":"test-model"}`))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidator_MissingRequired(t *testing.T) {
	v := specs.NewValidator()
	v.LoadRaw("test", "v1", []byte(minimalSchema))

	err := v.Validate("test", "v1", []byte(`{"stream":true}`))
	if err == nil {
		t.Error("expected validation error for missing model")
	}
}

func TestValidator_UnknownProviderVersion(t *testing.T) {
	v := specs.NewValidator()
	// no schema loaded — should return nil (pass-through, not hard fail)
	err := v.Validate("unknown", "v99", []byte(`{}`))
	if err != nil {
		t.Errorf("unknown provider should not error, got: %v", err)
	}
}
```

- [x] **Step 2: Run to confirm failure**

```bash
go test ./internal/specs/ -run TestValidator -v
```

Expected: compile error.

- [x] **Step 3: Implement validator.go**

```go
// internal/specs/validator.go
package specs

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidationError wraps schema validation failures.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return "validation failed: " + strings.Join(e.Errors, "; ")
}

// Validator holds compiled JSON schemas per (provider, version).
type Validator struct {
	mu      sync.RWMutex
	schemas map[string]*jsonschema.Schema
}

func NewValidator() *Validator {
	return &Validator{schemas: make(map[string]*jsonschema.Schema)}
}

// LoadRaw compiles and stores a schema from raw JSON/YAML bytes.
func (v *Validator) LoadRaw(provider, version string, data []byte) error {
	compiler := jsonschema.NewCompiler()
	uri := "mem://" + provider + "-" + version + ".json"
	if err := compiler.AddResource(uri, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("add resource: %w", err)
	}
	schema, err := compiler.Compile(uri)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	v.mu.Lock()
	v.schemas[provider+":"+version] = schema
	v.mu.Unlock()
	return nil
}

// Validate validates body against the schema for (provider, version).
// Returns nil if no schema is loaded (pass-through) or if validation passes.
func (v *Validator) Validate(provider, version string, body []byte) error {
	v.mu.RLock()
	schema, ok := v.schemas[provider+":"+version]
	v.mu.RUnlock()
	if !ok {
		return nil // no schema loaded → pass-through
	}

	var inst any
	if err := jsonschema.UnmarshalJSON(bytes.NewReader(body), &inst); err != nil {
		return &ValidationError{Errors: []string{"invalid JSON: " + err.Error()}}
	}
	if err := schema.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if ok := asValidationError(err, &ve); ok {
			msgs := collectMessages(ve)
			return &ValidationError{Errors: msgs}
		}
		return &ValidationError{Errors: []string{err.Error()}}
	}
	return nil
}

func asValidationError(err error, out **jsonschema.ValidationError) bool {
	if ve, ok := err.(*jsonschema.ValidationError); ok {
		*out = ve
		return true
	}
	return false
}

func collectMessages(ve *jsonschema.ValidationError) []string {
	var msgs []string
	msgs = append(msgs, ve.Error())
	for _, c := range ve.Causes {
		msgs = append(msgs, collectMessages(c)...)
	}
	return msgs
}
```

- [x] **Step 4: Run tests**

```bash
go test ./internal/specs/ -run TestValidator -v
```

Expected: `PASS`

- [x] **Step 5: Commit**

```bash
git add internal/specs/validator.go internal/specs/validator_test.go
git commit -m "feat: JSON Schema validator with per-provider/version schema store"
```

---

## Phase 3 — Response Generation

### Task 6: Lorem ipsum generator

**Files:**
- Create: `internal/response/lorem.go`
- Create: `internal/response/lorem_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/response/lorem_test.go
package response_test

import (
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/response"
)

func TestLoremGenerate_ReturnsTokens(t *testing.T) {
	g := response.NewLoremGenerator()
	tokens := g.Generate(10)
	if len(tokens) == 0 {
		t.Fatal("expected non-empty tokens")
	}
}

func TestLoremGenerate_ApproximateWordCount(t *testing.T) {
	g := response.NewLoremGenerator()
	tokens := g.Generate(20)
	// tokens are words/punctuation; count should be in a reasonable range
	if len(tokens) < 15 || len(tokens) > 30 {
		t.Errorf("expected ~20 tokens, got %d", len(tokens))
	}
}

func TestLoremGenerate_NonEmpty(t *testing.T) {
	g := response.NewLoremGenerator()
	tokens := g.Generate(5)
	for i, tok := range tokens {
		if strings.TrimSpace(tok) == "" {
			t.Errorf("token[%d] is empty", i)
		}
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/response/ -run TestLorem -v
```

Expected: compile error.

- [ ] **Step 3: Implement lorem.go**

```go
// internal/response/lorem.go
package response

var loremWords = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur",
	"adipiscing", "elit", "sed", "do", "eiusmod", "tempor",
	"incididunt", "ut", "labore", "et", "dolore", "magna",
	"aliqua", "enim", "ad", "minim", "veniam", "quis",
	"nostrud", "exercitation", "ullamco", "laboris", "nisi",
	"aliquip", "ex", "ea", "commodo", "consequat", "duis",
	"aute", "irure", "in", "reprehenderit", "voluptate",
	"velit", "esse", "cillum", "fugiat", "nulla", "pariatur",
}

// LoremGenerator produces deterministic lorem ipsum token slices.
type LoremGenerator struct{}

func NewLoremGenerator() *LoremGenerator { return &LoremGenerator{} }

// Generate returns approximately n words as a slice of string tokens.
// Each token is a single word followed by a space (except the last).
func (g *LoremGenerator) Generate(n int) []string {
	tokens := make([]string, n)
	for i := range tokens {
		word := loremWords[i%len(loremWords)]
		if i < n-1 {
			tokens[i] = word + " "
		} else {
			tokens[i] = word + "."
		}
	}
	return tokens
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/response/ -run TestLorem -v
```

Expected: `PASS`

- [ ] **Step 5: Commit**

```bash
git add internal/response/lorem.go internal/response/lorem_test.go
git commit -m "feat: lorem ipsum token generator"
```

---

### Task 7: SSE writer utility

**Files:**
- Create: `internal/response/sse.go`
- Create: `internal/response/sse_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/response/sse_test.go
package response_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/response"
)

func TestSSEWriter_WriteEvent(t *testing.T) {
	rr := httptest.NewRecorder()
	w := response.NewSSEWriter(rr)

	w.WriteEvent("content_block_delta", []byte(`{"type":"content_block_delta"}`))
	w.Flush()

	body := rr.Body.String()
	if !strings.Contains(body, "event: content_block_delta\n") {
		t.Errorf("missing event line, got: %q", body)
	}
	if !strings.Contains(body, `data: {"type":"content_block_delta"}`) {
		t.Errorf("missing data line, got: %q", body)
	}
	if !strings.Contains(body, "\n\n") {
		t.Errorf("missing event terminator, got: %q", body)
	}
}

func TestSSEWriter_WriteData(t *testing.T) {
	rr := httptest.NewRecorder()
	w := response.NewSSEWriter(rr)

	w.WriteData([]byte(`[DONE]`))
	w.Flush()

	body := rr.Body.String()
	if !strings.Contains(body, "data: [DONE]\n\n") {
		t.Errorf("unexpected body: %q", body)
	}
	// no event: line for data-only
	if strings.Contains(body, "event:") {
		t.Errorf("unexpected event line in data-only write: %q", body)
	}
}

func TestSSEWriter_SetHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	w := response.NewSSEWriter(rr)
	w.SetHeaders()

	ct := rr.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want text/event-stream", ct)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/response/ -run TestSSE -v
```

Expected: compile error.

- [ ] **Step 3: Implement sse.go**

```go
// internal/response/sse.go
package response

import (
	"fmt"
	"net/http"
)

// SSEWriter writes Server-Sent Events to an http.ResponseWriter.
// It requires the ResponseWriter to implement http.Flusher.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	f, _ := w.(http.Flusher) // may be nil in tests without a real flusher
	return &SSEWriter{w: w, flusher: f}
}

// SetHeaders writes the required SSE response headers.
// Must be called before any WriteEvent/WriteData calls.
func (s *SSEWriter) SetHeaders() {
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("Connection", "keep-alive")
	s.w.Header().Set("X-Accel-Buffering", "no")
}

// WriteEvent writes a named SSE event with a data payload.
// Format:
//
//	event: <name>\n
//	data: <payload>\n
//	\n
func (s *SSEWriter) WriteEvent(name string, data []byte) {
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data)
}

// WriteData writes a data-only SSE event (no event: line).
// Format:
//
//	data: <payload>\n
//	\n
func (s *SSEWriter) WriteData(data []byte) {
	fmt.Fprintf(s.w, "data: %s\n\n", data)
}

// Flush flushes the underlying ResponseWriter if it supports flushing.
func (s *SSEWriter) Flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/response/ -v
```

Expected: `PASS`

- [ ] **Step 5: Commit**

```bash
git add internal/response/sse.go internal/response/sse_test.go
git commit -m "feat: SSE writer with named event and data-only event support"
```

---

## Phase 4 — Fixture Engine

### Task 8: Fixture loader

**Files:**
- Create: `internal/fixture/fixture.go`
- Create: `internal/fixture/loader.go`
- Create: `internal/fixture/loader_test.go`
- Create: `testdata/fixtures/sample-anthropic/meta.yaml`
- Create: `testdata/fixtures/sample-anthropic/response.json`

- [ ] **Step 1: Create testdata fixtures**

`testdata/fixtures/sample-anthropic/meta.yaml`:
```yaml
id: sample-anthropic
provider: anthropic
version: v1
stream: true
status: 200
```

`testdata/fixtures/sample-anthropic/response.json`:
```json
{
  "id": "msg_test",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "Hello from fixture."}],
  "model": "claude-3-5-sonnet-20241022",
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 10, "output_tokens": 5}
}
```

- [ ] **Step 2: Write failing tests**

```go
// internal/fixture/loader_test.go
package fixture_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
)

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "fixtures")
}

func TestLoader_LoadDirectory(t *testing.T) {
	l := fixture.NewLoader(testdataDir())
	fixtures, err := l.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("expected at least one fixture")
	}
}

func TestLoader_FixtureMetadata(t *testing.T) {
	l := fixture.NewLoader(testdataDir())
	fixtures, _ := l.Load()

	var found *fixture.Fixture
	for i := range fixtures {
		if fixtures[i].ID == "sample-anthropic" {
			found = &fixtures[i]
			break
		}
	}
	if found == nil {
		t.Fatal("sample-anthropic fixture not found")
	}
	if found.Provider != "anthropic" {
		t.Errorf("provider: got %q, want anthropic", found.Provider)
	}
	if !found.Stream {
		t.Error("expected stream: true")
	}
	if len(found.ResponseBody) == 0 {
		t.Error("expected non-empty response body")
	}
}
```

- [ ] **Step 3: Run to confirm failure**

```bash
go test ./internal/fixture/ -run TestLoader -v
```

Expected: compile error.

- [ ] **Step 4: Implement fixture.go and loader.go**

```go
// internal/fixture/fixture.go
package fixture

// Fixture represents a loaded canned response with its match module.
type Fixture struct {
	ID           string
	Provider     string
	Version      string
	Stream       bool
	Status       int
	ResponseBody []byte
	WASMPath     string // path to match.wasm; empty if not yet loaded
}
```

```go
// internal/fixture/loader.go
package fixture

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type meta struct {
	ID       string `yaml:"id"`
	Provider string `yaml:"provider"`
	Version  string `yaml:"version"`
	Stream   bool   `yaml:"stream"`
	Status   int    `yaml:"status"`
}

// Loader scans a directory tree for fixture subdirectories.
type Loader struct {
	dir string
}

func NewLoader(dir string) *Loader {
	return &Loader{dir: dir}
}

// Load reads all fixture subdirectories. Each subdirectory must contain
// meta.yaml and response.json. match.wasm is optional at load time
// (it may not exist yet in test environments without a WASM toolchain).
func (l *Loader) Load() ([]Fixture, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, fmt.Errorf("read fixture dir %q: %w", l.dir, err)
	}

	var fixtures []Fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f, err := loadOne(filepath.Join(l.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("fixture %q: %w", e.Name(), err)
		}
		fixtures = append(fixtures, f)
	}
	return fixtures, nil
}

func loadOne(dir string) (Fixture, error) {
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.yaml"))
	if err != nil {
		return Fixture{}, fmt.Errorf("read meta.yaml: %w", err)
	}
	var m meta
	if err := yaml.Unmarshal(metaData, &m); err != nil {
		return Fixture{}, fmt.Errorf("parse meta.yaml: %w", err)
	}
	if m.Status == 0 {
		m.Status = 200
	}

	body, err := os.ReadFile(filepath.Join(dir, "response.json"))
	if err != nil {
		return Fixture{}, fmt.Errorf("read response.json: %w", err)
	}

	wasmPath := filepath.Join(dir, "match.wasm")
	if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
		wasmPath = ""
	}

	return Fixture{
		ID:           m.ID,
		Provider:     m.Provider,
		Version:      m.Version,
		Stream:       m.Stream,
		Status:       m.Status,
		ResponseBody: body,
		WASMPath:     wasmPath,
	}, nil
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/fixture/ -run TestLoader -v
```

Expected: `PASS`

- [ ] **Step 6: Commit**

```bash
git add internal/fixture/fixture.go internal/fixture/loader.go internal/fixture/loader_test.go testdata/
git commit -m "feat: fixture loader with meta.yaml and response.json parsing"
```

---

### Task 9: WASM runner

**Files:**
- Create: `internal/fixture/wasm.go`
- Create: `internal/fixture/wasm_test.go`

The WASM ABI:
- Module exports: `memory` (linear memory), `match(ptr i32, len i32) f32`
- Host writes JSON context bytes to memory at offset 0, then calls `match(0, len)`
- Return value: `f32` — negative = no match, non-negative = score

- [ ] **Step 1: Write failing tests**

```go
// internal/fixture/wasm_test.go
package fixture_test

import (
	"context"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
)

// alwaysMatchWAT is a minimal WAT module that always returns score 1.0.
// It exports memory and a match function that ignores its arguments.
const alwaysMatchWAT = `(module
  (memory (export "memory") 1)
  (func (export "match") (param i32 i32) (result f32)
    f32.const 1.0
  )
)`

// noMatchWAT always returns -1.0 (no match).
const noMatchWAT = `(module
  (memory (export "memory") 1)
  (func (export "match") (param i32 i32) (result f32)
    f32.const -1.0
  )
)`

func TestRunner_Score_AlwaysMatch(t *testing.T) {
	r := fixture.NewRunner()
	defer r.Close()

	mod, err := r.CompileWAT(context.Background(), []byte(alwaysMatchWAT))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	score, err := r.Score(context.Background(), mod, []byte(`{"provider":"anthropic"}`))
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if score < 0 {
		t.Errorf("expected non-negative score, got %f", score)
	}
}

func TestRunner_Score_NoMatch(t *testing.T) {
	r := fixture.NewRunner()
	defer r.Close()

	mod, err := r.CompileWAT(context.Background(), []byte(noMatchWAT))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	score, err := r.Score(context.Background(), mod, []byte(`{}`))
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if score >= 0 {
		t.Errorf("expected negative score, got %f", score)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/fixture/ -run TestRunner -v
```

Expected: compile error.

- [ ] **Step 3: Implement wasm.go**

```go
// internal/fixture/wasm.go
package fixture

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// CompiledModule is an opaque handle to a compiled WASM match function.
type CompiledModule struct {
	compiled wazero.CompiledModule
}

// Runner manages a wazero runtime and executes WASM match functions.
type Runner struct {
	rt wazero.Runtime
}

func NewRunner() *Runner {
	return &Runner{rt: wazero.NewRuntime(context.Background())}
}

func (r *Runner) Close() {
	r.rt.Close(context.Background())
}

// CompileWAT compiles a WebAssembly Text format module.
// Use this in tests to avoid needing a WASM toolchain.
func (r *Runner) CompileWAT(ctx context.Context, wat []byte) (CompiledModule, error) {
	wasm, err := wazero.NewRuntimeConfig() // convert WAT → WASM via wazero internal
	_ = wasm
	// wazero's CompileModule accepts both WASM binary and WAT text
	compiled, err := r.rt.CompileModule(ctx, wat)
	if err != nil {
		return CompiledModule{}, fmt.Errorf("compile WAT: %w", err)
	}
	return CompiledModule{compiled: compiled}, nil
}

// CompileWASM compiles a binary WASM module from disk bytes.
func (r *Runner) CompileWASM(ctx context.Context, wasmBytes []byte) (CompiledModule, error) {
	compiled, err := r.rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return CompiledModule{}, fmt.Errorf("compile WASM: %w", err)
	}
	return CompiledModule{compiled: compiled}, nil
}

// Score instantiates the module, writes input JSON into memory at offset 0,
// calls match(0, len(input)), and returns the f32 score.
// A negative score means no match.
func (r *Runner) Score(ctx context.Context, mod CompiledModule, input []byte) (float32, error) {
	cfg := wazero.NewModuleConfig().WithName("")
	inst, err := r.rt.InstantiateModule(ctx, mod.compiled, cfg)
	if err != nil {
		return -1, fmt.Errorf("instantiate: %w", err)
	}
	defer inst.Close(ctx)

	mem := inst.Memory()
	if mem == nil {
		return -1, fmt.Errorf("module has no exported memory")
	}
	if !mem.Write(0, input) {
		return -1, fmt.Errorf("failed to write input to WASM memory (size %d)", len(input))
	}

	matchFn := inst.ExportedFunction("match")
	if matchFn == nil {
		return -1, fmt.Errorf("module does not export 'match' function")
	}

	results, err := matchFn.Call(ctx, uint64(0), uint64(len(input)))
	if err != nil {
		return -1, fmt.Errorf("call match: %w", err)
	}

	score := api.DecodeF32(results[0])
	return score, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/fixture/ -run TestRunner -v
```

Expected: `PASS`

- [ ] **Step 5: Commit**

```bash
git add internal/fixture/wasm.go internal/fixture/wasm_test.go
git commit -m "feat: wazero WASM runner with ABI for fixture match scoring"
```

---

### Task 10: Fixture matcher

**Files:**
- Create: `internal/fixture/matcher.go`
- Create: `internal/fixture/matcher_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/fixture/matcher_test.go
package fixture_test

import (
	"context"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
)

func TestMatcher_MatchesHighestScore(t *testing.T) {
	r := fixture.NewRunner()
	defer r.Close()

	highMod, _ := r.CompileWAT(context.Background(), []byte(`(module
		(memory (export "memory") 1)
		(func (export "match") (param i32 i32) (result f32) f32.const 10.0)
	)`))
	lowMod, _ := r.CompileWAT(context.Background(), []byte(`(module
		(memory (export "memory") 1)
		(func (export "match") (param i32 i32) (result f32) f32.const 1.0)
	)`))

	fixtures := []fixture.Fixture{
		{ID: "low", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"low"}`), Module: &lowMod},
		{ID: "high", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{"id":"high"}`), Module: &highMod},
	}

	m := fixture.NewMatcher(r, fixtures)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}

	result, err := m.Match(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a match")
	}
	if result.ID != "high" {
		t.Errorf("expected high-scoring fixture, got %q", result.ID)
	}
}

func TestMatcher_NilOnNoMatch(t *testing.T) {
	r := fixture.NewRunner()
	defer r.Close()

	noMod, _ := r.CompileWAT(context.Background(), []byte(`(module
		(memory (export "memory") 1)
		(func (export "match") (param i32 i32) (result f32) f32.const -1.0)
	)`))

	fixtures := []fixture.Fixture{
		{ID: "none", Provider: "anthropic", Version: "v1", Status: 200, ResponseBody: []byte(`{}`), Module: &noMod},
	}

	m := fixture.NewMatcher(r, fixtures)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}

	result, err := m.Match(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got fixture %q", result.ID)
	}
}

func TestMatcher_SkipsWrongProviderOrVersion(t *testing.T) {
	r := fixture.NewRunner()
	defer r.Close()

	alwaysMod, _ := r.CompileWAT(context.Background(), []byte(`(module
		(memory (export "memory") 1)
		(func (export "match") (param i32 i32) (result f32) f32.const 1.0)
	)`))

	fixtures := []fixture.Fixture{
		{ID: "openai-fixture", Provider: "openai", Version: "v1", Status: 200, ResponseBody: []byte(`{}`), Module: &alwaysMod},
	}

	m := fixture.NewMatcher(r, fixtures)
	req := fixture.MatchRequest{Provider: "anthropic", Version: "v1", Labels: map[string]string{}, Body: []byte(`{}`)}

	result, _ := m.Match(context.Background(), req)
	if result != nil {
		t.Error("fixture for wrong provider should not match")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/fixture/ -run TestMatcher -v
```

Expected: compile error (Module field not yet on Fixture struct).

- [ ] **Step 3: Update fixture.go to add the Module field**

```go
// internal/fixture/fixture.go
package fixture

// Fixture represents a loaded canned response with its compiled match module.
type Fixture struct {
	ID           string
	Provider     string
	Version      string
	Stream       bool
	Status       int
	ResponseBody []byte
	WASMPath     string
	Module       *CompiledModule // nil if no match.wasm present
}
```

- [ ] **Step 4: Implement matcher.go**

```go
// internal/fixture/matcher.go
package fixture

import (
	"context"
	"encoding/json"
)

// MatchRequest contains the context passed to WASM match functions.
type MatchRequest struct {
	Provider string            `json:"provider"`
	Version  string            `json:"version"`
	Labels   map[string]string `json:"labels"`
	Body     json.RawMessage   `json:"body"`
}

// Matcher scores all loaded fixtures against an incoming request and returns
// the highest-scoring match, or nil if no fixture scores >= 0.
type Matcher struct {
	runner   *Runner
	fixtures []Fixture
}

func NewMatcher(runner *Runner, fixtures []Fixture) *Matcher {
	return &Matcher{runner: runner, fixtures: fixtures}
}

// Match evaluates all fixtures for (provider, version), returns the winner or nil.
func (m *Matcher) Match(ctx context.Context, req MatchRequest) (*Fixture, error) {
	input, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	var best *Fixture
	var bestScore float32 = -1

	for i := range m.fixtures {
		f := &m.fixtures[i]
		if f.Provider != req.Provider || f.Version != req.Version {
			continue
		}
		if f.Module == nil {
			continue
		}
		score, err := m.runner.Score(ctx, *f.Module, input)
		if err != nil || score < 0 {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = f
		}
	}
	return best, nil
}
```

- [ ] **Step 5: Run all fixture tests**

```bash
go test ./internal/fixture/ -v
```

Expected: `PASS`

- [ ] **Step 6: Commit**

```bash
git add internal/fixture/
git commit -m "feat: fixture matcher with WASM scoring, highest-score wins, provider/version filter"
```

---

## Phase 5 — Provider Implementations

### Task 11: Anthropic provider

**Files:**
- Create: `internal/provider/anthropic/types.go`
- Create: `internal/provider/anthropic/errors.go`
- Create: `internal/provider/anthropic/sse.go`
- Create: `internal/provider/anthropic/handler.go`
- Create: `internal/provider/anthropic/handler_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/provider/anthropic/handler_test.go
package anthropic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/anthropic"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/specs"
)

func newHandler(t *testing.T) *anthropic.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	validator := specs.NewValidator()
	return anthropic.NewHandler(validator, matcher, lorem)
}

func TestMessages_MissingAuthHeader(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["type"] != "error" {
		t.Errorf("response type: got %v, want error", resp["type"])
	}
}

func TestMessages_LoremResponse_NonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["type"] != "message" {
		t.Errorf("response type: got %v, want message", resp["type"])
	}
	if resp["role"] != "assistant" {
		t.Errorf("role: got %v, want assistant", resp["role"])
	}
}

func TestMessages_LoremResponse_Streaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "sk-any-key")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "event: message_start") {
		t.Errorf("missing message_start event, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, "event: message_stop") {
		t.Errorf("missing message_stop event, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, "event: content_block_delta") {
		t.Errorf("missing content_block_delta, got:\n%s", responseBody)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/provider/anthropic/ -v
```

Expected: compile error.

- [ ] **Step 3: Implement types.go**

```go
// internal/provider/anthropic/types.go
package anthropic

// MessagesRequest mirrors the Anthropic Messages API request body.
type MessagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []Message `json:"messages"`
	System    string    `json:"system,omitempty"`
	Stream    bool      `json:"stream,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MessagesResponse mirrors the non-streaming Anthropic Messages API response.
type MessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
```

- [ ] **Step 4: Implement errors.go**

```go
// internal/provider/anthropic/errors.go
package anthropic

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Type  string    `json:"type"`
	Error apiError  `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorEnvelope{
		Type:  "error",
		Error: apiError{Type: errType, Message: message},
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "authentication_error", "invalid x-api-key")
}

func writeInvalidRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "invalid_request_error", message)
}
```

- [ ] **Step 5: Implement sse.go**

```go
// internal/provider/anthropic/sse.go
package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ketang/zolem/internal/response"
)

func streamResponse(w http.ResponseWriter, model string, tokens []string, inputTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	msgID := "msg_zolem_" + fmt.Sprintf("%016x", pseudoRandID())

	// message_start
	msgStart, _ := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []any{}, "model": model,
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": inputTokens, "output_tokens": 1},
		},
	})
	sse.WriteEvent("message_start", msgStart)
	sse.Flush()

	// content_block_start
	cbStart, _ := json.Marshal(map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]string{"type": "text", "text": ""},
	})
	sse.WriteEvent("content_block_start", cbStart)
	sse.Flush()

	// ping
	sse.WriteEvent("ping", []byte(`{"type":"ping"}`))
	sse.Flush()

	// content_block_delta — one per token
	for _, tok := range tokens {
		delta, _ := json.Marshal(map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]string{"type": "text_delta", "text": tok},
		})
		sse.WriteEvent("content_block_delta", delta)
		sse.Flush()
	}

	// content_block_stop
	sse.WriteEvent("content_block_stop", []byte(`{"type":"content_block_stop","index":0}`))
	sse.Flush()

	// message_delta
	msgDelta, _ := json.Marshal(map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": len(tokens)},
	})
	sse.WriteEvent("message_delta", msgDelta)
	sse.Flush()

	// message_stop
	sse.WriteEvent("message_stop", []byte(`{"type":"message_stop"}`))
	sse.Flush()
}

// pseudoRandID returns a simple counter-based ID (not cryptographic).
var pseudoCounter uint64

func pseudoRandID() uint64 {
	pseudoCounter++
	return pseudoCounter
}
```

- [ ] **Step 6: Implement handler.go**

```go
// internal/provider/anthropic/handler.go
package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	"github.com/ketang/zolem/internal/specs"
)

// Handler implements http.Handler for the Anthropic provider.
type Handler struct {
	validator *specs.Validator
	matcher   *fixture.Matcher
	lorem     *response.LoremGenerator
	mux       *chi.Mux
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, lorem *response.LoremGenerator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, lorem: lorem}
	h.mux = chi.NewRouter()
	h.mux.Post("/v1/messages", h.handleMessages)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	// auth: require non-empty x-api-key header
	if r.Header.Get("x-api-key") == "" {
		writeUnauthorized(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(w, "failed to read request body")
		return
	}

	// detect version from path
	version := "v1"
	if strings.HasPrefix(r.URL.Path, "/v1beta") {
		version = "v1beta"
	}

	// validate against OpenAPI spec (pass-through if no schema loaded)
	if err := h.validator.Validate("anthropic", version, body); err != nil {
		writeInvalidRequest(w, err.Error())
		return
	}

	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(w, "model is required")
		return
	}
	if req.MaxTokens == 0 {
		writeInvalidRequest(w, "max_tokens is required")
		return
	}

	// attempt fixture match
	matchReq := fixture.MatchRequest{
		Provider: "anthropic",
		Version:  version,
		Labels:   labelsFromContext(r.Context()),
		Body:     json.RawMessage(body),
	}
	matched, _ := h.matcher.Match(r.Context(), matchReq)

	if matched != nil {
		serveFixture(w, matched, req.Stream)
		return
	}

	// lorem ipsum fallback
	tokens := h.lorem.Generate(30)
	if req.Stream {
		streamResponse(w, req.Model, tokens, estimateInputTokens(req))
		return
	}

	text := strings.Join(tokens, "")
	resp := MessagesResponse{
		ID:         "msg_zolem_lorem",
		Type:       "message",
		Role:       "assistant",
		Content:    []ContentBlock{{Type: "text", Text: text}},
		Model:      req.Model,
		StopReason: "end_turn",
		Usage:      Usage{InputTokens: estimateInputTokens(req), OutputTokens: len(tokens)},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func serveFixture(w http.ResponseWriter, f *fixture.Fixture, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	// extract text from fixture response body and stream it
	var msg MessagesResponse
	if err := json.Unmarshal(f.ResponseBody, &msg); err != nil || len(msg.Content) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	text := msg.Content[0].Text
	tokens := tokenize(text)
	streamResponse(w, msg.Model, tokens, msg.Usage.InputTokens)
}

func tokenize(text string) []string {
	words := strings.Fields(text)
	tokens := make([]string, len(words))
	for i, w := range words {
		if i < len(words)-1 {
			tokens[i] = w + " "
		} else {
			tokens[i] = w
		}
	}
	return tokens
}

func estimateInputTokens(req MessagesRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(strings.Fields(m.Content)) + 4
	}
	return total
}

func labelsFromContext(ctx context.Context) map[string]string {
	if v := ctx.Value(router.LabelsKey{}); v != nil {
		if labels, ok := v.(map[string]string); ok {
			return labels
		}
	}
	return map[string]string{}
}
```

- [ ] **Step 7: Run tests**

```bash
go test ./internal/provider/anthropic/ -v
```

Expected: `PASS`

- [ ] **Step 8: Commit**

```bash
git add internal/provider/anthropic/
git commit -m "feat: Anthropic provider with auth, lorem fallback, streaming SSE, fixture serving"
```

---

### Task 12: OpenAI provider

**Files:**
- Create: `internal/provider/openai/types.go`
- Create: `internal/provider/openai/errors.go`
- Create: `internal/provider/openai/sse.go`
- Create: `internal/provider/openai/handler.go`
- Create: `internal/provider/openai/handler_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/provider/openai/handler_test.go
package openai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/openai"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/specs"
)

func newHandler(t *testing.T) *openai.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return openai.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator())
}

func TestChatCompletions_MissingAuth(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestChatCompletions_LoremNonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["object"] != "chat.completion" {
		t.Errorf("object: got %v, want chat.completion", resp["object"])
	}
}

func TestChatCompletions_LoremStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	body2 := rr.Body.String()
	if !strings.Contains(body2, "chat.completion.chunk") {
		t.Errorf("expected chunk objects, got:\n%s", body2)
	}
	if !strings.Contains(body2, "data: [DONE]") {
		t.Errorf("missing [DONE] terminator, got:\n%s", body2)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/provider/openai/ -v
```

Expected: compile error.

- [ ] **Step 3: Implement types.go**

```go
// internal/provider/openai/types.go
package openai

type ChatCompletionRequest struct {
	Model         string    `json:"model"`
	Messages      []Message `json:"messages"`
	Stream        bool      `json:"stream,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
```

- [ ] **Step 4: Implement errors.go**

```go
// internal/provider/openai/errors.go
package openai

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    *string `json:"code"`
}

func writeError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorEnvelope{
		Error: apiError{Message: message, Type: errType, Code: nil},
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "invalid_request_error", "Incorrect API key provided")
}

func writeInvalidRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "invalid_request_error", message)
}
```

- [ ] **Step 5: Implement sse.go**

```go
// internal/provider/openai/sse.go
package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ketang/zolem/internal/response"
)

func streamResponse(w http.ResponseWriter, model string, tokens []string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	id := fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano())
	created := time.Now().Unix()

	// first chunk: role
	firstChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]string{"role": "assistant", "content": ""}, "finish_reason": nil}},
	}
	data, _ := json.Marshal(firstChunk)
	sse.WriteData(data)
	sse.Flush()

	// content chunks
	for _, tok := range tokens {
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": tok}, "finish_reason": nil}},
		}
		data, _ := json.Marshal(chunk)
		sse.WriteData(data)
		sse.Flush()
	}

	// final chunk: finish_reason
	stop := "stop"
	finalChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": stop}},
	}
	data, _ = json.Marshal(finalChunk)
	sse.WriteData(data)
	sse.Flush()

	// usage chunk (always included)
	usageChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{},
		"usage": map[string]int{
			"prompt_tokens": promptTokens, "completion_tokens": len(tokens),
			"total_tokens": promptTokens + len(tokens),
		},
	}
	data, _ = json.Marshal(usageChunk)
	sse.WriteData(data)
	sse.Flush()

	sse.WriteData([]byte("[DONE]"))
	sse.Flush()
}
```

- [ ] **Step 6: Implement handler.go**

```go
// internal/provider/openai/handler.go
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	"github.com/ketang/zolem/internal/specs"
)

type Handler struct {
	validator *specs.Validator
	matcher   *fixture.Matcher
	lorem     *response.LoremGenerator
	mux       *chi.Mux
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, lorem *response.LoremGenerator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, lorem: lorem}
	h.mux = chi.NewRouter()
	h.mux.Post("/v1/chat/completions", h.handleChatCompletions)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		writeUnauthorized(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(w, "failed to read request body")
		return
	}

	if err := h.validator.Validate("openai", "v1", body); err != nil {
		writeInvalidRequest(w, err.Error())
		return
	}

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidRequest(w, "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" {
		writeInvalidRequest(w, "model is required")
		return
	}

	matchReq := fixture.MatchRequest{
		Provider: "openai", Version: "v1",
		Labels: labelsFromContext(r.Context()),
		Body:   json.RawMessage(body),
	}
	matched, _ := h.matcher.Match(r.Context(), matchReq)
	if matched != nil {
		serveFixture(w, matched, req)
		return
	}

	tokens := h.lorem.Generate(30)
	promptTokens := estimatePromptTokens(req)

	if req.Stream {
		streamResponse(w, req.Model, tokens, promptTokens)
		return
	}

	text := strings.Join(tokens, "")
	resp := ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: text}, FinishReason: "stop"}},
		Usage:   Usage{PromptTokens: promptTokens, CompletionTokens: len(tokens), TotalTokens: promptTokens + len(tokens)},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func serveFixture(w http.ResponseWriter, f *fixture.Fixture, req ChatCompletionRequest) {
	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	var resp ChatCompletionResponse
	if err := json.Unmarshal(f.ResponseBody, &resp); err != nil || len(resp.Choices) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	tokens := tokenize(resp.Choices[0].Message.Content)
	streamResponse(w, resp.Model, tokens, resp.Usage.PromptTokens)
}

func tokenize(text string) []string {
	words := strings.Fields(text)
	tokens := make([]string, len(words))
	for i, w := range words {
		if i < len(words)-1 {
			tokens[i] = w + " "
		} else {
			tokens[i] = w
		}
	}
	return tokens
}

func estimatePromptTokens(req ChatCompletionRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(strings.Fields(m.Content)) + 4
	}
	return total
}

func labelsFromContext(ctx context.Context) map[string]string {
	if v := ctx.Value(router.LabelsKey{}); v != nil {
		if labels, ok := v.(map[string]string); ok {
			return labels
		}
	}
	return map[string]string{}
}
```

- [ ] **Step 7: Run tests**

```bash
go test ./internal/provider/openai/ -v
```

Expected: `PASS`

- [ ] **Step 8: Commit**

```bash
git add internal/provider/openai/
git commit -m "feat: OpenAI provider with auth, lorem fallback, SSE streaming, [DONE] terminator"
```

---

### Task 13: Gemini provider

**Files:**
- Create: `internal/provider/gemini/types.go`
- Create: `internal/provider/gemini/errors.go`
- Create: `internal/provider/gemini/sse.go`
- Create: `internal/provider/gemini/handler.go`
- Create: `internal/provider/gemini/handler_test.go`

**Note on Gemini routing:** Gemini uses `/v1/models/{model}:generateContent` (non-streaming) and `/v1/models/{model}:streamGenerateContent?alt=sse` (streaming). These are distinct URL patterns, not a `stream` body parameter.

- [ ] **Step 1: Write failing tests**

```go
// internal/provider/gemini/handler_test.go
package gemini_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/gemini"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/specs"
)

func newHandler(t *testing.T) *gemini.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	return gemini.NewHandler(specs.NewValidator(), fixture.NewMatcher(runner, nil), response.NewLoremGenerator())
}

func TestGenerateContent_MissingAuth(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["error"]; !ok {
		t.Error("expected error field")
	}
}

func TestGenerateContent_LoremNonStreaming(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["candidates"]; !ok {
		t.Error("expected candidates field")
	}
}

func TestStreamGenerateContent_SSE(t *testing.T) {
	h := newHandler(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/models/gemini-2.0-flash:streamGenerateContent?alt=sse", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "test-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	responseBody := rr.Body.String()
	if !strings.Contains(responseBody, "candidates") {
		t.Errorf("expected candidates in SSE stream, got:\n%s", responseBody)
	}
	if !strings.Contains(responseBody, `"finishReason":"STOP"`) {
		t.Errorf("expected STOP in final chunk, got:\n%s", responseBody)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/provider/gemini/ -v
```

Expected: compile error.

- [ ] **Step 3: Implement types.go**

```go
// internal/provider/gemini/types.go
package gemini

type GenerateContentRequest struct {
	Contents         []Content        `json:"contents"`
	GenerationConfig *GenerationConfig `json:"generationConfig,omitempty"`
}

type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Text string `json:"text,omitempty"`
}

type GenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type GenerateContentResponse struct {
	Candidates    []Candidate   `json:"candidates"`
	UsageMetadata UsageMetadata `json:"usageMetadata"`
	ModelVersion  string        `json:"modelVersion,omitempty"`
}

type Candidate struct {
	Content      Content `json:"content"`
	FinishReason string  `json:"finishReason"`
	Index        int     `json:"index"`
}

type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount"`
}
```

- [ ] **Step 4: Implement errors.go**

```go
// internal/provider/gemini/errors.go
package gemini

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func writeError(w http.ResponseWriter, code int, status, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(errorEnvelope{
		Error: apiError{Code: code, Message: message, Status: status},
	})
}

func writeForbidden(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "API key not valid. Please pass a valid API key.")
}

func writeInvalidRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", message)
}
```

- [ ] **Step 5: Implement sse.go**

```go
// internal/provider/gemini/sse.go
package gemini

import (
	"encoding/json"
	"net/http"

	"github.com/ketang/zolem/internal/response"
)

func streamResponse(w http.ResponseWriter, model string, tokens []string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	for i, tok := range tokens {
		isLast := i == len(tokens)-1
		finishReason := "NONE"
		candidateTokenCount := 0
		if isLast {
			finishReason = "STOP"
			candidateTokenCount = len(tokens)
		}

		chunk := GenerateContentResponse{
			Candidates: []Candidate{{
				Content: Content{
					Parts: []Part{{Text: tok}},
					Role:  "model",
				},
				FinishReason: finishReason,
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount:     promptTokens,
				CandidatesTokenCount: candidateTokenCount,
				TotalTokenCount:      promptTokens + candidateTokenCount,
			},
			ModelVersion: model,
		}

		data, _ := json.Marshal(chunk)
		sse.WriteData(data)
		sse.Flush()
	}
}
```

- [ ] **Step 6: Implement handler.go**

```go
// internal/provider/gemini/handler.go
package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	"github.com/ketang/zolem/internal/specs"
)

type Handler struct {
	validator *specs.Validator
	matcher   *fixture.Matcher
	lorem     *response.LoremGenerator
	mux       *chi.Mux
}

func NewHandler(validator *specs.Validator, matcher *fixture.Matcher, lorem *response.LoremGenerator) *Handler {
	h := &Handler{validator: validator, matcher: matcher, lorem: lorem}
	h.mux = chi.NewRouter()
	// non-streaming
	h.mux.Post("/v1/models/{model}:generateContent", h.handleGenerate(false))
	h.mux.Post("/v1beta/models/{model}:generateContent", h.handleGenerate(false))
	// streaming
	h.mux.Post("/v1/models/{model}:streamGenerateContent", h.handleGenerate(true))
	h.mux.Post("/v1beta/models/{model}:streamGenerateContent", h.handleGenerate(true))
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleGenerate(stream bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") == "" {
			writeForbidden(w)
			return
		}

		version := "v1"
		if strings.HasPrefix(r.URL.Path, "/v1beta") {
			version = "v1beta"
		}

		model := chi.URLParam(r, "model")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeInvalidRequest(w, "failed to read request body")
			return
		}

		if err := h.validator.Validate("gemini", version, body); err != nil {
			writeInvalidRequest(w, err.Error())
			return
		}

		var req GenerateContentRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeInvalidRequest(w, "invalid JSON: "+err.Error())
			return
		}
		if len(req.Contents) == 0 {
			writeInvalidRequest(w, "contents is required")
			return
		}

		matchReq := fixture.MatchRequest{
			Provider: "gemini", Version: version,
			Labels: labelsFromContext(r.Context()),
			Body:   json.RawMessage(body),
		}
		matched, _ := h.matcher.Match(r.Context(), matchReq)
		if matched != nil {
			serveFixture(w, matched, stream, model)
			return
		}

		tokens := h.lorem.Generate(30)
		promptTokens := estimatePromptTokens(req)

		if stream {
			streamResponse(w, model, tokens, promptTokens)
			return
		}

		text := strings.Join(tokens, "")
		resp := GenerateContentResponse{
			Candidates: []Candidate{{
				Content:      Content{Parts: []Part{{Text: text}}, Role: "model"},
				FinishReason: "STOP",
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount:     promptTokens,
				CandidatesTokenCount: len(tokens),
				TotalTokenCount:      promptTokens + len(tokens),
			},
			ModelVersion: model,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

func serveFixture(w http.ResponseWriter, f *fixture.Fixture, stream bool, model string) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	var resp GenerateContentResponse
	if err := json.Unmarshal(f.ResponseBody, &resp); err != nil || len(resp.Candidates) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		w.Write(f.ResponseBody)
		return
	}
	text := ""
	if len(resp.Candidates[0].Content.Parts) > 0 {
		text = resp.Candidates[0].Content.Parts[0].Text
	}
	tokens := tokenize(text)
	streamResponse(w, model, tokens, resp.UsageMetadata.PromptTokenCount)
}

func tokenize(text string) []string {
	words := strings.Fields(text)
	tokens := make([]string, len(words))
	for i, w := range words {
		if i < len(words)-1 {
			tokens[i] = w + " "
		} else {
			tokens[i] = w
		}
	}
	return tokens
}

func estimatePromptTokens(req GenerateContentRequest) int {
	total := 0
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			total += len(strings.Fields(p.Text)) + 4
		}
	}
	return total
}

func labelsFromContext(ctx context.Context) map[string]string {
	if v := ctx.Value(router.LabelsKey{}); v != nil {
		if labels, ok := v.(map[string]string); ok {
			return labels
		}
	}
	return map[string]string{}
}
```

- [ ] **Step 7: Run tests**

```bash
go test ./internal/provider/gemini/ -v
```

Expected: `PASS`

- [ ] **Step 8: Commit**

```bash
git add internal/provider/gemini/
git commit -m "feat: Gemini provider with streamGenerateContent SSE, finishReason:STOP termination"
```

---

## Phase 6 — Integration

### Task 14: Server wiring

**Files:**
- Modify: `cmd/zolem/main.go` (complete implementation)
- Create: `cmd/zolem/main_test.go`

- [ ] **Step 1: Write failing integration test**

```go
// cmd/zolem/main_test.go
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ketang/zolem/internal/config"
	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/anthropic"
	"github.com/ketang/zolem/internal/provider/gemini"
	"github.com/ketang/zolem/internal/provider/openai"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	"github.com/ketang/zolem/internal/specs"
)

func buildServer(t *testing.T, routes []config.RouteConfig) http.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	validator := specs.NewValidator()
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	r := router.New(routes)

	anthropicH := anthropic.NewHandler(validator, matcher, lorem)
	openaiH := openai.NewHandler(validator, matcher, lorem)
	geminiH := gemini.NewHandler(validator, matcher, lorem)

	return buildVirtualHostHandler(r, anthropicH, openaiH, geminiH)
}

func buildVirtualHostHandler(r *router.Router, anthropicH, openaiH, geminiH http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, ok := r.Match(req.Host)
		if !ok {
			w.Header().Set("X-Zolem-Error", "true")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"zolem_error": "no route matched host: " + req.Host})
			return
		}
		req = req.WithContext(withLabels(req.Context(), ctx.Labels))
		switch ctx.Provider {
		case "anthropic":
			anthropicH.ServeHTTP(w, req)
		case "openai":
			openaiH.ServeHTTP(w, req)
		case "gemini":
			geminiH.ServeHTTP(w, req)
		default:
			w.Header().Set("X-Zolem-Error", "true")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"zolem_error": "unknown provider: " + ctx.Provider})
		}
	})
}

func withLabels(ctx interface{ Value(any) any }, labels map[string]string) interface{} {
	return nil // placeholder — see full implementation below
}

func TestVirtualHost_RoutesToAnthropic(t *testing.T) {
	routes := []config.RouteConfig{
		{Host: "*.api.anthropic.zolem.dev", Provider: "anthropic", Labels: map[string]string{"tenant": "{1}"}},
	}
	srv := httptest.NewServer(buildServer(t, routes))
	defer srv.Close()

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", bytes.NewBufferString(body))
	req.Host = "acme.api.anthropic.zolem.dev"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestVirtualHost_NoRouteReturnsZolemError(t *testing.T) {
	srv := httptest.NewServer(buildServer(t, nil))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", bytes.NewBufferString("{}"))
	req.Host = "unknown.host.dev"
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.Header.Get("X-Zolem-Error") != "true" {
		t.Error("expected X-Zolem-Error header")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./cmd/zolem/ -v
```

Expected: compile errors on `withLabels` placeholder and missing imports.

- [ ] **Step 3: Rewrite main.go with full wiring**

```go
// cmd/zolem/main.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/ketang/zolem/internal/config"
	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/anthropic"
	"github.com/ketang/zolem/internal/provider/gemini"
	"github.com/ketang/zolem/internal/provider/openai"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	"github.com/ketang/zolem/internal/specs"
)

func main() {
	cfgPath := flag.String("config", "zolem.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// spec fetching
	validator := specs.NewValidator()
	specSources := map[string]string{
		"anthropic:v1":    "https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/main/api.json",
		"openai:v1":       "https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml",
		"gemini:v1":       "https://generativelanguage.googleapis.com/$discovery/rest?version=v1",
		"gemini:v1beta":   "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta",
	}
	fetcher := specs.NewFetcher(cfg.Specs.CacheDir, specSources)
	for _, key := range []string{"anthropic:v1", "openai:v1", "gemini:v1", "gemini:v1beta"} {
		provider, version := splitKey(key)
		if data, err := fetcher.Get(provider, version); err == nil {
			if err := validator.LoadRaw(provider, version, data); err != nil {
				log.Printf("warn: failed to load spec %s: %v", key, err)
			}
		} else {
			log.Printf("warn: failed to fetch spec %s: %v (validation disabled for this provider/version)", key, err)
		}
	}

	// fixture loading
	runner := fixture.NewRunner()
	defer runner.Close()

	var fixtures []fixture.Fixture
	if cfg.Fixtures.Dir != "" {
		loader := fixture.NewLoader(cfg.Fixtures.Dir)
		fixtures, err = loader.Load()
		if err != nil {
			log.Fatalf("load fixtures: %v", err)
		}
		for i := range fixtures {
			if fixtures[i].WASMPath == "" {
				log.Printf("warn: fixture %q has no match.wasm — will never match", fixtures[i].ID)
				continue
			}
			wasmBytes, err := os.ReadFile(fixtures[i].WASMPath)
			if err != nil {
				log.Fatalf("read wasm for fixture %q: %v", fixtures[i].ID, err)
			}
			mod, err := runner.CompileWASM(context.Background(), wasmBytes)
			if err != nil {
				log.Fatalf("compile wasm for fixture %q: %v", fixtures[i].ID, err)
			}
			fixtures[i].Module = &mod
			log.Printf("loaded fixture: %s", fixtures[i].ID)
		}
	}

	lorem := response.NewLoremGenerator()
	matcher := fixture.NewMatcher(runner, fixtures)
	r := router.New(cfg.Routes)

	anthropicH := anthropic.NewHandler(validator, matcher, lorem)
	openaiH := openai.NewHandler(validator, matcher, lorem)
	geminiH := gemini.NewHandler(validator, matcher, lorem)

	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		routeCtx, ok := r.Match(req.Host)
		if !ok {
			w.Header().Set("X-Zolem-Error", "true")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{
				"zolem_error": "no route matched host: " + req.Host,
			})
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
			w.Header().Set("X-Zolem-Error", "true")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{
				"zolem_error": "unknown provider: " + routeCtx.Provider,
			})
		}
	})

	log.Printf("zolem listening on %s", cfg.Server.Addr)
	if cfg.Server.TLS.Cert != "" {
		log.Fatal(http.ListenAndServeTLS(cfg.Server.Addr, cfg.Server.TLS.Cert, cfg.Server.TLS.Key, handler))
	} else {
		log.Fatal(http.ListenAndServe(cfg.Server.Addr, handler))
	}
}

func splitKey(key string) (string, string) {
	for i, c := range key {
		if c == ':' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
```

- [ ] **Step 4: Fix the test file to use the real context helper instead of the placeholder**

Update `cmd/zolem/main_test.go` — replace the `withLabels` placeholder and rebuild `buildVirtualHostHandler` to use `context.WithValue`:

```go
// cmd/zolem/main_test.go
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ketang/zolem/internal/config"
	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/provider/anthropic"
	"github.com/ketang/zolem/internal/provider/gemini"
	"github.com/ketang/zolem/internal/provider/openai"
	"github.com/ketang/zolem/internal/response"
	"github.com/ketang/zolem/internal/router"
	"github.com/ketang/zolem/internal/specs"
)

func buildServer(t *testing.T, routes []config.RouteConfig) http.Handler {
	t.Helper()
	runner := fixture.NewRunner()
	t.Cleanup(runner.Close)
	validator := specs.NewValidator()
	matcher := fixture.NewMatcher(runner, nil)
	lorem := response.NewLoremGenerator()
	r := router.New(routes)

	anthropicH := anthropic.NewHandler(validator, matcher, lorem)
	openaiH := openai.NewHandler(validator, matcher, lorem)
	geminiH := gemini.NewHandler(validator, matcher, lorem)

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		routeCtx, ok := r.Match(req.Host)
		if !ok {
			w.Header().Set("X-Zolem-Error", "true")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"zolem_error": "no route matched: " + req.Host})
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
		}
	})
}

func TestVirtualHost_RoutesToAnthropic(t *testing.T) {
	routes := []config.RouteConfig{
		{Host: "*.api.anthropic.zolem.dev", Provider: "anthropic", Labels: map[string]string{"tenant": "{1}"}},
	}
	srv := httptest.NewServer(buildServer(t, routes))
	defer srv.Close()

	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", bytes.NewBufferString(body))
	req.Host = "acme.api.anthropic.zolem.dev"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestVirtualHost_RoutesToOpenAI(t *testing.T) {
	routes := []config.RouteConfig{
		{Host: "*.api.openai.zolem.dev", Provider: "openai", Labels: map[string]string{"tenant": "{1}"}},
	}
	srv := httptest.NewServer(buildServer(t, routes))
	defer srv.Close()

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewBufferString(body))
	req.Host = "acme.api.openai.zolem.dev"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestVirtualHost_NoRouteReturnsZolemError(t *testing.T) {
	srv := httptest.NewServer(buildServer(t, nil))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/anything", bytes.NewBufferString("{}"))
	req.Host = "unknown.host.dev"
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.Header.Get("X-Zolem-Error") != "true" {
		t.Error("expected X-Zolem-Error header")
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", resp.StatusCode)
	}
}
```

- [ ] **Step 5: Run all tests**

```bash
go test ./... -v
```

Expected: all `PASS`

- [ ] **Step 6: Verify binary builds**

```bash
go build ./cmd/zolem/
./zolem --help
```

Expected: binary produced, help printed.

- [ ] **Step 7: Commit**

```bash
git add cmd/zolem/ internal/
git commit -m "feat: complete server wiring with virtual host dispatch, X-Zolem-Error for unknown routes"
```

---

### Task 15: Smoke test with real curl commands

This task verifies the running binary behaves correctly end-to-end without mocks.

- [ ] **Step 1: Create a minimal local config**

```bash
cat > /tmp/zolem-smoke.yaml << 'EOF'
server:
  addr: ":18080"
mode: lorem
specs:
  cache_dir: /tmp/zolem-specs
fixtures:
  dir: /tmp/zolem-fixtures
  watch: false
routes:
  - host: "anthropic.localhost"
    provider: anthropic
  - host: "openai.localhost"
    provider: openai
  - host: "gemini.localhost"
    provider: gemini
EOF
mkdir -p /tmp/zolem-fixtures
```

- [ ] **Step 2: Start the server**

```bash
./zolem --config /tmp/zolem-smoke.yaml &
ZOLEM_PID=$!
sleep 1
```

- [ ] **Step 3: Smoke test Anthropic non-streaming**

```bash
curl -s -H "x-api-key: test" -H "Content-Type: application/json" \
  -H "Host: anthropic.localhost" \
  --resolve "anthropic.localhost:18080:127.0.0.1" \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}' \
  http://anthropic.localhost:18080/v1/messages | jq .
```

Expected: JSON with `"type":"message"`, `"role":"assistant"`, lorem ipsum content.

- [ ] **Step 4: Smoke test Anthropic streaming**

```bash
curl -s -H "x-api-key: test" -H "Content-Type: application/json" \
  -H "Host: anthropic.localhost" \
  --resolve "anthropic.localhost:18080:127.0.0.1" \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}' \
  http://anthropic.localhost:18080/v1/messages
```

Expected: SSE stream with `event: message_start`, `event: content_block_delta` lines, ending with `event: message_stop`.

- [ ] **Step 5: Smoke test OpenAI streaming**

```bash
curl -s -H "Authorization: Bearer sk-test" -H "Content-Type: application/json" \
  -H "Host: openai.localhost" \
  --resolve "openai.localhost:18080:127.0.0.1" \
  -d '{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}' \
  http://openai.localhost:18080/v1/chat/completions
```

Expected: `data: {...chat.completion.chunk...}` lines ending with `data: [DONE]`.

- [ ] **Step 6: Smoke test Gemini**

```bash
curl -s -H "x-goog-api-key: test" -H "Content-Type: application/json" \
  -H "Host: gemini.localhost" \
  --resolve "gemini.localhost:18080:127.0.0.1" \
  -d '{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}' \
  http://gemini.localhost:18080/v1/models/gemini-2.0-flash:generateContent | jq .
```

Expected: JSON with `"candidates"`, `"finishReason":"STOP"`.

- [ ] **Step 7: Stop server and commit**

```bash
kill $ZOLEM_PID
git add .
git commit -m "test: smoke test config for manual verification"
```

---

## Post-MVP Checklist

These are explicitly out of scope for this plan but should be tracked as future work:

- [ ] Real spec URLs: verify and update `specSources` in `main.go` with correct Anthropic/Gemini spec URLs
- [ ] WASM module compilation: document how to build `match.wasm` using TinyGo or Rust
- [ ] Spec refresh loop: add background goroutine in `main.go` that calls `fetcher.Get` + `validator.LoadRaw` on `cfg.Specs.RefreshInterval`
- [ ] `fsnotify` hot-reload: wire `loader.Watch()` goroutine in `main.go` to reload fixtures on filesystem changes
- [ ] Faker response mode
- [ ] Local LLM passthrough mode
