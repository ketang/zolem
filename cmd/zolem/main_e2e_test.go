package main_test

import (
	"strings"
	"testing"

	"zolem.dev/zolem/internal/provider/anthropic"
	"zolem.dev/zolem/internal/provider/gemini"
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
			case len(dataRecords) - 2:
				if chunk.Choices[0].FinishReason == nil || *chunk.Choices[0].FinishReason != "stop" {
					t.Fatalf("final completion chunk finish reason: %#v", chunk.Choices[0].FinishReason)
				}
			case len(dataRecords) - 1:
				if chunk.Usage == nil || chunk.Usage.TotalTokens != 12 {
					t.Fatalf("usage chunk: %#v", chunk.Usage)
				}
			default:
				combined.WriteString(chunk.Choices[0].Delta.Content)
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
			var chunk gemini.GenerateContentResponse
			mustJSONUnmarshal(t, sseDataPayload(t, record), &chunk)
			if i < len(records)-1 && chunk.Candidates[0].FinishReason != "NONE" {
				t.Fatalf("chunk %d finish reason: got %q, want NONE", i, chunk.Candidates[0].FinishReason)
			}
			if i == len(records)-1 && chunk.Candidates[0].FinishReason != "STOP" {
				t.Fatalf("last chunk finish reason: got %q, want STOP", chunk.Candidates[0].FinishReason)
			}
			combined.WriteString(chunk.Candidates[0].Content.Parts[0].Text)
		}
		if combined.String() != "Fixture says hello from gemini." {
			t.Fatalf("combined stream content: got %q", combined.String())
		}
	})
}
