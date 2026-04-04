// internal/config/config_test.go
package config_test

import (
	"os"
	"testing"

	"zolem.dev/zolem/internal/config"
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

func TestLoadFileNotFound(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "*.yaml")
	f.WriteString(":\tinvalid: yaml: [")
	f.Close()

	_, err := config.Load(f.Name())
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadInvalidMode(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "*.yaml")
	f.WriteString("mode: turbo\n")
	f.Close()

	_, err := config.Load(f.Name())
	if err == nil {
		t.Error("expected error for invalid mode")
	}
}
