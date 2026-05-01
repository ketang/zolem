package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestZolemcLocalRuntimeE2E(t *testing.T) {
	repoRoot := repoRoot(t)
	admin := startLocalAdminService(t, repoRoot)
	t.Cleanup(admin.Close)

	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "health")
	missing := runZolemcFail(t, repoRoot, "-admin-url", admin.baseURL, "profiles", "delete", "missing")
	if !strings.Contains(missing.stderr, "404") || !strings.Contains(missing.stderr, `"error":"profile not found"`) {
		t.Fatalf("missing profile error did not include status and body:\n%s", missing.stderr)
	}

	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "profiles", "create", "demo", "-backend", "lorem")

	profiles := runZolemc(t, repoRoot, "-json", "-admin-url", admin.baseURL, "profiles", "list")
	if !strings.Contains(profiles.stdout, `"name":"demo"`) {
		t.Fatalf("profiles list did not include demo:\n%s", profiles.stdout)
	}

	listener := runZolemc(t, repoRoot, "-json", "-admin-url", admin.baseURL, "listeners", "create", "openai-demo", "-addr", "127.0.0.1:0", "-provider", "openai", "-profile", "demo")
	var listenerPayload struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal([]byte(listener.stdout), &listenerPayload); err != nil {
		t.Fatalf("decode listener JSON: %v\n%s", err, listener.stdout)
	}
	if listenerPayload.BaseURL == "" {
		t.Fatalf("listener base_url missing:\n%s", listener.stdout)
	}

	runZolemc(t, repoRoot, "-base-url", listenerPayload.BaseURL, "listener", "health")
	state := runZolemc(t, repoRoot, "-json", "-base-url", listenerPayload.BaseURL, "listener", "state")
	if !strings.Contains(state.stdout, `"provider":"openai"`) {
		t.Fatalf("listener state did not include provider:\n%s", state.stdout)
	}

	request := runZolemc(t, repoRoot, "-base-url", listenerPayload.BaseURL, "request", "-method", "POST", "-path", "/v1/chat/completions", "-H", "Authorization: Bearer sk-test", "-json-body", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	var completion struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(request.stdout), &completion); err != nil {
		t.Fatalf("decode completion response: %v\n%s", err, request.stdout)
	}
	if len(completion.Choices) == 0 || completion.Choices[0].Message.Role != "assistant" || completion.Choices[0].Message.Content == "" {
		t.Fatalf("request output did not include assistant content:\n%#v", completion)
	}

	bodyFile := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(bodyFile, []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"from file"}]}`), 0o644); err != nil {
		t.Fatalf("write request body file: %v", err)
	}
	runZolemc(t, repoRoot, "-base-url", listenerPayload.BaseURL, "request", "-method", "POST", "-path", "/v1/chat/completions", "-H", "Authorization: Bearer sk-test", "-H", "Content-Type: application/json", "-body-file", bodyFile)

	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "listeners", "delete", "openai-demo")
	runZolemc(t, repoRoot, "-admin-url", admin.baseURL, "profiles", "delete", "demo")
}

type commandResult struct {
	stdout string
	stderr string
}

func runZolemc(t *testing.T, repoRoot string, args ...string) commandResult {
	t.Helper()

	result, err := runZolemcRaw(t, repoRoot, args...)
	if err != nil {
		t.Fatalf("zolemc %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, result.stdout, result.stderr)
	}
	return result
}

func runZolemcFail(t *testing.T, repoRoot string, args ...string) commandResult {
	t.Helper()

	result, err := runZolemcRaw(t, repoRoot, args...)
	if err == nil {
		t.Fatalf("zolemc %s unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), result.stdout, result.stderr)
	}
	return result
}

func runZolemcRaw(t *testing.T, repoRoot string, args ...string) (commandResult, error) {
	t.Helper()

	cmd := exec.Command("go", append([]string{"run", "./cmd/zolemc"}, args...)...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "gocache"))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return commandResult{stdout: stdout.String(), stderr: stderr.String()}, err
	}
	return commandResult{stdout: stdout.String(), stderr: stderr.String()}, nil
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

	port := pickPort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", port)

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/zolem", "-local-admin-addr", adminAddr)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "gocache"))
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

		resp, err := client.Get(svc.baseURL + "/_zolem/health")
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

func pickPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listeners are not permitted in this environment: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
