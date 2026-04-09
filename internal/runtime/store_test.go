package runtimecfg_test

import (
	"errors"
	"testing"

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
