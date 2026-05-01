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

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

type localAdminOptions struct {
	Addr        string
	FixturesDir string
	TLS         localTLSConfig
}

type localProfilePayload struct {
	Backend             string `json:"backend"`
	BackendModel        string `json:"backend_model"`
	ErrorType           string `json:"error_type"`
	ResponseModelPolicy string `json:"response_model_policy"`
	ResponseModel       string `json:"response_model"`
	FixtureNamespace    string `json:"fixture_namespace"`
	Seed                *int64 `json:"seed"`
}

type localListenerPayload struct {
	Addr     string `json:"addr"`
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	TLS      bool   `json:"tls,omitempty"`
}

type localListenerView struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	Backend  string `json:"backend"`
	TLS      bool   `json:"tls,omitempty"`
	BaseURL  string `json:"base_url"`
}

type localServer interface {
	Addr() string
	Close() error
}

type managedLocalListener struct {
	runtime runtimecfg.ListenerRuntime
	app     *startupApp
	server  localServer
	baseURL string
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
		delete(c.listeners, name)
		_ = c.store.DeleteListener(name)
	}
	return errors.Join(errs...)
}

func (c *localControlPlane) UpsertProfile(name string, payload localProfilePayload) (runtimecfg.RuntimeProfile, error) {
	profile := runtimecfg.RuntimeProfile{
		Name:                name,
		Backend:             payload.Backend,
		BackendModel:        payload.BackendModel,
		ErrorType:           payload.ErrorType,
		ResponseModelPolicy: payload.ResponseModelPolicy,
		ResponseModel:       payload.ResponseModel,
		FixtureNamespace:    payload.FixtureNamespace,
		Seed:                payload.Seed,
	}
	if profile.Backend == "" {
		profile.Backend = "lorem"
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
	if _, ok := c.store.GetProfile(payload.Profile); !ok {
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

	profile, _ := c.store.GetProfile(payload.Profile)
	runtime := runtimecfg.ListenerRuntime{
		Spec:    spec,
		Profile: profile,
	}

	app, warnings, err := buildLocalStartupAppForRuntime(runtime, c.fixturesDir, c.counters, c.deps)
	if err != nil {
		return localListenerView{}, warnings, err
	}

	c.mu.Lock()
	if existing, ok := c.listeners[name]; ok {
		_ = existing.server.Close()
		existing.app.close()
		delete(c.listeners, name)
		_ = c.store.DeleteListener(name)
	}
	c.mu.Unlock()

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

	view := localListenerView{
		Name:     actualSpec.Name,
		Addr:     actualSpec.Addr,
		Provider: actualSpec.Provider,
		Profile:  actualSpec.Profile,
		Backend:  profile.Backend,
		TLS:      actualSpec.TLS,
		BaseURL:  localBaseURL(actualSpec),
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, err := c.store.UpsertListener(actualSpec); err != nil {
		_ = server.Close()
		app.close()
		return localListenerView{}, warnings, err
	}
	c.listeners[name] = &managedLocalListener{
		runtime: actualRuntime,
		app:     app,
		server:  server,
		baseURL: view.BaseURL,
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
		out = append(out, localListenerView{
			Name:     spec.Name,
			Addr:     spec.Addr,
			Provider: spec.Provider,
			Profile:  spec.Profile,
			Backend:  entry.runtime.Profile.Backend,
			TLS:      spec.TLS,
			BaseURL:  entry.baseURL,
		})
	}
	return out
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
	delete(c.listeners, name)
	return c.store.DeleteListener(name)
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
			handleListenerResource(w, req, control)
			return
		default:
			http.NotFound(w, req)
		}
	})
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

func handleListenerResource(w http.ResponseWriter, req *http.Request, control *localControlPlane) {
	name, ok := localResourceName("/_zolem/listeners/", req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}

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
		w.Header().Set("Allow", "PUT, DELETE")
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
	return s.server.Shutdown(ctx)
}

func localBaseURL(spec runtimecfg.ListenerSpec) string {
	scheme := "http"
	if spec.TLS {
		scheme = "https"
	}
	return scheme + "://" + spec.Addr
}
