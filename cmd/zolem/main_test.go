package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestZolemVersion(t *testing.T) {
	// An unstamped test binary reports a non-empty fallback version rather than
	// the empty string.
	if got := zolemVersion(); got == "" {
		t.Fatal("zolemVersion returned empty string")
	}

	orig := version
	t.Cleanup(func() { version = orig })
	version = "1.2.3"
	if got := zolemVersion(); got != "1.2.3" {
		t.Fatalf("zolemVersion with stamped version = %q, want 1.2.3", got)
	}
}

func TestUsageNamesBothModes(t *testing.T) {
	var buf bytes.Buffer
	usage(&buf)
	out := buf.String()
	for _, want := range []string{"-local-provider", "-local-admin-addr", "fixed-listener", "admin control-plane", "-version"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage output missing %q:\n%s", want, out)
		}
	}
}
