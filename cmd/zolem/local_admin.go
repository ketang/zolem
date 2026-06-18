package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

type localAdminOptions struct {
	Addr        string
	FixturesDir string
	TLS         localTLSConfig
}

type localProfilePayload struct {
	Backend               string                 `json:"backend"`
	BackendModel          string                 `json:"backend_model"`
	ErrorType             string                 `json:"error_type"`
	ResponseModelPolicy   string                 `json:"response_model_policy"`
	ResponseModel         string                 `json:"response_model"`
	FixtureNamespace      string                 `json:"fixture_namespace"`
	Seed                  *int64                 `json:"seed"`
	OllamaUpstream        string                 `json:"ollama_upstream"`
	WASMModuleBase64      string                 `json:"wasm_module_base64"`
	WASMGenerateTimeoutMS int                    `json:"wasm_generate_timeout_ms"`
	StreamDelay           runtimecfg.StreamDelay `json:"stream_delay"`
}

type localListenerPayload struct {
	Addr                       string `json:"addr"`
	Provider                   string `json:"provider"`
	Profile                    string `json:"profile"`
	TLS                        bool   `json:"tls,omitempty"`
	RecordRequestBodyCapBytes  *int   `json:"record_request_body_cap_bytes,omitempty"`
	RecordResponseBodyCapBytes *int   `json:"record_response_body_cap_bytes,omitempty"`
	RecordStreamEventCap       *int   `json:"record_stream_event_cap,omitempty"`
}

type localListenerView struct {
	Name                       string `json:"name"`
	Addr                       string `json:"addr"`
	Provider                   string `json:"provider"`
	Profile                    string `json:"profile"`
	Backend                    string `json:"backend"`
	TLS                        bool   `json:"tls,omitempty"`
	BaseURL                    string `json:"base_url"`
	RecordRequestBodyCapBytes  int    `json:"record_request_body_cap_bytes"`
	RecordResponseBodyCapBytes int    `json:"record_response_body_cap_bytes"`
	RecordStreamEventCap       int    `json:"record_stream_event_cap"`
}

func newLocalListenerView(runtime runtimecfg.ListenerRuntime, baseURL string, caps RecordCaps) localListenerView {
	spec := runtime.Spec
	return localListenerView{
		Name:                       spec.Name,
		Addr:                       spec.Addr,
		Provider:                   spec.Provider,
		Profile:                    spec.Profile,
		Backend:                    runtime.Profile.Backend,
		TLS:                        spec.TLS,
		BaseURL:                    baseURL,
		RecordRequestBodyCapBytes:  caps.RequestBodyCapBytes,
		RecordResponseBodyCapBytes: caps.ResponseBodyCapBytes,
		RecordStreamEventCap:       caps.StreamEventCap,
	}
}

type localServer interface {
	Addr() string
	Close() error
}

type managedLocalListener struct {
	runtime  runtimecfg.ListenerRuntime
	app      *startupApp
	server   localServer
	baseURL  string
	recorder Recorder
	caps     RecordCaps
}

type localControlPlane struct {
	deps        startupDeps
	store       *runtimecfg.Store
	fixturesDir string
	tls         localTLSConfig
	startServer func(runtimecfg.ListenerSpec, localTLSConfig, http.Handler) (localServer, error)
	counters    *runtimecfg.ProfileCounters

	mu        sync.Mutex
	listeners map[string]*managedLocalListener
}

func runLocalAdmin(opts localAdminOptions, deps startupDeps) error {
	deps = deps.withDefaults()

	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:8090"
	}
	if err := runtimecfg.ValidateLoopbackAddr(addr); err != nil {
		return fmt.Errorf("invalid local admin addr %q: %w", addr, err)
	}
	if err := opts.TLS.validate(); err != nil {
		return err
	}

	control := newLocalControlPlane(opts, deps)
	defer control.Close()

	deps.logf("zolem local admin on %s", addr)
	if opts.TLS.enabled() {
		return deps.listenTLS(addr, opts.TLS.CertFile, opts.TLS.KeyFile, buildLocalAdminHandler(control))
	}
	return deps.listen(addr, buildLocalAdminHandler(control))
}

func newLocalControlPlane(opts localAdminOptions, deps startupDeps) *localControlPlane {
	return &localControlPlane{
		deps:        deps.withDefaults(),
		store:       runtimecfg.NewStore(),
		fixturesDir: opts.FixturesDir,
		tls:         opts.TLS,
		startServer: startLocalServer,
		counters:    runtimecfg.NewProfileCounters(),
		listeners:   make(map[string]*managedLocalListener),
	}
}

