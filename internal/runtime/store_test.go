package runtimecfg_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	runtimecfg "zolem.dev/zolem/internal/runtime"
)

func TestStore_ProfileLifecycle(t *testing.T) {
	store := runtimecfg.NewStore()

	profile, err := store.UpsertProfile(runtimecfg.RuntimeProfile{Name: "demo", Backend: "lorem"})
	if err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if profile.Name != "demo" {
		t.Fatalf("profile name: got %q, want demo", profile.Name)
	}

	profiles := store.ListProfiles()
	if len(profiles) != 1 || profiles[0].Name != "demo" {
		t.Fatalf("profiles: got %#v", profiles)
	}

	if err := store.DeleteProfile("demo"); err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	if _, ok := store.GetProfile("demo"); ok {
		t.Fatal("expected profile to be removed")
	}
}

func TestStore_ListProfilesAndListenersSorted(t *testing.T) {
	store := runtimecfg.NewStore()
	for _, name := range []string{"zeta", "alpha", "middle"} {
		if _, err := store.UpsertProfile(runtimecfg.RuntimeProfile{Name: name, Backend: "lorem"}); err != nil {
			t.Fatalf("upsert profile %s: %v", name, err)
		}
	}
	gotProfiles := profileNames(store.ListProfiles())
	if want := []string{"alpha", "middle", "zeta"}; !slices.Equal(gotProfiles, want) {
		t.Fatalf("profiles = %v, want %v", gotProfiles, want)
	}

	for _, spec := range []runtimecfg.ListenerSpec{
		{Name: "zeta", Addr: "127.0.0.1:12003", Provider: "openai", Profile: "zeta"},
		{Name: "alpha", Addr: "127.0.0.1:12001", Provider: "openai", Profile: "alpha"},
		{Name: "middle", Addr: "127.0.0.1:12002", Provider: "openai", Profile: "middle"},
	} {
		if _, err := store.UpsertListener(spec); err != nil {
			t.Fatalf("upsert listener %s: %v", spec.Name, err)
		}
	}
	gotListeners := listenerNames(store.ListListeners())
	if want := []string{"alpha", "middle", "zeta"}; !slices.Equal(gotListeners, want) {
		t.Fatalf("listeners = %v, want %v", gotListeners, want)
	}
}

func profileNames(profiles []runtimecfg.RuntimeProfile) []string {
	out := make([]string, len(profiles))
	for i, profile := range profiles {
		out[i] = profile.Name
	}
	return out
}

func listenerNames(listeners []runtimecfg.ListenerSpec) []string {
	out := make([]string, len(listeners))
	for i, listener := range listeners {
		out[i] = listener.Name
	}
	return out
}

func TestStore_DeleteProfileRejectsInUseProfile(t *testing.T) {
	store := runtimecfg.NewStore()
	if _, err := store.UpsertProfile(runtimecfg.RuntimeProfile{Name: "demo", Backend: "lorem"}); err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if _, err := store.UpsertListener(runtimecfg.ListenerSpec{
		Name:     "openai-demo",
		Addr:     "127.0.0.1:12001",
		Provider: "openai",
		Profile:  "demo",
	}); err != nil {
		t.Fatalf("upsert listener: %v", err)
	}

	err := store.DeleteProfile("demo")
	if !errors.Is(err, runtimecfg.ErrProfileInUse) {
		t.Fatalf("delete profile error: got %v, want ErrProfileInUse", err)
	}
}

func TestValidateListenerSpecRejectsNonLoopbackAddr(t *testing.T) {
	err := runtimecfg.ValidateListenerSpec(runtimecfg.ListenerSpec{
		Name:     "openai-demo",
		Addr:     "0.0.0.0:12001",
		Provider: "openai",
		Profile:  "demo",
	})
	if err == nil {
		t.Fatal("expected non-loopback listener addr to fail validation")
	}
}

func TestStoreDeleteMissingResources(t *testing.T) {
	store := runtimecfg.NewStore()
	if err := store.DeleteProfile("missing"); !errors.Is(err, runtimecfg.ErrProfileNotFound) {
		t.Fatalf("DeleteProfile missing = %v, want ErrProfileNotFound", err)
	}
	if err := store.DeleteListener("missing"); !errors.Is(err, runtimecfg.ErrListenerNotFound) {
		t.Fatalf("DeleteListener missing = %v, want ErrListenerNotFound", err)
	}
}

