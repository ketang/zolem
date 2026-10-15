package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type errorProbe struct {
	provider string
	path     string
	body     string
	headers  []string
	// status per error type
	rateLimitStatus  int
	overloadedStatus int
	rateLimitMarker  string // substring expected in the rate_limit body
	overloadedMarker string // substring expected in the overloaded body
}

func errorProbes() []errorProbe {
	return []errorProbe{
		{
			provider: "anthropic", path: "/v1/messages",
			body:            `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`,
			headers:         []string{"Content-Type: application/json", "x-api-key: sk-test"},
			rateLimitStatus: 429, overloadedStatus: 529,
			rateLimitMarker: "rate_limit_error", overloadedMarker: "overloaded_error",
		},
		{
			provider: "openai", path: "/v1/chat/completions",
			body:            `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
			headers:         []string{"Content-Type: application/json", "Authorization: Bearer sk-test"},
			rateLimitStatus: 429, overloadedStatus: 503,
			rateLimitMarker: "rate_limit_exceeded", overloadedMarker: "currently overloaded",
		},
		{
			provider: "gemini", path: "/v1beta/models/gemini-1.5-flash:generateContent",
			body:            `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			headers:         []string{"Content-Type: application/json", "x-goog-api-key: sk-test"},
			rateLimitStatus: 429, overloadedStatus: 503,
			rateLimitMarker: "RESOURCE_EXHAUSTED", overloadedMarker: "UNAVAILABLE",
		},
		{
			provider: "ollama", path: "/api/chat",
			body:            `{"model":"llama3","messages":[{"role":"user","content":"hi"}],"stream":false}`,
			headers:         []string{"Content-Type: application/json"},
			rateLimitStatus: 429, overloadedStatus: 503,
			rateLimitMarker: "rate limit exceeded", overloadedMarker: "server overloaded",
		},
	}
}

// TestLocalErrorBackend_RetryAfterAndOverloaded_E2E covers zolem-07i: rate_limit
// and overloaded responses always carry retry-after / retry-after-ms, driven by
// the profile's retry_after_seconds, and overloaded maps to each provider's
// native overload status.
func TestLocalErrorBackend_RetryAfterAndOverloaded_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	for _, p := range errorProbes() {
		p := p
		for _, errType := range []string{"rate_limit", "overloaded"} {
			errType := errType
			for _, retry := range []*int{nil, intPtr(2)} {
				retry := retry
				name := p.provider + "/" + errType + "/default"
				if retry != nil {
					name = p.provider + "/" + errType + "/explicit"
				}
				t.Run(name, func(t *testing.T) {
					admin := startLocalAdminService(t, repoRoot)
					t.Cleanup(admin.Close)
					profile := map[string]any{"backend": "error", "error_type": errType}
					wantSecs, wantMS := "", ""
					if retry != nil {
						profile["retry_after_seconds"] = *retry
						wantSecs, wantMS = "2", "2000"
					}
					base := createRuntimeListener(t, admin, p.provider, profile)
					resp, body := doRequest(t, base, "POST", p.path, p.body, p.headers...)
					defer resp.Body.Close()

					wantStatus, marker := p.rateLimitStatus, p.rateLimitMarker
					if errType == "overloaded" {
						wantStatus, marker = p.overloadedStatus, p.overloadedMarker
					}
					if resp.StatusCode != wantStatus {
						t.Fatalf("status: got %d, want %d\nbody: %s", resp.StatusCode, wantStatus, body)
					}
					if !strings.Contains(string(body), marker) {
						t.Fatalf("body missing %q: %s", marker, body)
					}
					if got := resp.Header.Get("Retry-After"); got != wantSecs {
						t.Fatalf("Retry-After: got %q, want %q", got, wantSecs)
					}
					if got := resp.Header.Get("Retry-After-Ms"); got != wantMS {
						t.Fatalf("Retry-After-Ms: got %q, want %q", got, wantMS)
					}
				})
			}
		}
	}
}

func TestLocalErrorBackend_NoRetryAfterOnOtherErrors_E2E(t *testing.T) {
	admin := startLocalAdminService(t, repoRoot(t))
	t.Cleanup(admin.Close)
	p := errorProbes()[0]
	base := createRuntimeListener(t, admin, p.provider, map[string]any{"backend": "error", "error_type": "server_error"})
	resp, _ := doRequest(t, base, "POST", p.path, p.body, p.headers...)
	defer resp.Body.Close()
	if got := resp.Header.Get("Retry-After"); got != "" {
		t.Fatalf("server_error should not set Retry-After, got %q", got)
	}
}

