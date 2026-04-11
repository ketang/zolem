package main

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

type fakeLocalServer struct {
	addr   string
	closed bool
}

func (s *fakeLocalServer) Addr() string {
	return s.addr
}

func (s *fakeLocalServer) Close() error {
	s.closed = true
	return nil
}

func newTestLocalControlPlane(t *testing.T, opts localAdminOptions) *localControlPlane {
	t.Helper()

	control := newLocalControlPlane(opts, startupDeps{
		newFetcher: func(string, map[string]string) specFetcher {
			return fakeFetcher{
				"anthropic:v1":  {err: errFetchDisabled()},
				"openai:v1":     {err: errFetchDisabled()},
				"gemini:v1":     {err: errFetchDisabled()},
				"gemini:v1beta": {err: errFetchDisabled()},
			}
		},
		logf: func(string, ...any) {},
	})
	control.startServer = func(spec runtimecfg.ListenerSpec, tls localTLSConfig, handler http.Handler) (localServer, error) {
		addr := spec.Addr
		if addr == "127.0.0.1:0" {
			addr = "127.0.0.1:19001"
		}
		if spec.TLS && !tls.enabled() {
			return nil, errors.New("missing TLS config")
		}
		return &fakeLocalServer{addr: addr}, nil
	}
	t.Cleanup(func() {
		_ = control.Close()
	})
	return control
}

func errFetchDisabled() error {
	return errors.New("fetch disabled")
}

func TestLocalAdminHandler_ProfileCRUD(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	putReq := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`))
	putResp := doRequest(t, handler, putReq)
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("put status: got %d, want 200", putResp.StatusCode)
	}

	getReq := httptestRequest(http.MethodGet, "/_zolem/profiles", bytes.NewBuffer(nil))
	getResp := doRequest(t, handler, getReq)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status: got %d, want 200", getResp.StatusCode)
	}
	var profiles []map[string]any
	decodeJSON(t, getResp.Body, &profiles)
	if len(profiles) != 1 || profiles[0]["name"] != "demo" {
		t.Fatalf("profiles: got %#v", profiles)
	}
	if profiles[0]["error_type"] != "" {
		t.Fatalf("error_type: got %#v, want empty default", profiles[0]["error_type"])
	}

	deleteReq := httptestRequest(http.MethodDelete, "/_zolem/profiles/demo", bytes.NewBuffer(nil))
	deleteResp := doRequest(t, handler, deleteReq)
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status: got %d, want 204", deleteResp.StatusCode)
	}
}

func TestLocalAdminHandler_ListenerCRUD(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	profileReq := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`))
	profileResp := doRequest(t, handler, profileReq)
	defer profileResp.Body.Close()
	if profileResp.StatusCode != http.StatusOK {
		t.Fatalf("profile put status: got %d, want 200", profileResp.StatusCode)
	}

	listenerReq := httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}`))
	listenerResp := doRequest(t, handler, listenerReq)
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusOK {
		t.Fatalf("listener put status: got %d, want 200", listenerResp.StatusCode)
	}
	var listener map[string]any
	decodeJSON(t, listenerResp.Body, &listener)
	if listener["base_url"] != "http://127.0.0.1:19001" {
		t.Fatalf("base_url: got %#v, want http://127.0.0.1:19001", listener["base_url"])
	}

	listReq := httptestRequest(http.MethodGet, "/_zolem/listeners", bytes.NewBuffer(nil))
	listResp := doRequest(t, handler, listReq)
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("listener list status: got %d, want 200", listResp.StatusCode)
	}
	var listeners []map[string]any
	decodeJSON(t, listResp.Body, &listeners)
	if len(listeners) != 1 || listeners[0]["name"] != "openai-demo" {
		t.Fatalf("listeners: got %#v", listeners)
	}

	deleteProfileReq := httptestRequest(http.MethodDelete, "/_zolem/profiles/demo", bytes.NewBuffer(nil))
	deleteProfileResp := doRequest(t, handler, deleteProfileReq)
	defer deleteProfileResp.Body.Close()
	if deleteProfileResp.StatusCode != http.StatusConflict {
		t.Fatalf("profile delete status: got %d, want 409", deleteProfileResp.StatusCode)
	}

	deleteListenerReq := httptestRequest(http.MethodDelete, "/_zolem/listeners/openai-demo", bytes.NewBuffer(nil))
	deleteListenerResp := doRequest(t, handler, deleteListenerReq)
	defer deleteListenerResp.Body.Close()
	if deleteListenerResp.StatusCode != http.StatusNoContent {
		t.Fatalf("listener delete status: got %d, want 204", deleteListenerResp.StatusCode)
	}
}

func TestLocalAdminHandler_ProfileRejectsUnsupportedBackend(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	req := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"bogus"}`))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestLocalAdminHandler_FixtureProfileRequiresFixturesDirAtListenerCreate(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	profileReq := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"fixture"}`))
	profileResp := doRequest(t, handler, profileReq)
	defer profileResp.Body.Close()
	if profileResp.StatusCode != http.StatusOK {
		t.Fatalf("profile put status: got %d, want 200", profileResp.StatusCode)
	}

	listenerReq := httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}`))
	listenerResp := doRequest(t, handler, listenerReq)
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("listener put status: got %d, want 400", listenerResp.StatusCode)
	}
}

