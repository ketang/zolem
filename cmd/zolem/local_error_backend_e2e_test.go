package main_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestLocalErrorBackend_E2E verifies fixed-listener behavior for the error
// backend: a valid error_type returns the provider-native error envelope, and
// an error backend without an error_type is rejected at startup with a clear
// message instead of silently serving success responses.
func TestLocalErrorBackend_E2E(t *testing.T) {
	bin := buildZolemBinary(t)

	t.Run("returns_provider_native_error", func(t *testing.T) {
		port := pickPort(t)
		var logs bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, bin,
			"-local-addr", fmt.Sprintf("127.0.0.1:%d", port),
			"-local-provider", "anthropic",
			"-local-profile", "demo",
			"-local-backend", "error",
			"-local-error-type", "rate_limit",
		)
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
			resp, _, err := doRequestRaw(c, baseURL, "POST", "/v1/messages",
				`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "x-api-key: sk-test")
			if err != nil {
				return errProbeNotReady
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusServiceUnavailable {
				return errProbeNotReady
			}
			return nil
		}); err != nil {
			t.Fatalf("zolem did not become ready: %v\nlogs:\n%s", err, logs.String())
		}

		resp, body := doRequest(t, svc.baseURL, "POST", "/v1/messages",
			`{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`,
			"Content-Type: application/json", "x-api-key: sk-test")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429\nbody: %s", resp.StatusCode, body)
		}
		var envelope struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		mustJSONUnmarshal(t, body, &envelope)
		if envelope.Type != "error" {
			t.Fatalf("envelope type: got %q, want error\nbody: %s", envelope.Type, body)
		}
		if envelope.Error.Type != "rate_limit_error" {
			t.Fatalf("error type: got %q, want rate_limit_error\nbody: %s", envelope.Error.Type, body)
		}
	})

	t.Run("rejects_missing_error_type_at_startup", func(t *testing.T) {
		port := pickPort(t)
		var logs bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin,
			"-local-addr", fmt.Sprintf("127.0.0.1:%d", port),
			"-local-provider", "anthropic",
			"-local-profile", "demo",
			"-local-backend", "error",
		)
		cmd.Stdout = &logs
		cmd.Stderr = &logs

		err := cmd.Run()
		if err == nil {
			t.Fatalf("expected non-zero exit for error backend without error type\nlogs:\n%s", logs.String())
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("expected ExitError, got %T: %v\nlogs:\n%s", err, err, logs.String())
		}
		if !strings.Contains(logs.String(), "error_type is required when backend is error") {
			t.Fatalf("expected clear error_type message in output, got:\n%s", logs.String())
		}
	})
}