func TestOpenAIResponsesWebSocket_ForcedErrorHandshake_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	for _, tc := range []struct {
		errType string
		status  int
	}{{"rate_limit", 429}, {"overloaded", 503}} {
		tc := tc
		t.Run(tc.errType, func(t *testing.T) {
			admin := startLocalAdminService(t, repoRoot)
			t.Cleanup(admin.Close)
			base := createRuntimeListener(t, admin, "openai", map[string]any{
				"backend": "error", "error_type": tc.errType, "retry_after_seconds": 2,
			})
			_, resp, err := websocket.DefaultDialer.Dial(responsesWSURL(t, base), http.Header{
				"Authorization": []string{"Bearer sk-test"},
			})
			if err == nil {
				t.Fatal("expected handshake failure")
			}
			if resp == nil || resp.StatusCode != tc.status {
				t.Fatalf("handshake status: got %#v, want %d", resp, tc.status)
			}
			if got := resp.Header.Get("Retry-After"); got != "2" {
				t.Fatalf("Retry-After: got %q, want 2", got)
			}
			if got := resp.Header.Get("Retry-After-Ms"); got != "2000" {
				t.Fatalf("Retry-After-Ms: got %q, want 2000", got)
			}
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read handshake body: %v", err)
			}
			var env struct {
				Error struct {
					Type string `json:"type"`
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("handshake body not JSON: %q (%v)", raw, err)
			}
			wantType, wantCode := "rate_limit_error", "rate_limit_exceeded"
			if tc.errType == "overloaded" {
				wantType, wantCode = "server_error", ""
			}
			if env.Error.Type != wantType || env.Error.Code != wantCode {
				t.Fatalf("handshake error: got type=%q code=%q, want type=%q code=%q (%s)", env.Error.Type, env.Error.Code, wantType, wantCode, raw)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

// TestLocalErrorBackend_RetryAfterFlag_E2E covers the fixed-listener
// -local-retry-after-seconds flag and its validation at startup.
func TestLocalErrorBackend_RetryAfterFlag_E2E(t *testing.T) {
	bin := buildZolemBinary(t)

	t.Run("sets_headers", func(t *testing.T) {
		svc := startZolemWithArgs(t, bin, "-local-provider", "anthropic", "-local-profile", "demo",
			"-local-backend", "error", "-local-error-type", "overloaded", "-local-retry-after-seconds", "3")
		resp, _ := doRequest(t, svc.baseURL, "POST", "/v1/messages", `{}`,
			"Content-Type: application/json", "x-api-key: sk-test")
		defer resp.Body.Close()
		if resp.StatusCode != 529 {
			t.Fatalf("status: got %d, want 529", resp.StatusCode)
		}
		if got := resp.Header.Get("Retry-After"); got != "3" {
			t.Fatalf("Retry-After: got %q, want 3", got)
		}
		if got := resp.Header.Get("Retry-After-Ms"); got != "3000" {
			t.Fatalf("Retry-After-Ms: got %q, want 3000", got)
		}
	})

	t.Run("rejects_above_cap", func(t *testing.T) {
		out, err := exec.Command(bin, "-local-addr", fmt.Sprintf("127.0.0.1:%d", pickPort(t)),
			"-local-provider", "anthropic", "-local-backend", "error", "-local-error-type", "rate_limit",
			"-local-retry-after-seconds", "999999").CombinedOutput()
		if err == nil || !strings.Contains(string(out), "retry_after_seconds must be between 0 and 3600") {
			t.Fatalf("expected cap rejection, err=%v out=%s", err, out)
		}
	})
}

func startZolemWithArgs(t *testing.T, bin string, args ...string) *serviceProcess {
	t.Helper()
	port := pickPort(t)
	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, append([]string{"-local-addr", fmt.Sprintf("127.0.0.1:%d", port)}, args...)...)
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	configureProcReaping(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start zolem: %v", err)
	}
	svc := &serviceProcess{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		cmd:     cmd,
		cancel:  cancel,
		done:    make(chan struct{}),
		errCh:   make(chan error, 1),
		logs:    &logs,
	}
	t.Cleanup(svc.Close)
	go func() {
		svc.errCh <- cmd.Wait()
		close(svc.done)
	}()
	client := &http.Client{Timeout: 2 * time.Second}
	if err := waitForReady(client, svc, func(c *http.Client, baseURL string) error {
		resp, _, err := doRequestRaw(c, baseURL, "POST", "/v1/messages", `{}`,
			"Content-Type: application/json", "x-api-key: sk-test")
		if err != nil {
			return errProbeNotReady
		}
		resp.Body.Close()
		return nil
	}); err != nil {
		t.Fatalf("zolem did not become ready: %v\nlogs:\n%s", err, logs.String())
	}
	return svc
}
