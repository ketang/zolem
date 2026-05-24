package main_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var zolemBinaryOnce sync.Once
var zolemBinaryPath string
var zolemBinaryErr error

func buildZolemBinary(t *testing.T) string {
	t.Helper()
	zolemBinaryOnce.Do(func() {
		repoRoot := repoRoot(t)
		tmp := os.TempDir()
		zolemBinaryPath = filepath.Join(tmp, "zolem-e2e-test-bin")
		cmd := exec.Command("go", "build", "-o", zolemBinaryPath, ".")
		cmd.Dir = filepath.Join(repoRoot, "cmd", "zolem")
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			zolemBinaryErr = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if zolemBinaryErr != nil {
		t.Fatalf("build zolem binary: %v", zolemBinaryErr)
	}
	return zolemBinaryPath
}

func TestLocalCallsFileJSONL_E2E(t *testing.T) {
	bin := buildZolemBinary(t)

	t.Run("basic_jsonl_recording", func(t *testing.T) {
		callsFile := filepath.Join(t.TempDir(), "calls.jsonl")
		svc := startZolemWithCallsFile(t, bin, callsFile, 0)

		for i := 0; i < 2; i++ {
			resp, _ := doRequest(t, svc.baseURL, "POST", "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			resp.Body.Close()
		}

		records := readJSONLFile(t, callsFile)
		if len(records) != 2 {
			t.Fatalf("expected 2 JSONL records, got %d", len(records))
		}
		for i, rec := range records {
			if rec.CallID <= 0 {
				t.Fatalf("record %d: call_id=%d, want > 0", i, rec.CallID)
			}
			if rec.Request.Method != "POST" {
				t.Fatalf("record %d: method=%q, want POST", i, rec.Request.Method)
			}
			if rec.Request.Path != "/v1/chat/completions" {
				t.Fatalf("record %d: path=%q", i, rec.Request.Path)
			}
			if rec.Response.Status != 200 {
				t.Fatalf("record %d: status=%d, want 200", i, rec.Response.Status)
			}
		}
		if records[0].CallID >= records[1].CallID {
			t.Fatalf("call_ids should be monotonically increasing: %d, %d", records[0].CallID, records[1].CallID)
		}
	})

	t.Run("body_cap_in_jsonl", func(t *testing.T) {
		callsFile := filepath.Join(t.TempDir(), "calls.jsonl")
		svc := startZolemWithCallsFile(t, bin, callsFile, 10)

		longBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"this is a much longer body than 10 bytes"}]}`
		resp, _ := doRequest(t, svc.baseURL, "POST", "/v1/chat/completions", longBody,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		resp.Body.Close()

		records := readJSONLFile(t, callsFile)
		if len(records) != 1 {
			t.Fatalf("expected 1 record, got %d", len(records))
		}
		if records[0].Request.BodyTruncatedBytes <= 0 {
			t.Fatalf("body_truncated_bytes: got %d, want > 0", records[0].Request.BodyTruncatedBytes)
		}
	})

	t.Run("file_append_semantics", func(t *testing.T) {
		callsFile := filepath.Join(t.TempDir(), "calls.jsonl")
		svc := startZolemWithCallsFile(t, bin, callsFile, 0)

		for i := 0; i < 2; i++ {
			resp, _ := doRequest(t, svc.baseURL, "POST", "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			resp.Body.Close()
		}
		records := readJSONLFile(t, callsFile)
		if len(records) != 2 {
			t.Fatalf("after first batch: expected 2 records, got %d", len(records))
		}

		for i := 0; i < 2; i++ {
			resp, _ := doRequest(t, svc.baseURL, "POST", "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"hello again"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			resp.Body.Close()
		}
		records = readJSONLFile(t, callsFile)
		if len(records) != 4 {
			t.Fatalf("after second batch: expected 4 records, got %d", len(records))
		}
	})

	t.Run("no_file_without_flag", func(t *testing.T) {
		callsFile := filepath.Join(t.TempDir(), "should-not-exist.jsonl")

		port := pickPort(t)
		var logs bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, bin,
			"-local-addr", fmt.Sprintf("127.0.0.1:%d", port),
			"-local-provider", "openai",
			"-local-profile", "demo",
			"-local-backend", "lorem",
		)
		cmd.Stdout = &logs
		cmd.Stderr = &logs
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
			resp, _, err := doRequestRaw(c, baseURL, "POST", "/v1/chat/completions",
				`{"model":"gpt-4o","messages":[{"role":"user","content":"probe"}]}`,
				"Content-Type: application/json", "Authorization: Bearer sk-test")
			if err != nil {
				return errProbeNotReady
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				return errProbeNotReady
			}
			return nil
		}); err != nil {
			t.Fatalf("zolem did not become ready: %v\nlogs:\n%s", err, logs.String())
		}

		if _, err := os.Stat(callsFile); err == nil {
			t.Fatalf("calls file %q should not exist without -local-calls-file flag", callsFile)
		}
	})
}

func startZolemWithCallsFile(t *testing.T, bin, callsFile string, requestBodyCap int) *serviceProcess {
	t.Helper()

	port := pickPort(t)
	args := []string{
		"-local-addr", fmt.Sprintf("127.0.0.1:%d", port),
		"-local-provider", "openai",
		"-local-profile", "demo",
		"-local-backend", "lorem",
		"-local-calls-file", callsFile,
	}
	if requestBodyCap > 0 {
		args = append(args, "-local-record-request-body-cap-bytes", fmt.Sprintf("%d", requestBodyCap))
	}

	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &logs
	cmd.Stderr = &logs

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
		resp, _, err := doRequestRaw(c, baseURL, "POST", "/v1/chat/completions",
			`{"model":"gpt-4o","messages":[{"role":"user","content":"probe"}]}`,
			"Content-Type: application/json", "Authorization: Bearer sk-test")
		if err != nil {
			return errProbeNotReady
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return errProbeNotReady
		}
		return nil
	}); err != nil {
		t.Fatalf("zolem did not become ready: %v\nlogs:\n%s", err, logs.String())
	}

	// Truncate the calls file to discard readiness-probe records.
	if err := os.Truncate(callsFile, 0); err != nil && !os.IsNotExist(err) {
		t.Fatalf("truncate calls file: %v", err)
	}

	return svc
}

func readJSONLFile(t *testing.T, path string) []recordedCall {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open JSONL file: %v", err)
	}
	defer f.Close()

	var records []recordedCall
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec recordedCall
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("unmarshal JSONL line: %v\nline: %s", err, line)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan JSONL file: %v", err)
	}
	return records
}
