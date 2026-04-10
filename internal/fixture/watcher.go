package fixture

import (
	"context"
	"log"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ReloadFunc loads a fresh set of fixtures. It is called by the Watcher
// whenever the fixture directory changes.
type ReloadFunc func() ([]Fixture, error)

// WatcherConfig holds the parameters for starting a fixture directory watcher.
type WatcherConfig struct {
	Dir     string
	Matcher *Matcher
	Reload  ReloadFunc
	Logf    func(string, ...any)
	// Debounce is the quiet period after the last filesystem event before
	// triggering a reload. Zero means use the default (500ms).
	Debounce time.Duration
}

func (c *WatcherConfig) debounce() time.Duration {
	if c.Debounce > 0 {
		return c.Debounce
	}
	return 500 * time.Millisecond
}

func (c *WatcherConfig) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// StartWatcher watches the fixture directory for changes and hot-reloads
// fixtures into the matcher. It returns a done channel that is closed when
// the watcher goroutine exits.
//
// The watcher debounces filesystem events so that a burst of writes (e.g.
// saving multiple files) triggers a single reload.
//
// On reload failure the last known-good fixture set is preserved.
func StartWatcher(ctx context.Context, cfg WatcherConfig) (<-chan struct{}, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := watcher.Add(cfg.Dir); err != nil {
		watcher.Close()
		return nil, err
	}

	done := make(chan struct{})
	go watchLoop(ctx, watcher, cfg, done)
	return done, nil
}

func watchLoop(ctx context.Context, watcher *fsnotify.Watcher, cfg WatcherConfig, done chan struct{}) {
	defer close(done)
	defer watcher.Close()

	var debounceTimer *time.Timer
	var debounceCh <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !isRelevant(event) {
				continue
			}
			// Reset debounce timer on every relevant event.
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(cfg.debounce())
				debounceCh = debounceTimer.C
			} else {
				debounceTimer.Reset(cfg.debounce())
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			cfg.logf("fixture watcher error: %v", err)

		case <-debounceCh:
			debounceTimer = nil
			debounceCh = nil
			reload(cfg)
		}
	}
}

func isRelevant(event fsnotify.Event) bool {
	return event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0
}

func reload(cfg WatcherConfig) {
	fixtures, err := cfg.Reload()
	if err != nil {
		cfg.logf("fixture reload failed (keeping previous): %v", err)
		return
	}
	cfg.Matcher.Swap(fixtures)
	cfg.logf("fixtures reloaded: %d fixture(s)", len(fixtures))
}
