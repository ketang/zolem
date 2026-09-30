package main

import (
	"strings"
	"testing"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

func TestLocalBaseURL_WildcardAdvertisesLocalhost(t *testing.T) {
	tests := []struct {
		addr string
		tls  bool
		want string
	}{
		{"0.0.0.0:18100", false, "http://localhost:18100"},
		{"[::]:18100", false, "http://localhost:18100"},
		{"127.0.0.1:18100", false, "http://127.0.0.1:18100"},
		{"0.0.0.0:18100", true, "https://localhost:18100"},
	}
	for _, tc := range tests {
		got := localBaseURL(runtimecfg.ListenerSpec{Addr: tc.addr, TLS: tc.tls})
		if got != tc.want {
			t.Errorf("localBaseURL(%q, tls=%v) = %q, want %q", tc.addr, tc.tls, got, tc.want)
		}
	}
}

func TestResolveBindPolicy(t *testing.T) {
	hosts := []string{"zolem.test"}
	tests := []struct {
		name    string
		allow   bool
		hosts   []string
		rng     string
		cp      bool
		wantErr string
	}{
		{"default", false, nil, "", true, ""},
		{"range without opt-in", false, nil, "18100-18101", true, "requires -allow-non-loopback-bind"},
		{"no hosts", true, nil, "18100-18101", true, "requires at least one -allowed-host"},
		{"no range control-plane", true, hosts, "", true, "-listener-port-range"},
		{"range in fixed mode", true, hosts, "18100-18101", false, "control-plane mode"},
		{"bad range", true, hosts, "9-1", true, "invalid port range"},
		{"ok control-plane", true, hosts, "18100-18101", true, ""},
		{"ok fixed", true, hosts, "", false, ""},
	}
	for _, tc := range tests {
		_, err := resolveBindPolicy(tc.allow, tc.hosts, tc.rng, tc.cp)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: got %v, want containing %q", tc.name, err, tc.wantErr)
		}
	}
}
