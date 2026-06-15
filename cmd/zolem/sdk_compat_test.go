package main_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

func TestSDKCompatibility_Anthropic(t *testing.T) {
	svc := startFixedService(t, "anthropic")

	client := anthropicapi.NewClient(
		anthropicoption.WithAPIKey("sk-test"),
		anthropicoption.WithBaseURL(svc.baseURL),
	)

	t.Run("non-streaming", func(t *testing.T) {
		message, err := client.Messages.New(context.Background(), anthropicapi.MessageNewParams{
			Model:     anthropicapi.Model("claude-3-5-sonnet-20241022"),
			MaxTokens: 32,
			Messages: []anthropicapi.MessageParam{
				anthropicapi.NewUserMessage(anthropicapi.NewTextBlock("hello")),
			},
		})
		if err != nil {
			t.Fatalf("messages.new: %v", err)
		}
		text, ok := message.Content[0].AsAny().(anthropicapi.TextBlock)
		if !ok || text.Text != "Fixture says hello from anthropic." {
			t.Fatalf("content: %#v", message.Content)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		stream := client.Messages.NewStreaming(context.Background(), anthropicapi.MessageNewParams{
			Model:     anthropicapi.Model("claude-3-5-sonnet-20241022"),
			MaxTokens: 32,
			Messages: []anthropicapi.MessageParam{
				anthropicapi.NewUserMessage(anthropicapi.NewTextBlock("hello")),
			},
		})

		var combined strings.Builder
		for stream.Next() {
			event := stream.Current()
			switch variant := event.AsAny().(type) {
			case anthropicapi.ContentBlockDeltaEvent:
				switch delta := variant.Delta.AsAny().(type) {
				case anthropicapi.TextDelta:
					combined.WriteString(delta.Text)
				}
			}
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("stream err: %v", err)
		}
		if got := combined.String(); got != "Fixture says hello from anthropic." {
			t.Fatalf("combined stream: got %q", got)
		}
	})

	t.Run("error", func(t *testing.T) {
		_, err := client.Messages.New(context.Background(), anthropicapi.MessageNewParams{
			Model:     anthropicapi.Model("claude-3-5-sonnet-20241022"),
			MaxTokens: 0,
			Messages: []anthropicapi.MessageParam{
				anthropicapi.NewUserMessage(anthropicapi.NewTextBlock("hello")),
			},
		})
		if err == nil {
			t.Fatal("expected error for invalid anthropic request")
		}
		var apiErr *anthropicapi.Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// TestE2E_Anthropic_ContentBlocks exercises request-side schema acceptance for
// the agentic content-block types (tool_use, tool_result, image, document,
// thinking) against a real zolem process. Before the vendored anthropic-v1
// schema was extended, these standard agentic round-trips returned HTTP 400.
func TestE2E_Anthropic_ContentBlocks(t *testing.T) {
	svc := startFixedService(t, "anthropic")
	headers := []string{"x-api-key: sk-test", "Content-Type: application/json"}

	cases := []struct {
		name string
		body string
	}{
		{
			name: "tool_use_tool_result_round_trip",
			body: `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[` +
				`{"role":"user","content":[{"type":"text","text":"What is the weather in SF?"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"location":"SF"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"sunny, 72F"}]}` +
				`]}`,
		},
		{
			name: "image_block",
			body: `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":[` +
				`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}},` +
				`{"type":"text","text":"describe this image"}` +
				`]}]}`,
		},
		{
			name: "thinking_block",
			body: `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"assistant","content":[` +
				`{"type":"thinking","thinking":"considering the request","signature":"sig"}` +
				`]}]}`,
		},
		{
			name: "document_block",
			body: `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":[` +
				`{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0="}},` +
				`{"type":"text","text":"summarize this document"}` +
				`]}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doRequest(t, svc.baseURL, http.MethodPost, "/v1/messages", tc.body, headers...)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: got %d, want 200; body: %s", resp.StatusCode, body)
			}
			var message anthropicapi.Message
			mustJSONUnmarshal(t, body, &message)
			if len(message.Content) == 0 {
				t.Fatalf("expected non-empty content; body: %s", body)
			}
			text, ok := message.Content[0].AsAny().(anthropicapi.TextBlock)
			if !ok || text.Text != "Fixture says hello from anthropic." {
				t.Fatalf("content: %#v", message.Content)
			}
		})
	}

	t.Run("malformed_tool_use_rejected", func(t *testing.T) {
		// tool_use block missing required name/input must still be rejected.
		body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01"}]}]}`
		resp, respBody := doRequest(t, svc.baseURL, http.MethodPost, "/v1/messages", body, headers...)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400; body: %s", resp.StatusCode, respBody)
		}
	})
}

func TestSDKCompatibility_OpenAI(t *testing.T) {
	svc := startFixedService(t, "openai")

	client := openai.NewClient(
		openaioption.WithAPIKey("sk-test"),
		openaioption.WithBaseURL(svc.baseURL+"/v1"),
	)

	t.Run("non-streaming", func(t *testing.T) {
		completion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
			Model: shared.ChatModel("gpt-4o"),
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("hello"),
			},
		})
		if err != nil {
			t.Fatalf("chat.completions.new: %v", err)
		}
		if got := completion.Choices[0].Message.Content; got != "Fixture says hello from openai." {
			t.Fatalf("content: got %q", got)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		stream := client.Chat.Completions.NewStreaming(context.Background(), openai.ChatCompletionNewParams{
			Model: shared.ChatModel("gpt-4o"),
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("hello"),
			},
		})

		var combined strings.Builder
		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) == 0 {
				continue
			}
			combined.WriteString(chunk.Choices[0].Delta.Content)
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("stream err: %v", err)
		}
		if got := combined.String(); got != "Fixture says hello from openai." {
			t.Fatalf("combined stream: got %q", got)
		}
	})

	t.Run("error", func(t *testing.T) {
		_, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
			Model: shared.ChatModel(""),
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("hello"),
			},
		})
		if err == nil {
			t.Fatal("expected error for invalid openai request")
		}
		var apiErr *openai.Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