func TestLocalAdminHandler_ProfileRejectsInvalidFixtureNamespace(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	req := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"fixture","fixture_namespace":"../escape"}`))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestLocalAdminHandler_ProfileRejectsInvalidResponseModelPolicy(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	req := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem","response_model_policy":"bogus"}`))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestLocalAdminHandler_ProfileAllowsErrorBackend(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	req := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"error","error_type":"rate_limit"}`))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var profile map[string]any
	decodeJSON(t, resp.Body, &profile)
	if profile["error_type"] != "rate_limit" {
		t.Fatalf("error_type: got %#v, want rate_limit", profile["error_type"])
	}
}

func TestLocalAdminHandler_ProfileRejectsErrorBackendWithoutErrorType(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	req := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"error"}`))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestLocalAdminHandler_ProfileRejectsErrorTypeWithoutErrorBackend(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	req := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem","error_type":"rate_limit"}`))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestRunLocalAdmin_RejectsNonLoopbackAddr(t *testing.T) {
	var listenCalled bool
	err := runLocalAdmin(localAdminOptions{Addr: "0.0.0.0:8090"}, startupDeps{
		listen: func(string, http.Handler) error {
			listenCalled = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected invalid local admin addr error")
	}
	if listenCalled {
		t.Fatal("listen should not be called for invalid local admin addr")
	}
}

func TestRunLocalAdmin_UsesTLSWhenConfigured(t *testing.T) {
	var plainCalled bool
	var tlsCalled bool
	err := runLocalAdmin(localAdminOptions{
		Addr: "127.0.0.1:8090",
		TLS: localTLSConfig{
			CertFile: "cert.pem",
			KeyFile:  "key.pem",
		},
	}, startupDeps{
		listen: func(string, http.Handler) error {
			plainCalled = true
			return nil
		},
		listenTLS: func(string, string, string, http.Handler) error {
			tlsCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runLocalAdmin: %v", err)
	}
	if plainCalled {
		t.Fatal("plain listener should not be called when local admin TLS is configured")
	}
	if !tlsCalled {
		t.Fatal("TLS listener should be called when local admin TLS is configured")
	}
}

func TestLocalAdminHandler_TLSListenerUsesHTTPSBaseURL(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{
		TLS: localTLSConfig{
			CertFile: "cert.pem",
			KeyFile:  "key.pem",
		},
	})
	handler := buildLocalAdminHandler(control)

	profileReq := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`))
	profileResp := doRequest(t, handler, profileReq)
	defer profileResp.Body.Close()
	if profileResp.StatusCode != http.StatusOK {
		t.Fatalf("profile put status: got %d, want 200", profileResp.StatusCode)
	}

	listenerReq := httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo","tls":true}`))
	listenerResp := doRequest(t, handler, listenerReq)
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusOK {
		t.Fatalf("listener put status: got %d, want 200", listenerResp.StatusCode)
	}
	var listener map[string]any
	decodeJSON(t, listenerResp.Body, &listener)
	if listener["base_url"] != "https://127.0.0.1:19001" {
		t.Fatalf("base_url: got %#v, want https://127.0.0.1:19001", listener["base_url"])
	}
	if listener["tls"] != true {
		t.Fatalf("tls: got %#v, want true", listener["tls"])
	}
}

func TestLocalAdminHandler_TLSListenerRequiresTLSConfig(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	profileReq := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`))
	profileResp := doRequest(t, handler, profileReq)
	defer profileResp.Body.Close()
	if profileResp.StatusCode != http.StatusOK {
		t.Fatalf("profile put status: got %d, want 200", profileResp.StatusCode)
	}

	listenerReq := httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo","tls":true}`))
	listenerResp := doRequest(t, handler, listenerReq)
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("listener put status: got %d, want 400", listenerResp.StatusCode)
	}
}
