package main_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const hostGuardChatBody = `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`

// startZolemArgs launches the zolem binary with args and waits until addr
// accepts TCP connections. It does not send HTTP so readiness never depends on
// the Host policy under test.
func startZolemArgs(t *testing.T, addr string, args ...string) {
	t.Helper()

	bin := buildZolemBinary(t)
	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = os.Environ()
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	configureProcReaping(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start zolem: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("zolem exited before readiness\nlogs:\n%s", logs.String())
		default:
		}
		if conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s\nlogs:\n%s", addr, logs.String())
}

// doWithHost sends a request whose Host header is overridden to host.
func doWithHost(t *testing.T, client *http.Client, method, url, host, body string, headers ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	for _, h := range headers {
		parts := strings.SplitN(h, ": ", 2)
		req.Header.Set(parts[0], parts[1])
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request with Host %q: %v", host, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

// assertHostPolicy checks chat requests against baseURL with each Host value.
func assertHostPolicy(t *testing.T, client *http.Client, baseURL string, port int, allowed, denied []string) {
	t.Helper()
	for _, host := range allowed {
		status, body := doWithHost(t, client, http.MethodPost, baseURL+"/v1/chat/completions", host, hostGuardChatBody,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		if status != http.StatusOK {
			t.Errorf("Host %q: got %d, want 200: %s", host, status, body)
		}
	}
	for _, host := range denied {
		status, body := doWithHost(t, client, http.MethodPost, baseURL+"/v1/chat/completions", host, hostGuardChatBody,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		if status != http.StatusForbidden {
			t.Errorf("Host %q: got %d, want 403: %s", host, status, body)
		}
		want := fmt.Sprintf("host %q not allowed", host)
		if !strings.Contains(body, want) {
			t.Errorf("Host %q body %q does not contain %q", host, body, want)
		}
	}
}

func TestFixedListenerHostGuard_E2E(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("default_policy", func(t *testing.T) {
		port := pickPort(t)
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		startZolemArgs(t, addr, "-local-addr", addr, "-local-provider", "openai")
		assertHostPolicy(t, client, "http://"+addr, port,
			[]string{fmt.Sprintf("127.0.0.1:%d", port), fmt.Sprintf("localhost:%d", port)},
			[]string{"evil.example", fmt.Sprintf("evil.example:%d", port)})
	})

	t.Run("allowed_host_is_additive", func(t *testing.T) {
		port := pickPort(t)
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		startZolemArgs(t, addr, "-local-addr", addr, "-local-provider", "openai", "-allowed-host", "zolem.test")
		assertHostPolicy(t, client, "http://"+addr, port,
			[]string{fmt.Sprintf("zolem.test:%d", port), fmt.Sprintf("localhost:%d", port)},
			[]string{"evil.example"})
	})

	t.Run("tls", func(t *testing.T) {
		certs := generateTestTLSCerts(t)
		port := pickPort(t)
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		startZolemArgs(t, addr, "-local-addr", addr, "-local-provider", "openai",
			"-local-tls-cert", certs.certPath, "-local-tls-key", certs.keyPath, "-allowed-host", "zolem.test")
		tlsClient := httpsClientWithRoots(certs.caPool)
		assertHostPolicy(t, tlsClient, "https://"+addr, port,
			[]string{fmt.Sprintf("127.0.0.1:%d", port), fmt.Sprintf("localhost:%d", port), fmt.Sprintf("zolem.test:%d", port)},
			[]string{"evil.example"})
	})

	t.Run("responses_websocket_upgrade", func(t *testing.T) {
		port := pickPort(t)
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		startZolemArgs(t, addr, "-local-addr", addr, "-local-provider", "openai")
		wsURL := "ws://" + addr + "/v1/responses"

		dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
		_, resp, err := dialer.Dial(wsURL, http.Header{
			"Authorization": []string{"Bearer sk-test"},
			"Host":          []string{"evil.example"},
		})
		if err == nil {
			t.Fatal("upgrade with foreign Host succeeded")
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("foreign Host upgrade: got %#v, want 403", resp)
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		resp.Body.Close()
		if !strings.Contains(buf.String(), `host "evil.example" not allowed`) {
			t.Fatalf("foreign Host upgrade body %q missing Host guard message", buf.String())
		}

		conn, _, err := dialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer sk-test"}})
		if err != nil {
			t.Fatalf("upgrade with loopback Host: %v", err)
		}
		conn.Close()
	})
}

func TestLocalAdminHostGuard_E2E(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}

	for _, tc := range []struct {
		name    string
		extra   []string
		allowed []string
	}{
		{"default_policy", nil, nil},
		{"allowed_host_is_additive", []string{"-allowed-host", "zolem.test"}, []string{"zolem.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := pickPort(t)
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			startZolemArgs(t, addr, append([]string{"-local-admin-addr", addr}, tc.extra...)...)
			adminURL := "http://" + addr

			// Admin API.
			allowed := append([]string{fmt.Sprintf("localhost:%d", port)}, tc.allowed...)
			for _, host := range allowed {
				if status, body := doWithHost(t, client, http.MethodGet, adminURL+"/_zolem/health", host, ""); status != http.StatusOK {
					t.Errorf("admin Host %q: got %d, want 200: %s", host, status, body)
				}
			}
			status, body := doWithHost(t, client, http.MethodGet, adminURL+"/_zolem/health", "evil.example", "")
			if status != http.StatusForbidden || !strings.Contains(body, `host "evil.example" not allowed`) {
				t.Errorf("admin foreign Host: got %d %s, want 403 host guard", status, body)
			}

			// Created data listener.
			doRequestStatus := func(method, path, body string) string {
				resp, b := doRequest(t, adminURL, method, path, body, "Content-Type: application/json")
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s %s: got %d: %s", method, path, resp.StatusCode, b)
				}
				return string(b)
			}
			doRequestStatus(http.MethodPut, "/_zolem/profiles/p1", `{"backend":"lorem"}`)
			view := doRequestStatus(http.MethodPut, "/_zolem/listeners/l1", `{"addr":"127.0.0.1:0","provider":"openai","profile":"p1"}`)
			var payload struct {
				BaseURL string `json:"base_url"`
			}
			mustJSONUnmarshal(t, []byte(view), &payload)
			listenerPort := strings.TrimPrefix(payload.BaseURL, "http://127.0.0.1:")
			assertHostPolicy(t, client, payload.BaseURL, 0,
				append([]string{"127.0.0.1:" + listenerPort, "localhost:" + listenerPort}, tc.allowed...),
				[]string{"evil.example"})
		})
	}
}

