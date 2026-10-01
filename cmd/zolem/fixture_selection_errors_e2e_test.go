package main_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trapSelectorWASM is a selector.wasm whose `select` export executes
// `unreachable`, so every selection traps. Equivalent WAT:
//
//	(module
//	  (memory (export "memory") 1)
//	  (func (export "select") (param i32 i32) (result i32 i32)
//	    unreachable))
var trapSelectorWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x08, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x02, 0x7f, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x13, 0x02,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x06, 's', 'e', 'l', 'e', 'c', 't', 0x00, 0x00,
	0x0a, 0x05, 0x01, 0x03, 0x00, 0x00, 0x0b,
}

// exhaustYAML puts an on_exhaust: error sequence first, then an entry whose
// CEL expression errors on bodies without metadata.tenant, then a catch-all.
func exhaustYAML(c verbatimCase) string {
	return "provider: " + c.provider + "\nversion: " + c.version + "\nfixtures:\n" +
		"  - expression: '" + c.matchExpr + "'\n    sequence: {id: s, on_exhaust: error, steps: [ok]}\n" +
		"  - expression: 'body[\"metadata\"][\"tenant\"] == \"acme\"'\n    fixture: rl\n" +
		"  - expression: 'true'\n    fixture: ok\n"
}

func TestE2E_FixtureSequenceOnExhaustError(t *testing.T) {
	for _, c := range verbatimCases() {
		t.Run(c.provider, func(t *testing.T) {
			dir := writeVerbatimNamespace(t, c, nil)
			if err := os.WriteFile(filepath.Join(dir, "fixtures.yaml"), []byte(exhaustYAML(c)), 0o644); err != nil {
				t.Fatal(err)
			}
			svc := startProviderService(t, c.provider, "-local-backend", "fixture", "-local-fixtures-dir", dir)
			hdr := append([]string{"Content-Type: application/json"}, c.headers...)

			resp, body := doRequest(t, svc.baseURL, http.MethodPost, c.path, c.requestBody("ratelimit"), hdr...)
			if resp.StatusCode != 200 {
				t.Fatalf("first request: status %d: %s", resp.StatusCode, body)
			}
			if resp.Header.Get("X-Zolem-Error") != "" {
				t.Fatalf("first request unexpectedly tagged X-Zolem-Error")
			}

			resp, body = doRequest(t, svc.baseURL, http.MethodPost, c.path, c.requestBody("ratelimit"), hdr...)
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("second request: status %d, want 500: %s", resp.StatusCode, body)
			}
			if resp.Header.Get("X-Zolem-Error") != "true" {
				t.Fatalf("second request: missing X-Zolem-Error: true")
			}
			wantMsg := `fixture sequence "s" in namespace "` + c.provider + ":" + c.version + `" exhausted`
			m := jsonMap(t, body)
			switch c.provider {
			case "openai":
				e, _ := m["error"].(map[string]any)
				if e["type"] != "server_error" || e["message"] != wantMsg {
					t.Fatalf("openai envelope: %s", body)
				}
			case "anthropic":
				e, _ := m["error"].(map[string]any)
				if m["type"] != "error" || e["type"] != "api_error" || e["message"] != wantMsg {
					t.Fatalf("anthropic envelope: %s", body)
				}
			case "gemini":
				e, _ := m["error"].(map[string]any)
				if e["code"] != float64(500) || e["status"] != "INTERNAL" || e["message"] != wantMsg {
					t.Fatalf("gemini envelope: %s", body)
				}
			case "ollama":
				if m["error"] != wantMsg {
					t.Fatalf("ollama envelope: %s", body)
				}
			}

			// A CEL error on an earlier entry must not hide the catch-all.
			resp, body = doRequest(t, svc.baseURL, http.MethodPost, c.path, c.requestBody("hello"), hdr...)
			if resp.StatusCode != 200 || !strings.Contains(string(body), "fixture ok") {
				t.Fatalf("hello: status %d, want ok fixture: %s", resp.StatusCode, body)
			}
		})
	}
}

func TestE2E_FixtureSelectorWASMTrapIsInfrastructureError(t *testing.T) {
	repoRoot := repoRoot(t)
	c := verbatimCases()[0] // openai
	nsDir := filepath.Join(t.TempDir(), "fixtures")
	mustMkdir(t, nsDir)
	writeYAMLNamespaceFixture(t, nsDir, "ok", "ok")
	if err := os.WriteFile(filepath.Join(nsDir, "selector.wasm"), trapSelectorWASM, 0o644); err != nil {
		t.Fatal(err)
	}
	admin := startLocalAdminServiceWithFixtures(t, repoRoot, nsDir)
	t.Cleanup(admin.Close)
	base := createRuntimeListener(t, admin, "openai", map[string]any{"backend": "fixture"})

	resp, body := doRequest(t, base, http.MethodPost, c.path, c.requestBody("hello"),
		"Content-Type: application/json", "Authorization: Bearer sk-test")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Zolem-Error") != "true" {
		t.Fatalf("missing X-Zolem-Error: true")
	}
	if !strings.Contains(string(body), "fixture selection failed") {
		t.Fatalf("body missing selection failure message: %s", body)
	}
}
