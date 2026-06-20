package backend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/ketang/zolem/internal/wasmgen"
)

// wasmBackendUnavailableMsg is the clean, user-facing message the WASM backend
// must surface instead of internal configuration phrasing. Internal details
// like "generator is not configured" must never reach the client-facing 502.
const wasmBackendUnavailableMsg = "the requested backend is not available"

func assertCleanBackendError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var be *BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BackendError, got %T: %v", err, err)
	}
	if be.Msg != wasmBackendUnavailableMsg {
		t.Errorf("message = %q, want clean user-facing %q", be.Msg, wasmBackendUnavailableMsg)
	}
	// Guard against internal phrasing leaking into the client response.
	for _, leak := range []string{"generator", "configured", "fixture match"} {
		if strings.Contains(be.Msg, leak) {
			t.Errorf("message %q leaks internal phrasing %q", be.Msg, leak)
		}
	}
}

func TestWasmBackendTokensNilGeneratorReturnsCleanBackendError(t *testing.T) {
	b := &wasmContentBackend{gen: nil}
	_, err := b.Tokens(context.Background(), GenerateRequest{FixtureMatch: &fixture.MatchRequest{}})
	assertCleanBackendError(t, err)
}

func TestWasmBackendTokensNilFixtureMatchReturnsCleanBackendError(t *testing.T) {
	// A non-nil generator passes the first guard, so this exercises the nil
	// FixtureMatch branch specifically. An empty Generator is sufficient: the
	// branch returns before any generator method is invoked.
	b := &wasmContentBackend{gen: &wasmgen.Generator{}}
	_, err := b.Tokens(context.Background(), GenerateRequest{})
	assertCleanBackendError(t, err)
}

func TestWasmBackendStreamNilGeneratorReturnsCleanBackendError(t *testing.T) {
	b := &wasmContentBackend{gen: nil}
	err := b.Stream(context.Background(), GenerateRequest{FixtureMatch: &fixture.MatchRequest{}}, func(string) error { return nil })
	assertCleanBackendError(t, err)
}
