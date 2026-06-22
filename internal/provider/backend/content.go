package backend

import (
	"context"
	"strings"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/ollama"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
	"github.com/ketang/zolem/internal/wasmgen"
)

// Tokenize splits text into word-level tokens suitable for streaming.
// Each token except the last carries a trailing space so joining them
// reconstructs the original whitespace.
func Tokenize(text string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	tokens := make([]string, len(words))
	for i, w := range words {
		if i < len(words)-1 {
			tokens[i] = w + " "
		} else {
			tokens[i] = w
		}
	}
	return tokens
}

// GenerateRequest is a provider-agnostic content generation request.
type GenerateRequest struct {
	// Messages is the chat history, already converted to the Ollama wire format.
	Messages []ollama.ChatMessage
	// Model is the model the client requested (used as fallback when the profile
	// does not specify a BackendModel).
	Model string
	// FixtureMatch is populated by the provider handler for WASM generation.
	// It carries the provider name, version, and raw request body so the WASM
	// module can inspect them. May be nil for non-WASM backends.
	FixtureMatch *fixture.MatchRequest
}

// ContentBackend generates text tokens for a provider-agnostic request.
// Fixture matching is NOT part of this interface — providers resolve fixtures
// before selecting a backend.
type ContentBackend interface {
	// Tokens generates all tokens at once and returns them.
	Tokens(ctx context.Context, req GenerateRequest) ([]string, error)
	// Stream calls fn for each token as it arrives.
	// fn may return a non-nil error to abort; Stream propagates that error.
	Stream(ctx context.Context, req GenerateRequest, fn func(delta string) error) error
}

// Resolve returns the ContentBackend for the current request context.
// It reads the backend type from the listener runtime attached to ctx.
func Resolve(ctx context.Context, gen response.Generator, ollamaHTTP ChatGenerator, wasmGen *wasmgen.Generator) ContentBackend {
	switch runtimecfg.BackendForRequest(ctx) {
	case runtimecfg.BackendOllama:
		return &ollamaContentBackend{http: ollamaHTTP}
	case runtimecfg.BackendWASM:
		return &wasmContentBackend{gen: wasmGen}
	default:
		// lorem, faker, hybrid, error, fixture-fallback, and anything else all
		// use the generator (lorem/faker produce plausible tokens; the others
		// fall through here only when fixtures matched nothing).
		return &loremContentBackend{gen: gen}
	}
}

// BackendError is returned by ContentBackend when the upstream backend fails.
// Providers use it to write a provider-native 502 response.
type BackendError struct {
	Msg string
}

func (e *BackendError) Error() string { return e.Msg }

// loremContentBackend generates tokens using the lorem/faker generator.

type loremContentBackend struct{ gen response.Generator }

func (b *loremContentBackend) Tokens(_ context.Context, _ GenerateRequest) ([]string, error) {
	return b.gen.Generate(30), nil
}

func (b *loremContentBackend) Stream(ctx context.Context, _ GenerateRequest, fn func(string) error) error {
	delay := runtimecfg.StreamDelayForRequest(ctx)
	for _, tok := range b.gen.Generate(30) {
		if err := fn(tok); err != nil {
			return err
		}
		if delay != nil {
			if err := delay(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// ollamaContentBackend proxies generation through the Ollama HTTP API.
// Upstream URL and BackendModel are read from the listener runtime in ctx.

type ollamaContentBackend struct{ http ChatGenerator }

func (b *ollamaContentBackend) upstream(ctx context.Context) string {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(ctx)
	if u := rt.Profile.OllamaUpstream; u != "" {
		return u
	}
	return "http://localhost:11434"
}

func (b *ollamaContentBackend) model(ctx context.Context, fallback string) string {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(ctx)
	if m := rt.Profile.BackendModel; m != "" {
		return m
	}
	return fallback
}

func (b *ollamaContentBackend) Tokens(ctx context.Context, req GenerateRequest) ([]string, error) {
	text, err := b.http.NonStreaming(ctx, b.upstream(ctx), req.Messages, b.model(ctx, req.Model))
	if err != nil {
		return nil, &BackendError{Msg: "ollama backend error: " + err.Error()}
	}
	return Tokenize(text), nil
}

func (b *ollamaContentBackend) Stream(ctx context.Context, req GenerateRequest, fn func(string) error) error {
	err := b.http.Streaming(ctx, b.upstream(ctx), req.Messages, b.model(ctx, req.Model), fn)
	if err != nil {
		return &BackendError{Msg: "ollama backend error: " + err.Error()}
	}
	return nil
}

// wasmContentBackend generates tokens by executing a compiled WASM module.

type wasmContentBackend struct{ gen *wasmgen.Generator }

func (b *wasmContentBackend) Tokens(ctx context.Context, req GenerateRequest) ([]string, error) {
	if b.gen == nil {
		return nil, &BackendError{Msg: "the requested backend is not available"}
	}
	if req.FixtureMatch == nil {
		return nil, &BackendError{Msg: "the requested backend is not available"}
	}
	return b.gen.Generate(ctx, *req.FixtureMatch)
}

func (b *wasmContentBackend) Stream(ctx context.Context, req GenerateRequest, fn func(string) error) error {
	tokens, err := b.Tokens(ctx, req)
	if err != nil {
		return err
	}
	delay := runtimecfg.StreamDelayForRequest(ctx)
	for _, tok := range tokens {
		if err := fn(tok); err != nil {
			return err
		}
		if delay != nil {
			if err := delay(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
