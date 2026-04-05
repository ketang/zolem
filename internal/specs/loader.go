package specs

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed fallbacks/*.json
var fallbackFS embed.FS

type NormalizedSchema struct {
	Bytes []byte
}

type Normalizer interface {
	Normalize(source ContractSource, raw []byte) (NormalizedSchema, error)
}

type PassThroughNormalizer struct{}

func (PassThroughNormalizer) Normalize(source ContractSource, raw []byte) (NormalizedSchema, error) {
	switch source.Kind {
	case SourceKindVendoredDocsSnapshot:
		return NormalizedSchema{Bytes: raw}, nil
	default:
		return NormalizedSchema{}, fmt.Errorf("normalization for source kind %q is not implemented", source.Kind)
	}
}

type ContractLoader struct {
	cacheDir   string
	fetchURL   func(string) ([]byte, error)
	normalizer Normalizer
}

func NewContractLoader(cacheDir string) *ContractLoader {
	return &ContractLoader{
		cacheDir:   cacheDir,
		fetchURL:   fetchURL,
		normalizer: PassThroughNormalizer{},
	}
}

func (l *ContractLoader) LoadFallback(source ContractSource) (NormalizedSchema, error) {
	data, err := fallbackFS.ReadFile(source.FallbackPath)
	if err != nil {
		return NormalizedSchema{}, fmt.Errorf("read fallback %q: %w", source.FallbackPath, err)
	}
	return NormalizedSchema{Bytes: data}, nil
}

func (l *ContractLoader) Refresh(source ContractSource) (NormalizedSchema, error) {
	if !source.HasRemote() {
		return NormalizedSchema{}, fmt.Errorf("no remote source configured")
	}

	data, err := l.fetchURL(source.RemoteURL)
	if err != nil {
		return NormalizedSchema{}, err
	}
	if err := l.writeCache(source, data); err != nil {
		return NormalizedSchema{}, err
	}
	return l.normalizer.Normalize(source, data)
}

func (l *ContractLoader) writeCache(source ContractSource, data []byte) error {
	if l.cacheDir == "" {
		return nil
	}

	if err := os.MkdirAll(l.cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	cachePath := filepath.Join(l.cacheDir, source.Provider+"-"+source.Version+".raw")
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		return fmt.Errorf("write cache %q: %w", cachePath, err)
	}
	return nil
}
