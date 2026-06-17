package main

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

// countingLocalServer wraps a real localServer and tracks how many remain open
// so a test can assert that a concurrent replace leaves exactly one live server
// (no orphaned http.Server + net.Listener leaking goroutines and ports).
type countingLocalServer struct {
	inner localServer
	open  *int64
	once  sync.Once
}

func (s *countingLocalServer) Addr() string { return s.inner.Addr() }

func (s *countingLocalServer) Close() error {
	s.once.Do(func() { atomic.AddInt64(s.open, -1) })
	return s.inner.Close()
}

// TestUpsertListener_ConcurrentReplaceNoLeak fires many concurrent PUTs to the
// same listener name and asserts that, once they settle, exactly one server is
// still open and exactly one listener is registered. Under the previous
// lock-gap implementation two racing PUTs could both bind and the loser's
// server was orphaned, leaving open > 1.
func TestUpsertListener_ConcurrentReplaceNoLeak(t *testing.T) {
	control := newTestLocalControlPlane(t, localAdminOptions{})
	if _, err := control.UpsertProfile("demo", localProfilePayload{Backend: "lorem"}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}

	var open int64
	control.startServer = func(_ runtimecfg.ListenerSpec, _ localTLSConfig, handler http.Handler) (localServer, error) {
		inner, err := startLocalHTTPServer("127.0.0.1:0", handler)
		if err != nil {
			return nil, err
		}
		atomic.AddInt64(&open, 1)
		return &countingLocalServer{inner: inner, open: &open}, nil
	}

	upsert := func() error {
		_, _, err := control.UpsertListener("openai-demo", localListenerPayload{
			Addr:     "127.0.0.1:0",
			Provider: "openai",
			Profile:  "demo",
		})
		return err
	}

	// Warm up single-threaded: the first listener build lazily initializes
	// process-global state in the embedded WASM runtime (wazero memoizes its
	// version without synchronization), which the race detector would otherwise
	// flag for the concurrent builds below. This also makes the storm a genuine
	// replace test — there is already a registered listener to swap out.
	if err := upsert(); err != nil {
		t.Fatalf("warmup upsert: %v", err)
	}

	const goroutines = 24
	var wg sync.WaitGroup
	var failures int64
	for range goroutines {
		wg.Go(func() {
			if err := upsert(); err != nil {
				atomic.AddInt64(&failures, 1)
			}
		})
	}
	wg.Wait()

	if failures != 0 {
		t.Fatalf("UpsertListener failures: got %d, want 0", failures)
	}

	control.mu.Lock()
	registered := len(control.listeners)
	control.mu.Unlock()
	if registered != 1 {
		t.Fatalf("registered listeners: got %d, want 1", registered)
	}

	if got := atomic.LoadInt64(&open); got != 1 {
		t.Fatalf("open servers after concurrent replace: got %d, want 1 (leaked %d)", got, got-1)
	}
}

// TestHTTPLocalServerClose_ForceClosesInFlightStream verifies that Close force
// closes connections that outlive the graceful-shutdown deadline (e.g. an SSE
// stream honoring stream_delay) instead of hanging or returning before the
// stream is actually torn down.
func TestHTTPLocalServerClose_ForceClosesInFlightStream(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseStream := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseStream()

	handlerDone := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		// Simulate a stream_delay that far outlives the 2s shutdown deadline.
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})

	server, err := startLocalHTTPServer("127.0.0.1:0", handler)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	go func() {
		resp, err := http.Get("http://" + server.Addr() + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started serving the in-flight stream")
	}

	done := make(chan error, 1)
	go func() { done <- server.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		releaseStream()
		t.Fatal("Close did not return; force-close fallback missing")
	}

	// The in-flight handler must have been torn down by the force-close, not
	// left running past shutdown.
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight handler outlived Close; force-close did not cut the connection")
	}
}