func TestValidateListenerSpecRejectsMissingAndUnsupportedFields(t *testing.T) {
	tests := []runtimecfg.ListenerSpec{
		{Addr: "127.0.0.1:12001", Provider: "openai", Profile: "demo"},
		{Name: "demo", Addr: "127.0.0.1:12001", Provider: "openai"},
		{Name: "demo", Addr: "127.0.0.1:12001", Provider: "bogus", Profile: "demo"},
		{Name: "demo", Addr: "not-host-port", Provider: "openai", Profile: "demo"},
	}
	for _, spec := range tests {
		if err := runtimecfg.ValidateListenerSpec(spec); err == nil {
			t.Fatalf("ValidateListenerSpec(%+v) unexpectedly succeeded", spec)
		}
	}
	if err := runtimecfg.ValidateListenerSpec(runtimecfg.ListenerSpec{Name: "demo", Addr: "localhost:12001", Provider: "gemini", Profile: "demo"}); err != nil {
		t.Fatalf("localhost listener should be valid: %v", err)
	}
}

func TestValidateProfileRejectsMissingNameAndInvalidNestedFields(t *testing.T) {
	tests := []runtimecfg.RuntimeProfile{
		{Backend: "lorem"},
		{Name: "demo", Backend: "lorem", FixtureNamespace: "nested//bad"},
		{Name: "demo", Backend: "lorem", FixtureNamespace: `bad\path`},
		{Name: "demo", Backend: "lorem", ResponseModelPolicy: runtimecfg.ResponseModelForceLiteral},
		{Name: "demo", Backend: "lorem", OllamaUpstream: "http://"},
		{Name: "demo", Backend: "lorem", WASMModuleBase64: "AGFzbQEAAA=="},
		{Name: "demo", Backend: "lorem", WASMGenerateTimeoutMS: 10},
		{Name: "demo", Backend: runtimecfg.BackendWASM, WASMModuleBase64: "AGFzbQEAAA==", WASMGenerateTimeoutMS: -1},
		{Name: "demo", Backend: "lorem", StreamDelay: runtimecfg.StreamDelay{MS: 1}},
		{Name: "demo", Backend: "lorem", StreamDelay: runtimecfg.StreamDelay{Mode: "fixed", MinMS: 1}},
		{Name: "demo", Backend: "lorem", StreamDelay: runtimecfg.StreamDelay{Mode: "random", MinMS: 2, MaxMS: 1}},
		{Name: "demo", Backend: "lorem", StreamDelay: runtimecfg.StreamDelay{Mode: "random", MS: 1}},
		{Name: "demo", Backend: "lorem", StreamDelay: runtimecfg.StreamDelay{Mode: "bogus"}},
	}
	for _, profile := range tests {
		if err := runtimecfg.ValidateProfile(profile); err == nil {
			t.Fatalf("ValidateProfile(%+v) unexpectedly succeeded", profile)
		}
	}
}

func TestValidateProfileRejectsUnsupportedBackend(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:    "demo",
		Backend: "bogus",
	})
	if err == nil {
		t.Fatal("expected unsupported backend to fail validation")
	}
}

func TestValidateProfileAcceptsFixtureBackend(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:    "demo",
		Backend: "fixture",
	})
	if err != nil {
		t.Fatalf("expected fixture backend to be valid, got %v", err)
	}
}

func TestValidateProfileRejectsInvalidFixtureNamespace(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:             "demo",
		Backend:          "fixture",
		FixtureNamespace: "../escape",
	})
	if err == nil {
		t.Fatal("expected invalid fixture namespace to fail validation")
	}
}

func TestValidateProfileRejectsInvalidResponseModelPolicy(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:                "demo",
		Backend:             "lorem",
		ResponseModelPolicy: "bogus",
	})
	if err == nil {
		t.Fatal("expected invalid response_model_policy to fail validation")
	}
}

func TestValidateProfileRejectsForceLiteralWithoutResponseModel(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:                "demo",
		Backend:             "lorem",
		ResponseModelPolicy: runtimecfg.ResponseModelForceLiteral,
	})
	if err == nil {
		t.Fatal("expected missing response model to fail validation")
	}
}

func TestValidateProfile_OllamaBackend(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:    "test",
		Backend: "ollama",
	})
	if err != nil {
		t.Fatalf("ollama backend should be valid: %v", err)
	}
}

func TestValidateProfile_OllamaUpstreamValid(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:           "test",
		Backend:        "ollama",
		OllamaUpstream: "http://localhost:11434",
	})
	if err != nil {
		t.Fatalf("valid ollama upstream should pass: %v", err)
	}
}

