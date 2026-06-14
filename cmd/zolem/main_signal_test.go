package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPWithContext_ShutsDownOnCancel(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listeners are not permitted in this environment: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTPWithContext(ctx, server, func() error {
			return server.Serve(listener)
		})
	}()

	resp, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("GET before shutdown: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status before shutdown = %d, want 200", resp.StatusCode)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serveHTTPWithContext returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down after context cancellation")
	}
}
