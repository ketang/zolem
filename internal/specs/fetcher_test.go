// internal/specs/fetcher_test.go
package specs_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"zolem.dev/zolem/internal/specs"
)

func TestFetcher_FetchAndCache(t *testing.T) {
	content := `{"openapi":"3.0.0","info":{"title":"Test"},"paths":{}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	sources := map[string]string{
		"test:v1": srv.URL,
	}
	f := specs.NewFetcher(cacheDir, sources)

	data, err := f.Get("test", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != content {
		t.Errorf("data mismatch")
	}

	cached := filepath.Join(cacheDir, "test-v1.json")
	if _, err := os.Stat(cached); os.IsNotExist(err) {
		t.Error("expected cache file to exist")
	}
}

func TestFetcher_FallbackOnNetworkError(t *testing.T) {
	cacheDir := t.TempDir()
	sources := map[string]string{
		"test:v1": "http://localhost:0",
	}
	fallback := []byte(`{"openapi":"3.0.0","paths":{}}`)
	f := specs.NewFetcherWithFallback(cacheDir, sources, map[string][]byte{
		"test:v1": fallback,
	})

	data, err := f.Get("test", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != string(fallback) {
		t.Error("expected fallback data")
	}
}

func TestFetcher_ServesFromCacheOnSecondCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	f := specs.NewFetcher(cacheDir, map[string]string{"p:v1": srv.URL})

	f.Get("p", "v1")
	f.Get("p", "v1")

	if calls != 1 {
		t.Errorf("expected 1 HTTP call, got %d", calls)
	}
}
