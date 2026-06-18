package main

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

type fakeLocalServer struct {
	addr   string
	closed bool
	err    error
}

func (s *fakeLocalServer) Addr() string {
	return s.addr
}

func (s *fakeLocalServer) Close() error {
	s.closed = true
	return s.err
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

func TestLocalAdminHandler_ListenerNamedCallsCRUDAndCallsSubresource(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	profileResp := doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`)))
	defer profileResp.Body.Close()
	if profileResp.StatusCode != http.StatusOK {
		t.Fatalf("profile put status: got %d, want 200", profileResp.StatusCode)
	}

	listenerResp := doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/listeners/calls", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}`)))
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusOK {
		t.Fatalf("listener put status: got %d, want 200", listenerResp.StatusCode)
	}
	var listener map[string]any
	decodeJSON(t, listenerResp.Body, &listener)
	if listener["name"] != "calls" {
		t.Fatalf("listener name: got %#v, want calls", listener["name"])
	}

	getResp := doRequest(t, handler, httptestRequest(http.MethodGet, "/_zolem/listeners/calls", bytes.NewBuffer(nil)))
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("listener get status: got %d, want 200", getResp.StatusCode)
	}
	var fetched map[string]any
	decodeJSON(t, getResp.Body, &fetched)
	if fetched["name"] != "calls" {
		t.Fatalf("fetched listener name: got %#v, want calls", fetched["name"])
	}

	control.mu.Lock()
	rec := control.listeners["calls"].recorder
	control.mu.Unlock()
	rec.NextCallID()
	rec.Record(RecordedCall{CallID: 1, Listener: "calls"})

	callsResp := doRequest(t, handler, httptestRequest(http.MethodGet, "/_zolem/listeners/calls/calls", bytes.NewBuffer(nil)))
	defer callsResp.Body.Close()
	if callsResp.StatusCode != http.StatusOK {
		t.Fatalf("calls get status: got %d, want 200", callsResp.StatusCode)
	}
	var callsBody map[string]any
	decodeJSON(t, callsResp.Body, &callsBody)
	calls, ok := callsBody["calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("calls: got %#v, want 1", callsBody["calls"])
	}

	deleteResp := doRequest(t, handler, httptestRequest(http.MethodDelete, "/_zolem/listeners/calls", bytes.NewBuffer(nil)))
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("listener delete status: got %d, want 204", deleteResp.StatusCode)
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

func TestLocalAdminHandler_ListenerCapsDefaultAndOverride(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	profileReq := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`))
	doRequest(t, handler, profileReq).Body.Close()

	// Default caps.
	listenerReq := httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}`))
	listenerResp := doRequest(t, handler, listenerReq)
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusOK {
		t.Fatalf("listener put status: got %d, want 200", listenerResp.StatusCode)
	}
	var view map[string]any
	decodeJSON(t, listenerResp.Body, &view)
	want := DefaultRecordCaps()
	if int(view["record_request_body_cap_bytes"].(float64)) != want.RequestBodyCapBytes {
		t.Fatalf("default req cap: got %v, want %d", view["record_request_body_cap_bytes"], want.RequestBodyCapBytes)
	}
	if int(view["record_response_body_cap_bytes"].(float64)) != want.ResponseBodyCapBytes {
		t.Fatalf("default resp cap: got %v, want %d", view["record_response_body_cap_bytes"], want.ResponseBodyCapBytes)
	}
	if int(view["record_stream_event_cap"].(float64)) != want.StreamEventCap {
		t.Fatalf("default stream cap: got %v, want %d", view["record_stream_event_cap"], want.StreamEventCap)
	}

	// Override caps.
	overrideReq := httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo","record_request_body_cap_bytes":42,"record_response_body_cap_bytes":99,"record_stream_event_cap":7}`))
	overrideResp := doRequest(t, handler, overrideReq)
	defer overrideResp.Body.Close()
	if overrideResp.StatusCode != http.StatusOK {
		t.Fatalf("override put status: got %d, want 200", overrideResp.StatusCode)
	}
	var view2 map[string]any
	decodeJSON(t, overrideResp.Body, &view2)
	if int(view2["record_request_body_cap_bytes"].(float64)) != 42 {
		t.Fatalf("override req cap: got %v, want 42", view2["record_request_body_cap_bytes"])
	}
	if int(view2["record_response_body_cap_bytes"].(float64)) != 99 {
		t.Fatalf("override resp cap: got %v, want 99", view2["record_response_body_cap_bytes"])
	}
	if int(view2["record_stream_event_cap"].(float64)) != 7 {
		t.Fatalf("override stream cap: got %v, want 7", view2["record_stream_event_cap"])
	}
}

