// Package backend defines the shared generator interfaces that provider
// handlers (Anthropic, Gemini, OpenAI) depend on for their Ollama-backed text
// and chat generation, plus deterministic no-op implementations that let
// Shatter and tests construct handlers without a live Ollama backend.
package backend

import (
	"context"

	"github.com/ketang/zolem/internal/ollama"
)

// TextGenerator produces a single completion string for a prompt. The real
// implementation is the Ollama client; handlers fall back to other generation
// paths when it returns an empty string or error.
type TextGenerator interface {
	Generate(context.Context, string) (string, error)
}

// ChatGenerator produces chat completions against an upstream Ollama server,
// either as a single response or streamed token-by-token via the callback.
type ChatGenerator interface {
	NonStreaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string) (string, error)
	Streaming(ctx context.Context, upstream string, messages []ollama.ChatMessage, model string, fn func(delta string) error) error
}

// noopTextGenerator is a deterministic TextGenerator that always returns an
// empty string and no error, causing handlers to fall through to their default
// generation path.
type noopTextGenerator struct{}

// NewNoopTextGenerator returns a deterministic TextGenerator that produces an
// empty completion. It is safe for use in Shatter exploration and tests where
// no real backend is available.
func NewNoopTextGenerator() TextGenerator {
	return noopTextGenerator{}
}

func (noopTextGenerator) Generate(context.Context, string) (string, error) {
	return "", nil
}

// noopChatGenerator is a deterministic ChatGenerator that produces no output:
// NonStreaming returns an empty string and Streaming invokes its callback zero
// times.
type noopChatGenerator struct{}

// NewNoopChatGenerator returns a deterministic ChatGenerator that produces no
// chat output. It is safe for use in Shatter exploration and tests where no
// real backend is available.
func NewNoopChatGenerator() ChatGenerator {
	return noopChatGenerator{}
}

func (noopChatGenerator) NonStreaming(context.Context, string, []ollama.ChatMessage, string) (string, error) {
	return "", nil
}

func (noopChatGenerator) Streaming(context.Context, string, []ollama.ChatMessage, string, func(delta string) error) error {
	return nil
}
