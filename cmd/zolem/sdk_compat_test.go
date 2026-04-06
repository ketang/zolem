package main_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	openai "github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

func TestSDKCompatibility_Anthropic(t *testing.T) {
	repoRoot := repoRoot(t)
	svc := startService(t, repoRoot)
	t.Cleanup(svc.Close)

	client := anthropicapi.NewClient(
		anthropicoption.WithAPIKey("sk-test"),
		anthropicoption.WithBaseURL(svc.baseURL),
		anthropicoption.WithHTTPClient(newHostRewriteClient("acme.api.anthropic.zolem.dev")),
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
		if message.Type != "message" {
			t.Fatalf("type: got %q, want message", message.Type)
		}
		if message.Role != "assistant" {
			t.Fatalf("role: got %q, want assistant", message.Role)
		}
		if len(message.Content) != 1 {
			t.Fatalf("content blocks: got %d, want 1", len(message.Content))
		}
		text, ok := message.Content[0].AsAny().(anthropicapi.TextBlock)
		if !ok {
			t.Fatalf("unexpected content type: %#v", message.Content[0].AsAny())
		}
		if text.Text != "Fixture says hello from anthropic." {
			t.Fatalf("text: got %q", text.Text)
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
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected anthropic api error, got %T", err)
		}
		if apiErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400", apiErr.StatusCode)
		}
		if !strings.Contains(apiErr.Error(), "max_tokens is required") {
			t.Fatalf("error text: %v", err)
		}
	})
}

func TestSDKCompatibility_OpenAI(t *testing.T) {
	repoRoot := repoRoot(t)
	svc := startService(t, repoRoot)
	t.Cleanup(svc.Close)

	client := openai.NewClient(
		openaioption.WithAPIKey("sk-test"),
		openaioption.WithBaseURL(svc.baseURL+"/v1"),
		openaioption.WithHTTPClient(newHostRewriteClient("acme.api.openai.zolem.dev")),
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
		if len(completion.Choices) != 1 {
			t.Fatalf("choices: got %d, want 1", len(completion.Choices))
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
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected openai api error, got %T", err)
		}
		if apiErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400", apiErr.StatusCode)
		}
		if !strings.Contains(apiErr.Message, "model") {
			t.Fatalf("message: got %q", apiErr.Message)
		}
	})
}

func newHostRewriteClient(host string) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: hostRewriteRoundTripper{
			host: host,
			base: http.DefaultTransport,
		},
	}
}

type hostRewriteRoundTripper struct {
	host string
	base http.RoundTripper
}

func (rt hostRewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Host = rt.host
	return rt.base.RoundTrip(clone)
}