func (c *localControlPlane) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error
	for name, listener := range c.listeners {
		if err := listener.server.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close listener %s: %w", name, err))
		}
		listener.app.close()
		if listener.recorder != nil {
			listener.recorder.Close()
		}
		delete(c.listeners, name)
		_ = c.store.DeleteListener(name)
	}
	return errors.Join(errs...)
}

func (c *localControlPlane) UpsertProfile(name string, payload localProfilePayload) (runtimecfg.RuntimeProfile, error) {
	profile := runtimecfg.RuntimeProfile{
		Name:                  name,
		Backend:               payload.Backend,
		BackendModel:          payload.BackendModel,
		ErrorType:             payload.ErrorType,
		ResponseModelPolicy:   payload.ResponseModelPolicy,
		ResponseModel:         payload.ResponseModel,
		FixtureNamespace:      payload.FixtureNamespace,
		Seed:                  payload.Seed,
		OllamaUpstream:        payload.OllamaUpstream,
		WASMModuleBase64:      payload.WASMModuleBase64,
		WASMGenerateTimeoutMS: payload.WASMGenerateTimeoutMS,
		StreamDelay:           payload.StreamDelay,
	}
	if profile.Backend == "" {
		profile.Backend = "lorem"
	}
	if err := runtimecfg.ValidateProfile(profile); err != nil {
		return runtimecfg.RuntimeProfile{}, err
	}
	if profile.Backend == runtimecfg.BackendWASM {
		g, err := wasmGeneratorForProfile(profile)
		if err != nil {
			return runtimecfg.RuntimeProfile{}, err
		}
		_ = g.Close(context.Background())
	}
	return c.store.UpsertProfile(profile)
}

func (c *localControlPlane) ListProfiles() []runtimecfg.RuntimeProfile {
	return c.store.ListProfiles()
}

func (c *localControlPlane) DeleteProfile(name string) error {
	return c.store.DeleteProfile(name)
}

func (c *localControlPlane) UpsertListener(name string, payload localListenerPayload) (localListenerView, []string, error) {
	// Snapshot the profile exactly once. Re-fetching it after validation would
	// open a TOCTOU window where a concurrent DeleteProfile lets the listener
	// bind with a zero-valued profile.
	profile, ok := c.store.GetProfile(payload.Profile)
	if !ok {
		return localListenerView{}, nil, runtimecfg.ErrProfileNotFound
	}

	spec := runtimecfg.ListenerSpec{
		Name:     name,
		Addr:     payload.Addr,
		Provider: payload.Provider,
		Profile:  payload.Profile,
		TLS:      payload.TLS,
	}
	if err := runtimecfg.ValidateListenerSpec(spec); err != nil {
		return localListenerView{}, nil, err
	}

	runtime := runtimecfg.ListenerRuntime{
		Spec:    spec,
		Profile: profile,
	}

	caps := DefaultRecordCaps()
	if payload.RecordRequestBodyCapBytes != nil {
		if *payload.RecordRequestBodyCapBytes <= 0 {
			return localListenerView{}, nil, errors.New("record_request_body_cap_bytes must be positive")
		}
		caps.RequestBodyCapBytes = *payload.RecordRequestBodyCapBytes
	}
	if payload.RecordResponseBodyCapBytes != nil {
		if *payload.RecordResponseBodyCapBytes <= 0 {
			return localListenerView{}, nil, errors.New("record_response_body_cap_bytes must be positive")
		}
		caps.ResponseBodyCapBytes = *payload.RecordResponseBodyCapBytes
	}
	if payload.RecordStreamEventCap != nil {
		if *payload.RecordStreamEventCap <= 0 {
			return localListenerView{}, nil, errors.New("record_stream_event_cap must be positive")
		}
		caps.StreamEventCap = *payload.RecordStreamEventCap
	}

	recorder := newInMemoryRecorder(name)
	app, warnings, err := buildLocalStartupAppForRuntime(runtime, c.fixturesDir, c.counters, recorder, caps, c.deps)
	if err != nil {
		return localListenerView{}, warnings, err
	}

	// Bind the new listener before touching the registry. The replace then
	// happens in a single critical section: any previously-registered listener
	// for this name is swapped out atomically with the new one. Holding c.mu
	// across the whole swap closes the gap where two concurrent PUTs could both
	// bind and have the loser orphan the winner's server + listener (a goroutine
	// and port leak that survived until process exit).
	server, err := c.startServer(spec, c.tls, app.handler)
	if err != nil {
		app.close()
		return localListenerView{}, warnings, err
	}

	actualSpec := spec
	actualSpec.Addr = server.Addr()
	actualRuntime := runtimecfg.ListenerRuntime{
		Spec:    actualSpec,
		Profile: profile,
	}
	// Reflect the resolved bound addr in the runtime the /_zolem/state endpoint
	// reports; otherwise it keeps showing the pre-bind request addr (e.g. :0).
	if app.setListenerAddr != nil {
		app.setListenerAddr(actualSpec.Addr)
	}

	view := newLocalListenerView(actualRuntime, localBaseURL(actualSpec), caps)

	c.mu.Lock()
	old := c.listeners[name]
	if _, err := c.store.UpsertListener(actualSpec); err != nil {
		c.mu.Unlock()
		_ = server.Close()
		app.close()
		return localListenerView{}, warnings, err
	}
	c.listeners[name] = &managedLocalListener{
		runtime:  actualRuntime,
		app:      app,
		server:   server,
		baseURL:  view.BaseURL,
		recorder: recorder,
		caps:     caps,
	}
	c.mu.Unlock()

	// Tear down the replaced listener outside the lock: server.Close() may block
	// up to its shutdown deadline, and we must not hold c.mu while it drains.
	if old != nil {
		_ = old.server.Close()
		old.app.close()
		if old.recorder != nil {
			old.recorder.Close()
		}
	}

	return view, warnings, nil
}

