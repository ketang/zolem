// internal/router/router_test.go
package router_test

import (
	"testing"

	"zolem.dev/zolem/internal/config"
	"zolem.dev/zolem/internal/router"
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
	r := router.New(routes())
	ctx, ok := r.Match("acme.api.openai.zolem.dev")
	if !ok {
		t.Fatal("expected match")
	}
	if ctx.Provider != "openai" {
		t.Errorf("provider: got %q, want openai", ctx.Provider)
	}
}
