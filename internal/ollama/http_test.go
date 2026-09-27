package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPChatCompletion_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != false {
			t.Errorf("expected stream=false, got %v", req["stream"])
		}
		if req["model"] != "gemma3:4b" {
			t.Errorf("expected model=gemma3:4b, got %v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "Hello from ollama"}},
			},
		})
	}))
	defer srv.Close()
	text, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello from ollama" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestHTTPChatCompletion_ConnectionRefused(t *testing.T) {
	_, err := HTTPChatCompletion(context.Background(), "http://127.0.0.1:1", []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ollama backend unavailable") {
		t.Fatalf("expected 'ollama backend unavailable', got: %v", err)
	}
}

func TestHTTPChatCompletion_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer srv.Close()
	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
	if !strings.Contains(err.Error(), "ollama backend error") {
		t.Fatalf("expected 'ollama backend error', got: %v", err)
	}
}

func TestHTTPChatCompletion_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if !strings.Contains(err.Error(), "unparseable") {
		t.Fatalf("expected 'unparseable', got: %v", err)
	}
}

func TestHTTPChatCompletion_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := HTTPChatCompletion(ctx, srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestHTTPChatCompletionStream_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if req["stream"] != true {
			t.Errorf("expected stream=true, got %v", req["stream"])
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{"Hello ", "from ", "ollama"}
		for _, chunk := range chunks {
			data, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{
					{"delta": map[string]string{"content": chunk}},
				},
			})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()
	var deltas []string
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deltas) != 3 {
		t.Fatalf("expected 3 deltas, got %d: %v", len(deltas), deltas)
	}
	joined := strings.Join(deltas, "")
	if joined != "Hello from ollama" {
		t.Fatalf("unexpected text: %q", joined)
	}
}

func TestHTTPChatCompletionStream_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer srv.Close()
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		t.Fatal("callback should not be called")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
	if !strings.Contains(err.Error(), "ollama backend error") {
		t.Fatalf("expected 'ollama backend error', got: %v", err)
	}
}

func TestHTTPChatCompletionStream_ConnectionRefused(t *testing.T) {
	err := HTTPChatCompletionStream(context.Background(), "http://127.0.0.1:1", []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error { return nil })
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "ollama backend unavailable") {
		t.Fatalf("expected 'ollama backend unavailable', got: %v", err)
	}
}

func TestHTTPChatCompletionStream_CallbackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]string{"content": "hello"}},
			},
		})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}))
	defer srv.Close()
	callbackErr := errors.New("writer closed")
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error { return callbackErr })
	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error, got: %v", err)
	}
}

// --- Upstream client hardening tests ---------------------------------------

func TestHTTPChatCompletion_DoesNotFollowRedirects(t *testing.T) {
	var hit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"from-target"}}]}`))
	}))
	defer target.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer up.Close()

	_, err := HTTPChatCompletion(context.Background(), up.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for refused redirect")
	}
	if !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("expected 'redirect refused', got: %v", err)
	}
	if hit.Load() {
		t.Fatal("redirect target should never have been hit")
	}
}

func TestHTTPChatCompletionStream_DoesNotFollowRedirects(t *testing.T) {
	var hit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"from-target"}}]}`))
	}))
	defer target.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer up.Close()

	err := HTTPChatCompletionStream(context.Background(), up.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		t.Fatal("callback should not be called")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for refused redirect")
	}
	if !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("expected 'redirect refused', got: %v", err)
	}
	if hit.Load() {
		t.Fatal("redirect target should never have been hit")
	}
}

func TestUpstreamClient_PoliciesDoNotSharePools(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("cannot bind 127.0.0.2 in this environment: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	overrideIPClass = func(addr netip.Addr) (ipClass, bool) {
		if addr.String() == "127.0.0.2" {
			return classPublic, true
		}
		return 0, false
	}
	defer func() { overrideIPClass = nil }()

	// External policy allows the fake-public address.
	extCtx := WithAllowExternalUpstream(context.Background(), true)
	if _, err := HTTPChatCompletion(extCtx, srv.URL, []ChatMessage{{Role: "user", Content: "hi"}}, "m"); err != nil {
		t.Fatalf("expected external-policy request to succeed, got: %v", err)
	}

	// Default policy must refuse the same address, proving the pooled
	// external-policy connection was not reused to skip the dial check.
	defaultCtx := context.Background()
	_, err = HTTPChatCompletion(defaultCtx, srv.URL, []ChatMessage{{Role: "user", Content: "hi"}}, "m")
	if err == nil {
		t.Fatal("expected default-policy request to be refused")
	}
	if !strings.Contains(err.Error(), "dial refused") {
		t.Fatalf("expected dial-policy error, got: %v", err)
	}

	if defaultPolicyClient.Transport == externalPolicyClient.Transport {
		t.Fatal("expected distinct transport pointers per policy")
	}
}