func (c *localControlPlane) ListListeners() []localListenerView {
	c.mu.Lock()
	defer c.mu.Unlock()

	specs := c.store.ListListeners()
	out := make([]localListenerView, 0, len(specs))
	for _, spec := range specs {
		entry := c.listeners[spec.Name]
		if entry == nil {
			// Defensive: the store spec and the managed-listener map are kept in
			// sync under c.mu, so this should not happen. Skip rather than panic
			// on a nil index if they ever diverge.
			continue
		}
		out = append(out, newLocalListenerView(entry.runtime, entry.baseURL, entry.caps))
	}
	return out
}

func (c *localControlPlane) GetListener(name string) (localListenerView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.listeners[name]
	if !ok {
		return localListenerView{}, runtimecfg.ErrListenerNotFound
	}
	return newLocalListenerView(entry.runtime, entry.baseURL, entry.caps), nil
}

func (c *localControlPlane) DeleteListener(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.listeners[name]
	if !ok {
		return runtimecfg.ErrListenerNotFound
	}
	if err := entry.server.Close(); err != nil {
		return err
	}
	entry.app.close()
	if entry.recorder != nil {
		entry.recorder.Close()
	}
	delete(c.listeners, name)
	return c.store.DeleteListener(name)
}

func (c *localControlPlane) ListCalls(name string) ([]RecordedCall, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.listeners[name]
	if !ok {
		return nil, runtimecfg.ErrListenerNotFound
	}
	if entry.recorder == nil {
		return []RecordedCall{}, nil
	}
	return entry.recorder.List(), nil
}

func (c *localControlPlane) ClearCalls(name string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.listeners[name]
	if !ok {
		return 0, runtimecfg.ErrListenerNotFound
	}
	if entry.recorder == nil {
		return 0, nil
	}
	return entry.recorder.Clear(), nil
}

func buildLocalAdminHandler(control *localControlPlane) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/_zolem/health":
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		case req.Method == http.MethodGet && req.URL.Path == "/_zolem/profiles":
			writeJSON(w, http.StatusOK, control.ListProfiles())
			return
		case req.Method == http.MethodGet && req.URL.Path == "/_zolem/listeners":
			writeJSON(w, http.StatusOK, control.ListListeners())
			return
		case strings.HasPrefix(req.URL.Path, "/_zolem/profiles/"):
			handleProfileResource(w, req, control)
			return
		case strings.HasPrefix(req.URL.Path, "/_zolem/listeners/"):
			handleListenerPath(w, req, control)
			return
		default:
			http.NotFound(w, req)
		}
	})
}

func handleListenerPath(w http.ResponseWriter, req *http.Request, control *localControlPlane) {
	rest := strings.TrimPrefix(req.URL.Path, "/_zolem/listeners/")
	segments := strings.Split(rest, "/")
	switch {
	case len(segments) == 1 && segments[0] != "":
		handleListenerResource(w, req, control, segments[0])
	case len(segments) == 2 && segments[0] != "" && segments[1] == "calls":
		handleListenerCalls(w, req, control, segments[0])
	default:
		http.NotFound(w, req)
	}
}

