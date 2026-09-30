package main_test

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Wildcard binds are covered by unit tests and the CI docker job; these tests
// keep every real bind on 127.0.0.1.

func TestNonLoopbackBindFlagValidation_E2E(t *testing.T) {
	bin := buildZolemBinary(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no opt-in", []string{"-local-admin-addr", "0.0.0.0:18090"}, "listener addr must bind to localhost or a loopback IP"},
		{"no allowed host", []string{"-local-admin-addr", "0.0.0.0:18090", "-allow-non-loopback-bind", "-listener-port-range", "18100-18101"}, "-allow-non-loopback-bind requires at least one -allowed-host"},
		{"no range", []string{"-local-admin-addr", "0.0.0.0:18090", "-allow-non-loopback-bind", "-allowed-host", "zolem.test"}, "-listener-port-range"},
		{"range in fixed mode", []string{"-local-provider", "openai", "-allow-non-loopback-bind", "-allowed-host", "zolem.test", "-listener-port-range", "18100-18101"}, "-listener-port-range"},
		{"range without opt-in", []string{"-local-admin-addr", "127.0.0.1:18090", "-listener-port-range", "18100-18101"}, "requires -allow-non-loopback-bind"},
		{"specific non-loopback ip", []string{"-local-admin-addr", "192.168.1.5:18090", "-allow-non-loopback-bind", "-allowed-host", "zolem.test", "-listener-port-range", "18100-18101"}, "0.0.0.0"},
		{"fixed mode no opt-in", []string{"-local-provider", "openai", "-local-addr", "0.0.0.0:18090"}, "listener addr must bind to localhost or a loopback IP"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command(bin, tc.args...).CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("got err %v, want exit 1\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("output missing %q:\n%s", tc.want, out)
			}
		})
	}
}

func TestNonLoopbackBindControlPlane_E2E(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	adminPort := pickPort(t)
	listenerPort := pickPort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", adminPort)
	startZolemArgs(t, adminAddr, "-local-admin-addr", adminAddr,
		"-allow-non-loopback-bind", "-allowed-host", "zolem.test",
		"-listener-port-range", fmt.Sprintf("%d-%d", listenerPort, listenerPort+1))
	adminURL := "http://" + adminAddr

	put := func(path, body string) (int, string) {
		resp, b := doRequest(t, adminURL, http.MethodPut, path, body, "Content-Type: application/json")
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	if status, body := put("/_zolem/profiles/p1", `{"backend":"lorem"}`); status != http.StatusOK {
		t.Fatalf("create profile: %d %s", status, body)
	}

	outside := fmt.Sprintf(`{"addr":"127.0.0.1:%d","provider":"openai","profile":"p1"}`, listenerPort+100)
	status, body := put("/_zolem/listeners/out", outside)
	want := fmt.Sprintf("listener port %d outside -listener-port-range %d-%d", listenerPort+100, listenerPort, listenerPort+1)
	if status != http.StatusBadRequest || !strings.Contains(body, want) {
		t.Fatalf("outside range: got %d %s, want 400 containing %q", status, body, want)
	}

	inside := fmt.Sprintf(`{"addr":"127.0.0.1:%d","provider":"openai","profile":"p1"}`, listenerPort)
	if status, body := put("/_zolem/listeners/in", inside); status != http.StatusOK {
		t.Fatalf("inside range: got %d %s, want 200", status, body)
	}
	if status, body := put("/_zolem/listeners/wild", fmt.Sprintf(`{"addr":"10.0.0.5:%d","provider":"openai","profile":"p1"}`, listenerPort+1)); status != http.StatusBadRequest {
		t.Fatalf("specific non-loopback IP: got %d %s, want 400", status, body)
	}

	// Host policy is additive on the admin API and on the listener.
	for _, host := range []string{fmt.Sprintf("zolem.test:%d", adminPort), fmt.Sprintf("localhost:%d", adminPort)} {
		if status, body := doWithHost(t, client, http.MethodGet, adminURL+"/_zolem/health", host, ""); status != http.StatusOK {
			t.Errorf("admin Host %q: got %d %s, want 200", host, status, body)
		}
	}
	if status, _ := doWithHost(t, client, http.MethodGet, adminURL+"/_zolem/health", "evil.example", ""); status != http.StatusForbidden {
		t.Errorf("admin evil Host: got %d, want 403", status)
	}
	assertHostPolicy(t, client, fmt.Sprintf("http://127.0.0.1:%d", listenerPort),
		[]string{fmt.Sprintf("zolem.test:%d", listenerPort), fmt.Sprintf("localhost:%d", listenerPort)},
		[]string{"evil.example"})
}

func TestNonLoopbackBindFixedListenerHostPolicy_E2E(t *testing.T) {
	client := &http.Client{Timeout: 5 * time.Second}
	port := pickPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	startZolemArgs(t, addr, "-local-addr", addr, "-local-provider", "openai",
		"-allow-non-loopback-bind", "-allowed-host", "zolem.test")
	assertHostPolicy(t, client, "http://"+addr,
		[]string{fmt.Sprintf("zolem.test:%d", port), fmt.Sprintf("localhost:%d", port)},
		[]string{"evil.example"})
}