func TestHTTPChatCompletion_StalledBodyTimesOut(t *testing.T) {
	origTimeout := overallTimeout
	overallTimeout = 200 * time.Millisecond
	defer func() { overallTimeout = origTimeout }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	start := time.Now()
	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > time.Second {
		t.Fatalf("expected call to return within 1s, took %v", elapsed)
	}
}

func TestHTTPChatCompletionStream_IdleTimeout(t *testing.T) {
	origTimeout := streamIdleTimeout
	streamIdleTimeout = 200 * time.Millisecond
	defer func() { streamIdleTimeout = origTimeout }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]string{"content": "hello"}},
			},
		})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	var deltas []string
	start := time.Now()
	err := HTTPChatCompletionStream(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected idle timeout error")
	}
	if elapsed > time.Second {
		t.Fatalf("expected call to return within 1s, took %v", elapsed)
	}
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta delivered before stall, got %d: %v", len(deltas), deltas)
	}
}

func TestUpstreamClient_NoProxyNoRedirectConfig(t *testing.T) {
	clients := []struct {
		name   string
		client *http.Client
		dialer *net.Dialer
	}{
		{"default", defaultPolicyClient, defaultPolicyDialer},
		{"external", externalPolicyClient, externalPolicyDialer},
	}
	for _, c := range clients {
		t.Run(c.name, func(t *testing.T) {
			tr, ok := c.client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("expected *http.Transport, got %T", c.client.Transport)
			}
			if tr.Proxy != nil {
				t.Error("expected Transport.Proxy to be nil")
			}
			if c.client.CheckRedirect == nil {
				t.Error("expected non-nil CheckRedirect")
			}
			if tr.TLSHandshakeTimeout != tlsHandshakeTimeout {
				t.Errorf("expected TLSHandshakeTimeout %v, got %v", tlsHandshakeTimeout, tr.TLSHandshakeTimeout)
			}
			if tr.ResponseHeaderTimeout != responseHeaderTimeout {
				t.Errorf("expected ResponseHeaderTimeout %v, got %v", responseHeaderTimeout, tr.ResponseHeaderTimeout)
			}
			if c.dialer.Timeout != dialTimeout {
				t.Errorf("expected Dialer.Timeout %v, got %v", dialTimeout, c.dialer.Timeout)
			}
		})
	}
}

func TestDialControl_Policy(t *testing.T) {
	tests := []struct {
		name          string
		addr          string
		allowExternal bool
		wantErr       bool
	}{
		{"external-allowed-linklocal-metadata", "169.254.169.254:80", true, true},
		{"external-allowed-linklocal-v6", "[fe80::1]:80", true, true},
		{"external-allowed-unspecified", "0.0.0.0:80", true, true},
		{"external-allowed-multicast", "224.0.0.1:80", true, true},
		{"external-allowed-public", "8.8.8.8:80", true, false},
		{"external-allowed-private", "10.0.0.1:80", true, false},
		{"external-disallowed-public", "8.8.8.8:80", false, true},
		{"external-disallowed-loopback", "127.0.0.1:80", false, false},
		{"external-disallowed-loopback-v6", "[::1]:80", false, false},
		{"external-disallowed-private", "192.168.1.50:80", false, false},
		{"mapped-linklocal-blocked-always", "[::ffff:169.254.169.254]:80", true, true},
		{"mapped-public-blocked-without-external", "[::ffff:8.8.8.8]:80", false, true},
		{"mapped-loopback-allowed", "[::ffff:127.0.0.1]:80", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, _, err := net.SplitHostPort(tt.addr)
			if err != nil {
				t.Fatalf("split host/port: %v", err)
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				t.Fatalf("parse addr: %v", err)
			}
			err = dialAllowed(addr, tt.allowExternal)
			if tt.wantErr && err == nil {
				t.Fatalf("expected dial to be refused for %s (allowExternal=%v)", tt.addr, tt.allowExternal)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected dial to be allowed for %s (allowExternal=%v), got: %v", tt.addr, tt.allowExternal, err)
			}
		})
	}
}

func TestHTTPChatCompletion_CapsResponseSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"message":{"content":"`))
		w.Write(bytes.Repeat([]byte("a"), maxResponseBodyBytes+1))
	}))
	defer srv.Close()

	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected size-limit error, got: %v", err)
	}
}

func TestHTTPChatCompletion_ErrorBodyTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(bytes.Repeat([]byte("x"), 1024*1024))
	}))
	defer srv.Close()

	_, err := HTTPChatCompletion(context.Background(), srv.URL, []ChatMessage{
		{Role: "user", Content: "hi"},
	}, "gemma3:4b")
	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
	if len(err.Error()) > maxErrorBodyBytes+200 {
		t.Fatalf("expected truncated error message (<= %d bytes), got %d bytes", maxErrorBodyBytes+200, len(err.Error()))
	}
}
