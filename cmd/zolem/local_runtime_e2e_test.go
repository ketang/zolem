package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalRuntimeErrorBackend_E2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	t.Run("openai-rate-limit", func(t *testing.T) {
		listenerBaseURL := createRuntimeListener(t, admin, "openai", map[string]any{
			"backend":    "error",
			"error_type": "rate_limit",
		})

		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, "Content-Type: application/json", "Authorization: Bearer sk-test")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429", resp.StatusCode)
		}
		var payload struct {
			Error struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		mustJSONUnmarshal(t, body, &payload)
		if payload.Error.Type != "rate_limit_error" {
			t.Fatalf("error type: got %q, want rate_limit_error", payload.Error.Type)
		}
		if payload.Error.Code != "rate_limit_exceeded" {
			t.Fatalf("error code: got %q, want rate_limit_exceeded", payload.Error.Code)
		}
	})

	t.Run("anthropic-rate-limit", func(t *testing.T) {
		listenerBaseURL := createRuntimeListener(t, admin, "anthropic", map[string]any{
			"backend":    "error",
			"error_type": "rate_limit",
		})

		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/messages", `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, "Content-Type: application/json", "x-api-key: sk-test")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429", resp.StatusCode)
		}
		var payload struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Error     struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		mustJSONUnmarshal(t, body, &payload)
		if payload.Type != "error" {
			t.Fatalf("type: got %q, want error", payload.Type)
		}
		if payload.Error.Type != "rate_limit_error" {
			t.Fatalf("error type: got %q, want rate_limit_error", payload.Error.Type)
		}
		if payload.RequestID == "" {
			t.Fatal("expected request_id")
		}
	})

	t.Run("gemini-rate-limit", func(t *testing.T) {
		listenerBaseURL := createRuntimeListener(t, admin, "gemini", map[string]any{
			"backend":    "error",
			"error_type": "rate_limit",
		})

		resp, body := doRequest(t, listenerBaseURL, http.MethodPost, "/v1/models/gemini-2.0-flash:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, "Content-Type: application/json", "x-goog-api-key: test-key")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429", resp.StatusCode)
		}
		var payload struct {
			Error struct {
				Status  string `json:"status"`
				Details []struct {
					Reason string `json:"reason"`
				} `json:"details"`
			} `json:"error"`
		}
		mustJSONUnmarshal(t, body, &payload)
		if payload.Error.Status != "RESOURCE_EXHAUSTED" {
			t.Fatalf("status text: got %q, want RESOURCE_EXHAUSTED", payload.Error.Status)
		}
		if len(payload.Error.Details) == 0 || payload.Error.Details[0].Reason != "RATE_LIMIT_EXCEEDED" {
			t.Fatalf("details: got %#v, want RATE_LIMIT_EXCEEDED", payload.Error.Details)
		}
	})
}

type localAdminService struct {
	baseURL string
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	errCh   chan error
	logs    *bytes.Buffer
}

func startLocalAdminService(t *testing.T, repoRoot string) *localAdminService {
	t.Helper()

	workDir := t.TempDir()
	port := pickPort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", port)

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-local-admin-addr", adminAddr)
	cmd.Dir = filepath.Join(repoRoot, "cmd", "zolem")
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(workDir, "gocache"))
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	if err := cmd.Start(); err != nil {
		t.Fatalf("start local admin: %v", err)
	}

	svc := &localAdminService{
		baseURL: "http://" + adminAddr,
		cmd:     cmd,
		cancel:  cancel,
		done:    make(chan struct{}),
		errCh:   make(chan error, 1),
		logs:    &logs,
	}

	go func() {
		svc.errCh <- cmd.Wait()
		close(svc.done)
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-svc.done:
			err := <-svc.errCh
			t.Fatalf("local admin exited before readiness: %v\nlogs:\n%s", err, logs.String())
		default:
		}

		resp, _, err := doRequestRaw(client, svc.baseURL, http.MethodGet, "/_zolem/health", "")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return svc
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for local admin readiness\nlogs:\n%s", logs.String())
	return nil
}

func (s *localAdminService) Close() {
	s.cancel()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

func createRuntimeListener(t *testing.T, admin *localAdminService, provider string, profile map[string]any) string {
	t.Helper()

	profileName := provider + "-error-demo"
	listenerName := provider + "-error-listener"
	profileBody, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}

	profileResp, body := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/profiles/"+profileName, string(profileBody), "Content-Type: application/json")
	defer profileResp.Body.Close()
	if profileResp.StatusCode != http.StatusOK {
		t.Fatalf("profile status: got %d, want 200: %s", profileResp.StatusCode, body)
	}
	t.Cleanup(func() {
		resp, _, err := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, admin.baseURL, http.MethodDelete, "/_zolem/profiles/"+profileName, "")
		if err == nil && resp != nil {
			resp.Body.Close()
		}
	})

	listenerPayload := fmt.Sprintf(`{"addr":"127.0.0.1:0","provider":"%s","profile":"%s"}`, provider, profileName)
	listenerResp, listenerBody := doRequest(t, admin.baseURL, http.MethodPut, "/_zolem/listeners/"+listenerName, listenerPayload, "Content-Type: application/json")
	defer listenerResp.Body.Close()
	if listenerResp.StatusCode != http.StatusOK {
		t.Fatalf("listener status: got %d, want 200: %s", listenerResp.StatusCode, listenerBody)
	}
	t.Cleanup(func() {
		resp, _, err := doRequestRaw(&http.Client{Timeout: 5 * time.Second}, admin.baseURL, http.MethodDelete, "/_zolem/listeners/"+listenerName, "")
		if err == nil && resp != nil {
			resp.Body.Close()
		}
	})

	var payload struct {
		BaseURL string `json:"base_url"`
	}
	mustJSONUnmarshal(t, listenerBody, &payload)
	if payload.BaseURL == "" {
		t.Fatalf("listener base_url missing: %s", strings.TrimSpace(string(listenerBody)))
	}

	stateResp, stateBody := doRequest(t, payload.BaseURL, http.MethodGet, "/_zolem/state", "")
	defer stateResp.Body.Close()
	if stateResp.StatusCode != http.StatusOK {
		t.Fatalf("state status: got %d, want 200", stateResp.StatusCode)
	}
	var state map[string]any
	mustJSONUnmarshal(t, stateBody, &state)
	if state["provider"] != provider {
		t.Fatalf("state provider: got %#v, want %s", state["provider"], provider)
	}
	if state["backend"] != "error" {
		t.Fatalf("state backend: got %#v, want error", state["backend"])
	}

	return payload.BaseURL
}
