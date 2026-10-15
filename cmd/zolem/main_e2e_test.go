package main_test

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/provider/anthropic"
	"github.com/ketang/zolem/internal/provider/gemini"
)

func TestMain_E2E(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		svc := startFixedService(t, "anthropic")

		resp, body := doRequest(t, svc.baseURL, "POST", "/v1/messages", `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`, "Content-Type: application/json", "x-api-key: sk-test")
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}
		var got anthropic.MessagesResponse
		mustJSONUnmarshal(t, body, &got)
		if got.Type != "message" || got.Role != "assistant" || len(got.Content) != 1 || got.Content[0].Text != "Fixture says hello from anthropic." {
			t.Fatalf("response: %#v", got)
		}
	})

	t.Run("openai-stream", func(t *testing.T) {
		svc := startFixedService(t, "openai")

		resp, body := doRequest(t, svc.baseURL, "POST", "/v1/chat/completions", `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`, "Content-Type: application/json", "Authorization: Bearer sk-test", "Accept: text/event-stream")
		defer resp.Body.Close()

		assertSSEHeaders(t, resp.Header)
		if resp.StatusCode != 200 {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}

		records := sseRecords(t, body)
		if strings.TrimSpace(records[len(records)-1]) != "data: [DONE]" {
			t.Fatalf("terminal marker missing, got %q", records[len(records)-1])
		}
		dataRecords := records[:len(records)-1]
		var combined strings.Builder
		for i, record := range dataRecords {
			var chunk openAIStreamChunk
			mustJSONUnmarshal(t, sseDataPayload(t, record), &chunk)
			switch i {
			case 0:
				if chunk.Choices[0].Delta.Role != "assistant" {
					t.Fatalf("first chunk delta: %#v", chunk.Choices[0].Delta)
				}
			case len(dataRecords) - 1:
				if chunk.Choices[0].FinishReason == nil || *chunk.Choices[0].FinishReason != "stop" {
					t.Fatalf("final completion chunk finish reason: %#v", chunk.Choices[0].FinishReason)
				}
			default:
				combined.WriteString(chunk.Choices[0].Delta.Content)
			}
			if chunk.Usage != nil {
				t.Fatalf("unexpected usage chunk without include_usage: %#v", chunk.Usage)
			}
		}
		if combined.String() != "Fixture says hello from openai." {
			t.Fatalf("combined stream content: got %q", combined.String())
		}
	})

	t.Run("gemini-stream", func(t *testing.T) {
		svc := startFixedService(t, "gemini")

		resp, body := doRequest(t, svc.baseURL, "POST", "/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, "Content-Type: application/json", "x-goog-api-key: test-key")
		defer resp.Body.Close()

		assertSSEHeaders(t, resp.Header)
		if resp.StatusCode != 200 {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}

		records := sseRecords(t, body)
		var combined strings.Builder
		for i, record := range records {
			payload := sseDataPayload(t, record)
			var chunk gemini.GenerateContentResponse
			mustJSONUnmarshal(t, payload, &chunk)
			if i < len(records)-1 {
				var raw struct {
					Candidates []map[string]json.RawMessage `json:"candidates"`
				}
				mustJSONUnmarshal(t, payload, &raw)
				if _, ok := raw.Candidates[0]["finishReason"]; ok {
					t.Fatalf("chunk %d included finishReason, want omitted: %s", i, payload)
				}
			}
			if i == len(records)-1 && chunk.Candidates[0].FinishReason != "STOP" {
				t.Fatalf("last chunk finish reason: got %q, want STOP", chunk.Candidates[0].FinishReason)
			}
			combined.WriteString(chunk.Candidates[0].Content.Parts[0].Text)
		}
		if combined.String() != "Fixture says hello from gemini." {
			t.Fatalf("combined stream content: got %q", combined.String())
		}

		ctResp, ctBody := doRequest(t, svc.baseURL, "POST", "/v1beta/models/gemini-2.0-flash:countTokens", `{"contents":[{"role":"user","parts":[{"text":"hello world from zolem"}]}]}`, "Content-Type: application/json", "x-goog-api-key: test-key")
		defer ctResp.Body.Close()
		if ctResp.StatusCode != 200 {
			t.Fatalf("countTokens status: got %d, want 200; body: %s", ctResp.StatusCode, ctBody)
		}
		var ct struct {
			TotalTokens int `json:"totalTokens"`
		}
		mustJSONUnmarshal(t, ctBody, &ct)
		if ct.TotalTokens <= 0 {
			t.Fatalf("countTokens totalTokens: got %d, want > 0", ct.TotalTokens)
		}
	})
}

// TestFixedModeRejectsInvalidRecordCapsAndWasm_E2E covers zolem-2m4: zero and
// negative -local-record-* caps and the unusable wasm backend are usage errors
// (exit 2) that name the problem and never reach the listening log line.
func TestFixedModeRejectsInvalidRecordCapsAndWasm_E2E(t *testing.T) {
	bin := buildZolemBinary(t)
	run := func(t *testing.T, extra ...string) (string, int) {
		t.Helper()
		args := append([]string{"-local-provider", "openai", "-local-addr", "127.0.0.1:0"}, extra...)
		out, err := exec.Command(bin, args...).CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("args %v: got err %v, want non-zero exit\n%s", extra, err, out)
		}
		return string(out), exitErr.ExitCode()
	}

	for _, flagName := range []string{
		"local-record-request-body-cap-bytes",
		"local-record-response-body-cap-bytes",
		"local-record-stream-event-cap",
	} {
		for _, val := range []string{"-1", "0"} {
			t.Run(flagName+"="+val, func(t *testing.T) {
				out, code := run(t, "-"+flagName, val)
				if code != 2 {
					t.Fatalf("exit code: got %d, want 2\n%s", code, out)
				}
				if !strings.Contains(out, "-"+flagName+" must be positive") {
					t.Fatalf("output does not name the flag:\n%s", out)
				}
				if strings.Contains(out, "zolem local listener") {
					t.Fatalf("listener started despite invalid cap:\n%s", out)
				}
			})
		}
	}

	t.Run("wasm-backend", func(t *testing.T) {
		out, code := run(t, "-local-backend", "wasm")
		if code != 2 {
			t.Fatalf("exit code: got %d, want 2\n%s", code, out)
		}
		if !strings.Contains(out, "not available in fixed-listener mode") {
			t.Fatalf("output missing wasm rejection:\n%s", out)
		}
	})
}
