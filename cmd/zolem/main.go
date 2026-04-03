// cmd/zolem/main.go
package main

import (
	"context"
	"encoding/json"
	"flag"
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

func main() {
	cfgPath := flag.String("config", "zolem.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	validator := specs.NewValidator()
	specSources := map[string]string{
		"anthropic:v1":  "https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/main/api.json",
		"openai:v1":     "https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml",
		"gemini:v1":     "https://generativelanguage.googleapis.com/$discovery/rest?version=v1",
		"gemini:v1beta": "https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta",
	}
	fetcher := specs.NewFetcher(cfg.Specs.CacheDir, specSources)
	for _, key := range []string{"anthropic:v1", "openai:v1", "gemini:v1", "gemini:v1beta"} {
		provider, version := splitKey(key)
		if data, err := fetcher.Get(provider, version); err == nil {
			if err := validator.LoadRaw(provider, version, data); err != nil {
				log.Printf("warn: failed to load spec %s: %v", key, err)
			}
		} else {
			log.Printf("warn: failed to fetch spec %s: %v (validation disabled)", key, err)
		}
	}

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
	if i := strings.IndexByte(key, ':'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}
