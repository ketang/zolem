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
