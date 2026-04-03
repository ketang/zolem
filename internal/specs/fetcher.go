// internal/specs/fetcher.go
package specs

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// Fetcher retrieves and caches OpenAPI spec content per (provider, version) pair.
type Fetcher struct {
	cacheDir  string
	sources   map[string]string // key: "provider:version"
	fallbacks map[string][]byte
	mu        sync.RWMutex
	cache     map[string][]byte // in-memory cache
}

func NewFetcher(cacheDir string, sources map[string]string) *Fetcher {
	return NewFetcherWithFallback(cacheDir, sources, nil)
}

func NewFetcherWithFallback(cacheDir string, sources map[string]string, fallbacks map[string][]byte) *Fetcher {
	return &Fetcher{
		cacheDir:  cacheDir,
		sources:   sources,
		fallbacks: fallbacks,
		cache:     make(map[string][]byte),
	}
}

// Get returns the spec for (provider, version). Order: in-memory cache → disk cache → HTTP → fallback.
func (f *Fetcher) Get(provider, version string) ([]byte, error) {
	key := provider + ":" + version

	f.mu.RLock()
	if data, ok := f.cache[key]; ok {
		f.mu.RUnlock()
		return data, nil
	}
	f.mu.RUnlock()

	// disk cache
	diskPath := filepath.Join(f.cacheDir, provider+"-"+version+".json")
	if data, err := os.ReadFile(diskPath); err == nil {
		f.store(key, data)
		return data, nil
	}

	// HTTP fetch
	url, ok := f.sources[key]
	if ok {
		if data, err := fetchURL(url); err == nil {
			os.MkdirAll(f.cacheDir, 0o755)
			os.WriteFile(diskPath, data, 0o644)
			f.store(key, data)
			return data, nil
		}
	}

	// fallback
	if f.fallbacks != nil {
		if data, ok := f.fallbacks[key]; ok {
			f.store(key, data)
			return data, nil
		}
	}

	return nil, fmt.Errorf("no spec available for %s/%s", provider, version)
}

func (f *Fetcher) store(key string, data []byte) {
	f.mu.Lock()
	f.cache[key] = data
	f.mu.Unlock()
}

func fetchURL(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}