func TestLocalAdminHandler_ListenerCapsRejectNonPositive(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	profileReq := httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`))
	doRequest(t, handler, profileReq).Body.Close()

	req := httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo","record_request_body_cap_bytes":0}`))
	resp := doRequest(t, handler, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
}

func TestLocalAdminHandler_ListenerCalls_GetAndDelete(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`))).Body.Close()
	doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}`))).Body.Close()

	// Seed recorder directly.
	control.mu.Lock()
	rec := control.listeners["openai-demo"].recorder
	control.mu.Unlock()
	rec.NextCallID()
	rec.Record(RecordedCall{CallID: 1, Listener: "openai-demo"})
	rec.NextCallID()
	rec.Record(RecordedCall{CallID: 2, Listener: "openai-demo"})

	// GET returns the calls.
	getResp := doRequest(t, handler, httptestRequest(http.MethodGet, "/_zolem/listeners/openai-demo/calls", bytes.NewBuffer(nil)))
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status: got %d, want 200", getResp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, getResp.Body, &body)
	calls, ok := body["calls"].([]any)
	if !ok || len(calls) != 2 {
		t.Fatalf("calls: got %#v, want 2", body["calls"])
	}

	// DELETE clears and returns count.
	delResp := doRequest(t, handler, httptestRequest(http.MethodDelete, "/_zolem/listeners/openai-demo/calls", bytes.NewBuffer(nil)))
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status: got %d, want 200", delResp.StatusCode)
	}
	var delBody map[string]any
	decodeJSON(t, delResp.Body, &delBody)
	if int(delBody["cleared"].(float64)) != 2 {
		t.Fatalf("cleared: got %v, want 2", delBody["cleared"])
	}

	// DELETE on empty returns 0.
	delResp2 := doRequest(t, handler, httptestRequest(http.MethodDelete, "/_zolem/listeners/openai-demo/calls", bytes.NewBuffer(nil)))
	defer delResp2.Body.Close()
	var delBody2 map[string]any
	decodeJSON(t, delResp2.Body, &delBody2)
	if int(delBody2["cleared"].(float64)) != 0 {
		t.Fatalf("cleared on empty: got %v, want 0", delBody2["cleared"])
	}
}

func TestLocalAdminHandler_LoadedFixtureInfoDoesNotSurfaceInWarningsHeader(t *testing.T) {
	fixturesDir := t.TempDir()
	writeLocalFixture(t, fixturesDir, "wasm-only", "anthropic", "v1", []byte(`{"id":"wasm-only","type":"message","role":"assistant","content":[{"type":"text","text":"x"}],"model":"claude-3-5-sonnet-20241022","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`), localAlwaysMatchWASM)

	control := newTestLocalControlPlane(t, localAdminOptions{FixturesDir: fixturesDir})
	handler := buildLocalAdminHandler(control)

	profileResp := doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"fixture"}`)))
	defer profileResp.Body.Close()
	if profileResp.StatusCode != http.StatusOK {
		t.Fatalf("profile put status: got %d, want 200", profileResp.StatusCode)
	}

	listenerResp := doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/listeners/anthropic-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"anthropic","profile":"demo"}`)))
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusOK {
		t.Fatalf("listener put status: got %d, want 200", listenerResp.StatusCode)
	}
	warningsHeader := listenerResp.Header.Get("X-Zolem-Warnings")
	if strings.Contains(warningsHeader, "loaded fixture") {
		t.Fatalf("X-Zolem-Warnings contained fixture load info: %q", warningsHeader)
	}
}

