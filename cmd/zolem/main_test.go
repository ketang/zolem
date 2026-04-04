package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zolem.dev/zolem/internal/config"
	"zolem.dev/zolem/internal/fixture"
	"zolem.dev/zolem/internal/provider/anthropic"
	"zolem.dev/zolem/internal/provider/gemini"
	"zolem.dev/zolem/internal/provider/openai"
	"zolem.dev/zolem/internal/response"
	"zolem.dev/zolem/internal/router"
	"zolem.dev/zolem/internal/specs"
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Zolem-Error") != "true" {
		t.Error("expected X-Zolem-Error header")
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", resp.StatusCode)
	}
}
