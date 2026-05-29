package specs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingNormalizer struct {
	source ContractSource
	raw    []byte
	err    error
}

func (n *recordingNormalizer) Normalize(source ContractSource, raw []byte) (NormalizedSchema, error) {
	n.source = source
	n.raw = append([]byte(nil), raw...)
	if n.err != nil {
		return NormalizedSchema{}, n.err
	}
	return NormalizedSchema{Bytes: append([]byte("normalized:"), raw...)}, nil
}

func TestPassThroughNormalizerVendoredAndUnsupported(t *testing.T) {
	raw := []byte(`{"type":"object"}`)
	got, err := (PassThroughNormalizer{}).Normalize(ContractSource{Kind: SourceKindVendoredDocsSnapshot}, raw)
	if err != nil {
		t.Fatalf("vendored normalize: %v", err)
	}
	if !bytes.Equal(got.Bytes, raw) {
		t.Fatalf("vendored bytes = %s, want %s", got.Bytes, raw)
	}

	_, err = (PassThroughNormalizer{}).Normalize(ContractSource{Kind: SourceKind("unknown")}, raw)
	if err == nil || !strings.Contains(err.Error(), `source kind "unknown"`) {
		t.Fatalf("unsupported kind error = %v", err)
	}
}

func TestContractLoaderLoadFallback(t *testing.T) {
	loader := NewContractLoader("")
	got, err := loader.LoadFallback(ContractSource{FallbackPath: "fallbacks/anthropic-v1.json"})
	if err != nil {
		t.Fatalf("LoadFallback: %v", err)
	}
	if !bytes.Contains(got.Bytes, []byte(`"messages"`)) {
		t.Fatalf("fallback did not contain messages schema: %.80s", got.Bytes)
	}

	_, err = loader.LoadFallback(ContractSource{FallbackPath: "fallbacks/missing.json"})
	if err == nil || !strings.Contains(err.Error(), "read fallback") {
		t.Fatalf("missing fallback error = %v", err)
	}
}

func TestContractLoaderRefreshFetchesCachesAndNormalizes(t *testing.T) {
	cacheDir := t.TempDir()
	normalizer := &recordingNormalizer{}
	loader := &ContractLoader{
		cacheDir: cacheDir,
		fetchURL: func(url string) ([]byte, error) {
			if url != "https://example.test/schema.json" {
				t.Fatalf("fetch URL = %q", url)
			}
			return []byte(`{"raw":true}`), nil
		},
		normalizer: normalizer,
	}
	source := ContractSource{
		Provider:  "openai",
		Version:   "v1",
		Kind:      SourceKindVendoredDocsSnapshot,
		RemoteURL: "https://example.test/schema.json",
	}

	got, err := loader.Refresh(source)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if string(got.Bytes) != `normalized:{"raw":true}` {
		t.Fatalf("normalized bytes = %s", got.Bytes)
	}
	if normalizer.source != source || string(normalizer.raw) != `{"raw":true}` {
		t.Fatalf("normalizer saw source=%+v raw=%s", normalizer.source, normalizer.raw)
	}
	cached, err := os.ReadFile(filepath.Join(cacheDir, "openai-v1.raw"))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if string(cached) != `{"raw":true}` {
		t.Fatalf("cached bytes = %s", cached)
	}
}

func TestContractLoaderRefreshErrors(t *testing.T) {
	loader := &ContractLoader{}
	if _, err := loader.Refresh(ContractSource{}); err == nil || !strings.Contains(err.Error(), "no remote source") {
		t.Fatalf("no remote error = %v", err)
	}

	fetchErr := errors.New("network down")
	loader = &ContractLoader{
		fetchURL: func(string) ([]byte, error) { return nil, fetchErr },
	}
	_, err := loader.Refresh(ContractSource{RemoteURL: "https://example.test/schema.json"})
	if !errors.Is(err, fetchErr) {
		t.Fatalf("fetch error = %v, want %v", err, fetchErr)
	}

	normalizer := &recordingNormalizer{err: errors.New("bad schema")}
	loader = &ContractLoader{
		cacheDir:   filepath.Join(t.TempDir(), "cache"),
		fetchURL:   func(string) ([]byte, error) { return []byte("raw"), nil },
		normalizer: normalizer,
	}
	_, err = loader.Refresh(ContractSource{Provider: "p", Version: "v", RemoteURL: "https://example.test/schema.json"})
	if err == nil || !strings.Contains(err.Error(), "bad schema") {
		t.Fatalf("normalizer error = %v", err)
	}
}
