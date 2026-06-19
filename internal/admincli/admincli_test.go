package admincli

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestDialErrorHint(t *testing.T) {
	// Port 1 is reserved; dialing it should fail with a connection error.
	c := NewClient("http://127.0.0.1:1", nil)
	err := c.GetJSON(context.Background(), "/_zolem/health", nil)
	if err == nil {
		t.Skip("dial to port 1 unexpectedly succeeded; skipping hint test")
	}
	msg := err.Error()
	if !strings.Contains(msg, "hint:") {
		t.Fatalf("expected dial error to contain hint, got: %v", err)
	}
	if !strings.Contains(msg, "zolem -local-admin-addr") {
		t.Fatalf("expected hint to mention zolem -local-admin-addr, got: %v", err)
	}
}

func TestNewInertOptions(t *testing.T) {
	opts := NewInertOptions()
	if opts.AdminURL != InertURL || opts.BaseURL != InertURL {
		t.Fatalf("inert options URLs = %q/%q, want %q", opts.AdminURL, opts.BaseURL, InertURL)
	}
	if opts.Timeout != DefaultTimeout {
		t.Fatalf("inert options timeout = %v, want %v", opts.Timeout, DefaultTimeout)
	}
	if opts.JSON {
		t.Fatal("inert options JSON should default to false")
	}
}

func TestNewClientTrimsTrailingSlash(t *testing.T) {
	c := NewClient("http://127.0.0.1:8090/", nil)
	if c.BaseURL() != "http://127.0.0.1:8090" {
		t.Fatalf("base URL = %q, want trailing slash trimmed", c.BaseURL())
	}
	if c.http != http.DefaultClient {
		t.Fatal("nil httpClient should default to http.DefaultClient")
	}
}

func TestNewInertClientPerformsNoIO(t *testing.T) {
	c := NewInertClient()
	if c.BaseURL() != InertURL {
		t.Fatalf("inert client base URL = %q, want %q", c.BaseURL(), InertURL)
	}
	// A request through the inert client must fail fast without dialing.
	var out map[string]string
	err := c.GetJSON(context.Background(), "/_zolem/health", &out)
	if err == nil {
		t.Fatal("inert client request unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "inert client performs no I/O") {
		t.Fatalf("inert client error = %v, want inert-transport error", err)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	withBody := &APIError{Method: "GET", URL: "http://x/y", Status: "502 Bad Gateway", Body: "  bad  "}
	if got := withBody.Error(); got != "GET http://x/y: 502 Bad Gateway: bad" {
		t.Fatalf("APIError with body = %q", got)
	}
	noBody := &APIError{Method: "GET", URL: "http://x/y", Status: "404 Not Found"}
	if got := noBody.Error(); got != "GET http://x/y: 404 Not Found" {
		t.Fatalf("APIError without body = %q", got)
	}
}

func TestJoinBaseAndPath(t *testing.T) {
	got, err := JoinBaseAndPath("http://example.test/api/", "v1/models?limit=1")
	if err != nil {
		t.Fatalf("JoinBaseAndPath: %v", err)
	}
	if got != "http://example.test/api/v1/models?limit=1" {
		t.Fatalf("joined URL = %q", got)
	}
	bad := []struct{ base, path string }{
		{"", "/v1/models"},
		{"://bad", "/v1/models"},
		{"http://example.test", "https://evil.test/v1/models"},
	}
	for _, c := range bad {
		if _, err := JoinBaseAndPath(c.base, c.path); err == nil {
			t.Fatalf("JoinBaseAndPath(%q, %q) unexpectedly succeeded", c.base, c.path)
		}
	}
}
