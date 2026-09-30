package anthropic

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/ketang/zolem/internal/fixture"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// Production always attaches a listener runtime; these exercise that path.
func verbatimCtx() context.Context {
	return runtimecfg.WithListenerRuntime(context.Background(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "fx", Backend: runtimecfg.BackendFixture},
	})
}

func TestServeFixture_ErrorBodyVerbatimWithRuntime(t *testing.T) {
	const body = `{"error":{"message":"slow down","extra":[1,2]}}`
	rr := httptest.NewRecorder()
	ctx := verbatimCtx()
	f := &fixture.Fixture{Status: 429, ResponseBody: []byte(body)}
	serveFixture(rr, ctx, f, MessagesRequest{Model: "req-model"})
	if rr.Code != 429 || rr.Body.String() != body {
		t.Fatalf("got %d %q, want 429 %q", rr.Code, rr.Body.String(), body)
	}
}

func TestServeFixture_UnmodeledFieldsKeptWithRuntime(t *testing.T) {
	rr := httptest.NewRecorder()
	ctx := verbatimCtx()
	f := &fixture.Fixture{Status: 200, ResponseBody: []byte(`{"model":"x","unmodeled_field":{"a":1}}`)}
	serveFixture(rr, ctx, f, MessagesRequest{Model: "req-model"})
	if want := `{"model":"req-model","unmodeled_field":{"a":1}}`; rr.Body.String() != want {
		t.Fatalf("got %q want %q", rr.Body.String(), want)
	}
}
