package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

var localGeneratorWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x15, 0x04, 0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x60, 0x02, 0x7f, 0x7f, 0x00, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f, 0x60, 0x01, 0x7f, 0x00,
	0x03, 0x07, 0x06, 0x00, 0x01, 0x02, 0x00, 0x00, 0x03, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07,
	0x4f, 0x07, 0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x05, 0x61, 0x6c, 0x6c,
	0x6f, 0x63, 0x00, 0x00, 0x07, 0x64, 0x65, 0x61, 0x6c, 0x6c, 0x6f, 0x63, 0x00, 0x01, 0x08,
	0x67, 0x65, 0x6e, 0x65, 0x72, 0x61, 0x74, 0x65, 0x00, 0x02, 0x0a, 0x72, 0x65, 0x73, 0x75,
	0x6c, 0x74, 0x5f, 0x70, 0x74, 0x72, 0x00, 0x03, 0x0a, 0x72, 0x65, 0x73, 0x75, 0x6c, 0x74,
	0x5f, 0x6c, 0x65, 0x6e, 0x00, 0x04, 0x0b, 0x72, 0x65, 0x73, 0x75, 0x6c, 0x74, 0x5f, 0x66,
	0x72, 0x65, 0x65, 0x00, 0x05, 0x0a, 0x1d, 0x06, 0x05, 0x00, 0x41, 0x80, 0x08, 0x0b, 0x02,
	0x00, 0x0b, 0x04, 0x00, 0x41, 0x01, 0x0b, 0x05, 0x00, 0x41, 0x80, 0x10, 0x0b, 0x04, 0x00,
	0x41, 0x17, 0x0b, 0x02, 0x00, 0x0b, 0x0b, 0x1e, 0x01, 0x00, 0x41, 0x80, 0x10, 0x0b, 0x17,
	0x5b, 0x22, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x20, 0x22, 0x2c, 0x22, 0x66, 0x72, 0x6f, 0x6d,
	0x20, 0x57, 0x41, 0x53, 0x4d, 0x2e, 0x22, 0x5d,
}

func TestLocalAdminHandler_ProfileAllowsWASMBackend(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	payload := map[string]any{
		"backend":                  "wasm",
		"wasm_module_base64":       base64.StdEncoding.EncodeToString(localGeneratorWASM),
		"wasm_generate_timeout_ms": 100,
		"stream_delay": map[string]any{
			"mode": "fixed",
			"ms":   0,
		},
	}
	body, _ := json.Marshal(payload)
	resp := doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBuffer(body)))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var profile map[string]any
	decodeJSON(t, resp.Body, &profile)
	if profile["backend"] != "wasm" {
		t.Fatalf("backend: got %#v, want wasm", profile["backend"])
	}
}

func TestBuildLocalStartupAppForRuntime_WASMBackendOpenAI(t *testing.T) {
	app, _, err := buildLocalStartupAppForRuntime(runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     "openai-demo",
			Addr:     "127.0.0.1:12001",
			Provider: "openai",
			Profile:  "demo",
		},
		Profile: runtimecfg.RuntimeProfile{
			Name:             "demo",
			Backend:          runtimecfg.BackendWASM,
			WASMModuleBase64: base64.StdEncoding.EncodeToString(localGeneratorWASM),
		},
	}, "", runtimecfg.NewProfileCounters(), startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err != nil {
		t.Fatalf("buildLocalStartupAppForRuntime: %v", err)
	}
	defer app.close()

	req := httptestRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	resp := doRequest(t, app.handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var payload map[string]any
	decodeJSON(t, resp.Body, &payload)
	choices := payload["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if got := message["content"]; got != "Hello from WASM." {
		t.Fatalf("content: got %#v, want Hello from WASM.", got)
	}
}

func TestBuildLocalStartupAppForRuntime_WASMBackendStreaming(t *testing.T) {
	app, _, err := buildLocalStartupAppForRuntime(runtimecfg.ListenerRuntime{
		Spec: runtimecfg.ListenerSpec{
			Name:     "openai-demo",
			Addr:     "127.0.0.1:12001",
			Provider: "openai",
			Profile:  "demo",
		},
		Profile: runtimecfg.RuntimeProfile{
			Name:             "demo",
			Backend:          runtimecfg.BackendWASM,
			WASMModuleBase64: base64.StdEncoding.EncodeToString(localGeneratorWASM),
		},
	}, "", runtimecfg.NewProfileCounters(), startupDeps{
		newFetcher: disabledTestFetcher,
	})
	if err != nil {
		t.Fatalf("buildLocalStartupAppForRuntime: %v", err)
	}
	defer app.close()

	req := httptestRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	resp := doRequest(t, app.handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := body.String()
	if !strings.Contains(got, `"content":"Hello "`) || !strings.Contains(got, `"content":"from WASM."`) {
		t.Fatalf("stream body did not contain WASM chunks: %s", got)
	}
}
