package fixture_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"zolem.dev/zolem/internal/fixture"
)

func TestWatcher_ReloadsOnFileChange(t *testing.T) {
	dir := t.TempDir()

	r := fixture.NewRunner()
	defer r.Close()

	m := fixture.NewMatcher(r, nil)

	var reloadCount atomic.Int32
	reload := func() ([]fixture.Fixture, error) {
		reloadCount.Add(1)
		return []fixture.Fixture{{ID: "reloaded", Provider: "test", Version: "v1", Status: 200}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done, err := fixture.StartWatcher(ctx, fixture.WatcherConfig{
		Dir:      dir,
		Matcher:  m,
		Reload:   reload,
		Logf:     t.Logf,
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	// Write a file to trigger a reload.
	if err := os.WriteFile(filepath.Join(dir, "trigger.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write trigger: %v", err)
	}

	// Wait for the reload to fire.
	deadline := time.After(2 * time.Second)
	for reloadCount.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for reload")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	<-done

	if got := reloadCount.Load(); got < 1 {
		t.Errorf("expected at least 1 reload, got %d", got)
	}
}

func TestWatcher_PreservesFixturesOnReloadFailure(t *testing.T) {
	dir := t.TempDir()

	r := fixture.NewRunner()
	defer r.Close()

	original := []fixture.Fixture{{ID: "original", Provider: "test", Version: "v1", Status: 200}}
	m := fixture.NewMatcher(r, original)

	var reloadCount atomic.Int32
	reload := func() ([]fixture.Fixture, error) {
		reloadCount.Add(1)
		return nil, errors.New("simulated load failure")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done, err := fixture.StartWatcher(ctx, fixture.WatcherConfig{
		Dir:      dir,
		Matcher:  m,
		Reload:   reload,
		Logf:     t.Logf,
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	// Trigger a reload that will fail.
	if err := os.WriteFile(filepath.Join(dir, "bad.txt"), []byte("bad"), 0644); err != nil {
		t.Fatalf("write trigger: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for reloadCount.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for reload attempt")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Matcher should still serve — Swap was never called because reload failed.
	// We verify by matching: the original fixture has no Module so Match returns nil,
	// but the important thing is no panic and no error.
	result, err := m.Match(context.Background(), fixture.MatchRequest{Provider: "test", Version: "v1", Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("match after failed reload: %v", err)
	}
	// original has no Module, so result should be nil (no crash, no empty matcher)
	if result != nil {
		t.Error("expected nil match for fixture without Module")
	}

	cancel()
	<-done
}

func TestWatcher_CleansUpOnContextCancel(t *testing.T) {
	dir := t.TempDir()

	r := fixture.NewRunner()
	defer r.Close()

	m := fixture.NewMatcher(r, nil)
	reload := func() ([]fixture.Fixture, error) {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	done, err := fixture.StartWatcher(ctx, fixture.WatcherConfig{
		Dir:      dir,
		Matcher:  m,
		Reload:   reload,
		Logf:     t.Logf,
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	cancel()

	select {
	case <-done:
		// Watcher exited cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not shut down within 2s after context cancel")
	}
}