func TestValidateProfile_OllamaUpstreamInvalidURL(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:           "test",
		Backend:        "ollama",
		OllamaUpstream: "not-a-url",
	})
	if err == nil {
		t.Fatal("expected invalid ollama upstream to fail validation")
	}
}

func TestValidateProfile_OllamaUpstreamBadScheme(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:           "test",
		Backend:        "ollama",
		OllamaUpstream: "ftp://localhost:11434",
	})
	if err == nil {
		t.Fatal("expected non-http scheme to fail validation")
	}
}

func TestValidateProfileRejectsErrorTypeWithoutErrorBackend(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:      "demo",
		Backend:   "lorem",
		ErrorType: runtimecfg.ErrorTypeRateLimit,
	})
	if err == nil {
		t.Fatal("expected error_type without error backend to fail validation")
	}
}

func TestValidateProfileRejectsErrorBackendWithoutErrorType(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:    "demo",
		Backend: runtimecfg.BackendError,
	})
	if err == nil {
		t.Fatal("expected missing error_type to fail validation")
	}
}

func TestValidateProfileAcceptsErrorBackendWithErrorType(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:      "demo",
		Backend:   runtimecfg.BackendError,
		ErrorType: runtimecfg.ErrorTypeRateLimit,
	})
	if err != nil {
		t.Fatalf("expected error backend to be valid, got %v", err)
	}
}

func TestValidateProfileRejectsWASMBackendWithoutModule(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:    "demo",
		Backend: runtimecfg.BackendWASM,
	})
	if err == nil {
		t.Fatal("expected wasm backend without module to fail validation")
	}
}

func TestValidateProfileRejectsWASMTimeoutOutOfBounds(t *testing.T) {
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:                  "demo",
		Backend:               runtimecfg.BackendWASM,
		WASMModuleBase64:      "AGFzbQEAAA==",
		WASMGenerateTimeoutMS: 5001,
	})
	if err == nil {
		t.Fatal("expected out-of-bounds wasm timeout to fail validation")
	}
}

func TestValidateProfileAcceptsWASMStreamDelay(t *testing.T) {
	seed := int64(7)
	err := runtimecfg.ValidateProfile(runtimecfg.RuntimeProfile{
		Name:             "demo",
		Backend:          runtimecfg.BackendWASM,
		WASMModuleBase64: "AGFzbQEAAA==",
		StreamDelay: runtimecfg.StreamDelay{
			Mode:  "random",
			MinMS: 1,
			MaxMS: 3,
			Seed:  &seed,
		},
	})
	if err != nil {
		t.Fatalf("expected wasm stream delay profile to be valid, got %v", err)
	}
}

func TestStreamDelayForRequestFixedRandomAndCancellation(t *testing.T) {
	if delay := runtimecfg.StreamDelayForRequest(context.Background()); delay != nil {
		t.Fatalf("StreamDelayForRequest without runtime = %v, want nil", delay)
	}

	fixedCtx := runtimecfg.WithListenerRuntime(context.Background(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "fixed", StreamDelay: runtimecfg.StreamDelay{Mode: "fixed", MS: 0}},
	})
	if err := runtimecfg.StreamDelayForRequest(fixedCtx)(fixedCtx); err != nil {
		t.Fatalf("zero fixed delay: %v", err)
	}

	cancelCtx, cancel := context.WithCancel(runtimecfg.WithListenerRuntime(context.Background(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "fixed", StreamDelay: runtimecfg.StreamDelay{Mode: "fixed", MS: 50}},
	}))
	cancel()
	if err := runtimecfg.StreamDelayForRequest(cancelCtx)(cancelCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled fixed delay = %v, want context.Canceled", err)
	}

	seed := int64(11)
	randomBase := runtimecfg.WithListenerRuntime(context.Background(), runtimecfg.ListenerRuntime{
		Profile: runtimecfg.RuntimeProfile{Name: "random", StreamDelay: runtimecfg.StreamDelay{Mode: "random", MinMS: 0, MaxMS: 0, Seed: &seed}},
	})
	randomCtx := runtimecfg.WithProfileRequestSequence(randomBase, 3)
	start := time.Now()
	if err := runtimecfg.StreamDelayForRequest(randomCtx)(randomCtx); err != nil {
		t.Fatalf("zero random delay: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 25*time.Millisecond {
		t.Fatalf("zero random delay took too long: %v", elapsed)
	}
}