func handleListenerCalls(w http.ResponseWriter, req *http.Request, control *localControlPlane, name string) {
	switch req.Method {
	case http.MethodGet:
		calls, err := control.ListCalls(name)
		if err != nil {
			if errors.Is(err, runtimecfg.ErrListenerNotFound) {
				writeAdminError(w, http.StatusNotFound, err.Error())
				return
			}
			writeAdminError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if calls == nil {
			calls = []RecordedCall{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"calls": calls})
	case http.MethodDelete:
		cleared, err := control.ClearCalls(name)
		if err != nil {
			if errors.Is(err, runtimecfg.ErrListenerNotFound) {
				writeAdminError(w, http.StatusNotFound, err.Error())
				return
			}
			writeAdminError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cleared": cleared})
	default:
		w.Header().Set("Allow", "GET, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleProfileResource(w http.ResponseWriter, req *http.Request, control *localControlPlane) {
	name, ok := localResourceName("/_zolem/profiles/", req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}

	switch req.Method {
	case http.MethodPut:
		var payload localProfilePayload
		if err := decodeRequestJSON(req, &payload); err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
		profile, err := control.UpsertProfile(name, payload)
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, profile)
	case http.MethodDelete:
		err := control.DeleteProfile(name)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, runtimecfg.ErrProfileNotFound):
			writeAdminError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, runtimecfg.ErrProfileInUse):
			writeAdminError(w, http.StatusConflict, err.Error())
		default:
			writeAdminError(w, http.StatusBadRequest, err.Error())
		}
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleListenerResource(w http.ResponseWriter, req *http.Request, control *localControlPlane, name string) {
	switch req.Method {
	case http.MethodPut:
		var payload localListenerPayload
		if err := decodeRequestJSON(req, &payload); err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
		view, warnings, err := control.UpsertListener(name, payload)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, runtimecfg.ErrProfileNotFound) {
				status = http.StatusNotFound
			}
			writeAdminError(w, status, err.Error())
			return
		}
		if len(warnings) > 0 {
			w.Header().Set("X-Zolem-Warnings", strings.Join(warnings, "; "))
		}
		writeJSON(w, http.StatusOK, view)
	case http.MethodGet:
		view, err := control.GetListener(name)
		if err != nil {
			if errors.Is(err, runtimecfg.ErrListenerNotFound) {
				writeAdminError(w, http.StatusNotFound, err.Error())
				return
			}
			writeAdminError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, view)
	case http.MethodDelete:
		err := control.DeleteListener(name)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, runtimecfg.ErrListenerNotFound):
			writeAdminError(w, http.StatusNotFound, err.Error())
		default:
			writeAdminError(w, http.StatusBadRequest, err.Error())
		}
	default:
		w.Header().Set("Allow", "PUT, GET, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func localResourceName(prefix, p string) (string, bool) {
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(p, prefix)
	if name == "" || name != path.Base(name) {
		return "", false
	}
	return name, true
}

func decodeRequestJSON(req *http.Request, v any) error {
	defer req.Body.Close()
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain exactly one JSON object")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAdminError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

type httpLocalServer struct {
	listener net.Listener
	server   *http.Server
}

func startLocalServer(spec runtimecfg.ListenerSpec, tlsConfig localTLSConfig, handler http.Handler) (localServer, error) {
	if spec.TLS {
		if !tlsConfig.enabled() {
			return nil, fmt.Errorf("TLS listener %q requires local TLS cert and key", spec.Name)
		}
		return startLocalTLSServer(spec.Addr, handler, tlsConfig)
	}
	return startLocalHTTPServer(spec.Addr, handler)
}

func startLocalHTTPServer(addr string, handler http.Handler) (localServer, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()

	return &httpLocalServer{
		listener: listener,
		server:   server,
	}, nil
}

func startLocalTLSServer(addr string, handler http.Handler, tlsConfig localTLSConfig) (localServer, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	server := &http.Server{Handler: handler}
	go func() {
		_ = server.ServeTLS(listener, tlsConfig.CertFile, tlsConfig.KeyFile)
	}()

	return &httpLocalServer{
		listener: listener,
		server:   server,
	}, nil
}

func (s *httpLocalServer) Addr() string {
	return s.listener.Addr().String()
}

func (s *httpLocalServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.server.Shutdown(ctx); err != nil {
		// Graceful shutdown timed out with connections still in flight — e.g. an
		// SSE stream honoring a profile's stream_delay that outlives the 2s
		// deadline. Force the listener and active connections closed so teardown
		// cannot hang indefinitely. The force Close re-closes the already-closed
		// listener and would surface a spurious "use of closed network
		// connection"; teardown is best-effort, so report completion.
		_ = s.server.Close()
	}
	return nil
}

func localBaseURL(spec runtimecfg.ListenerSpec) string {
	scheme := "http"
	if spec.TLS {
		scheme = "https"
	}
	return scheme + "://" + spec.Addr
}