func TestLocalAdminHandler_ListenerCalls_NotFound(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	getResp := doRequest(t, handler, httptestRequest(http.MethodGet, "/_zolem/listeners/missing/calls", bytes.NewBuffer(nil)))
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("get status: got %d, want 404", getResp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, getResp.Body, &body)
	if _, ok := body["error"]; !ok {
		t.Fatalf("body missing error: %#v", body)
	}

	delResp := doRequest(t, handler, httptestRequest(http.MethodDelete, "/_zolem/listeners/missing/calls", bytes.NewBuffer(nil)))
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete status: got %d, want 404", delResp.StatusCode)
	}
}

func TestLocalAdminHandler_ListenerCalls_MethodNotAllowed(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(`{"backend":"lorem"}`))).Body.Close()
	doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/listeners/openai-demo", bytes.NewBufferString(`{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}`))).Body.Close()

	resp := doRequest(t, handler, httptestRequest(http.MethodPost, "/_zolem/listeners/openai-demo/calls", bytes.NewBuffer(nil)))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != "GET, DELETE" {
		t.Fatalf("Allow: got %q, want %q", got, "GET, DELETE")
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

func TestLocalAdminHandler_InvalidResourceNamesAndMethods(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	tests := []struct {
		method string
		path   string
		status int
		allow  string
	}{
		{method: http.MethodGet, path: "/_zolem/profiles/nested/name", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/_zolem/profiles/demo", status: http.StatusMethodNotAllowed, allow: "PUT, DELETE"},
		{method: http.MethodGet, path: "/_zolem/listeners/nested/name", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/_zolem/listeners/demo", status: http.StatusNotFound},
		{method: http.MethodPost, path: "/_zolem/listeners/demo", status: http.StatusMethodNotAllowed, allow: "PUT, GET, DELETE"},
		{method: http.MethodGet, path: "/_zolem/listeners/nested/name/calls", status: http.StatusNotFound},
		{method: http.MethodGet, path: "/_zolem/missing", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		resp := doRequest(t, handler, httptestRequest(tt.method, tt.path, bytes.NewBuffer(nil)))
		resp.Body.Close()
		if resp.StatusCode != tt.status {
			t.Fatalf("%s %s status = %d, want %d", tt.method, tt.path, resp.StatusCode, tt.status)
		}
		if tt.allow != "" && resp.Header.Get("Allow") != tt.allow {
			t.Fatalf("%s %s Allow = %q, want %q", tt.method, tt.path, resp.Header.Get("Allow"), tt.allow)
		}
	}
}

func TestLocalAdminHandler_DecodeRequestJSONRejectsUnknownAndExtraObjects(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	handler := buildLocalAdminHandler(control)

	tests := []string{
		`{"backend":"lorem","unexpected":true}`,
		`{"backend":"lorem"} {"backend":"faker"}`,
		`{`,
	}
	for _, body := range tests {
		resp := doRequest(t, handler, httptestRequest(http.MethodPut, "/_zolem/profiles/demo", bytes.NewBufferString(body)))
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestLocalControlPlaneListenerErrorBranches(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	if _, err := control.UpsertProfile("demo", localProfilePayload{Backend: "lorem"}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	zero := 0
	for _, payload := range []localListenerPayload{
		{Addr: "127.0.0.1:0", Provider: "openai", Profile: "missing"},
		{Addr: "127.0.0.1:0", Provider: "bogus", Profile: "demo"},
		{Addr: "127.0.0.1:0", Provider: "openai", Profile: "demo", RecordResponseBodyCapBytes: &zero},
		{Addr: "127.0.0.1:0", Provider: "openai", Profile: "demo", RecordStreamEventCap: &zero},
	} {
		if _, _, err := control.UpsertListener("bad", payload); err == nil {
			t.Fatalf("UpsertListener(%+v) unexpectedly succeeded", payload)
		}
	}

	startErr := errors.New("listen failed")
	control.startServer = func(runtimecfg.ListenerSpec, localTLSConfig, http.Handler) (localServer, error) {
		return nil, startErr
	}
	if _, _, err := control.UpsertListener("bad", localListenerPayload{Addr: "127.0.0.1:0", Provider: "openai", Profile: "demo"}); !errors.Is(err, startErr) {
		t.Fatalf("start server error = %v, want %v", err, startErr)
	}
}

func TestLocalControlPlaneListAndClearCallsWithoutRecorder(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	if _, err := control.UpsertProfile("demo", localProfilePayload{Backend: "lorem"}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if _, _, err := control.UpsertListener("openai-demo", localListenerPayload{Addr: "127.0.0.1:0", Provider: "openai", Profile: "demo"}); err != nil {
		t.Fatalf("upsert listener: %v", err)
	}
	control.mu.Lock()
	control.listeners["openai-demo"].recorder = nil
	control.mu.Unlock()

	calls, err := control.ListCalls("openai-demo")
	if err != nil {
		t.Fatalf("ListCalls: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v, want empty", calls)
	}
	cleared, err := control.ClearCalls("openai-demo")
	if err != nil {
		t.Fatalf("ClearCalls: %v", err)
	}
	if cleared != 0 {
		t.Fatalf("cleared = %d, want 0", cleared)
	}
}

func TestLocalControlPlaneCloseAndDeleteListenerPropagateServerErrors(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	closeErr := errors.New("close failed")
	if _, err := control.UpsertProfile("demo", localProfilePayload{Backend: "lorem"}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	control.startServer = func(spec runtimecfg.ListenerSpec, _ localTLSConfig, _ http.Handler) (localServer, error) {
		return &fakeLocalServer{addr: spec.Addr, err: closeErr}, nil
	}
	if _, _, err := control.UpsertListener("openai-demo", localListenerPayload{Addr: "127.0.0.1:19002", Provider: "openai", Profile: "demo"}); err != nil {
		t.Fatalf("upsert listener: %v", err)
	}
	if err := control.DeleteListener("openai-demo"); !errors.Is(err, closeErr) {
		t.Fatalf("DeleteListener error = %v, want %v", err, closeErr)
	}

	if _, _, err := control.UpsertListener("openai-demo", localListenerPayload{Addr: "127.0.0.1:19002", Provider: "openai", Profile: "demo"}); err != nil {
		t.Fatalf("upsert listener again: %v", err)
	}
	if err := control.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want %v", err, closeErr)
	}
}

func TestStartLocalServerHTTPAndTLSMissingConfig(t *testing.T) {
	server, err := startLocalServer(runtimecfg.ListenerSpec{Name: "plain", Addr: "127.0.0.1:0"}, localTLSConfig{}, http.NewServeMux())
	if err != nil {
		t.Fatalf("startLocalServer HTTP: %v", err)
	}
	if server.Addr() == "" {
		t.Fatal("HTTP server Addr is empty")
	}
	if err := server.Close(); err != nil {
		t.Fatalf("close HTTP server: %v", err)
	}

	_, err = startLocalServer(runtimecfg.ListenerSpec{Name: "tls", Addr: "127.0.0.1:0", TLS: true}, localTLSConfig{}, http.NewServeMux())
	if err == nil || !strings.Contains(err.Error(), "requires local TLS cert and key") {
		t.Fatalf("TLS missing config error = %v", err)
	}
}

func TestLocalBaseURL(t *testing.T) {
	plain := localBaseURL(runtimecfg.ListenerSpec{Addr: "127.0.0.1:19001"})
	if plain != "http://127.0.0.1:19001" {
		t.Fatalf("plain base URL = %q", plain)
	}
	tls := localBaseURL(runtimecfg.ListenerSpec{Addr: "127.0.0.1:19001", TLS: true})
	if tls != "https://127.0.0.1:19001" {
		t.Fatalf("TLS base URL = %q", tls)
	}
}
