package fixture

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type meta struct {
	ID           string  `yaml:"id"`
	Provider     string  `yaml:"provider"`
	Version      string  `yaml:"version"`
	Stream       bool    `yaml:"stream"`
	Status       int     `yaml:"status"`
	TemplateSeed *uint64 `yaml:"template_seed"`
}

type Loader struct {
	dir string
}

func NewLoader(dir string) *Loader {
	return &Loader{dir: dir}
}

// Load reads all fixture subdirectories. Each subdirectory must contain
// meta.yaml and response.json. match.wasm is optional.
func (l *Loader) Load() ([]Fixture, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, fmt.Errorf("read fixture dir %q: %w", l.dir, err)
	}

	var fixtures []Fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f, err := loadOne(filepath.Join(l.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("fixture %q: %w", e.Name(), err)
		}
		fixtures = append(fixtures, f)
	}
	return fixtures, nil
}

func loadOne(dir string) (Fixture, error) {
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.yaml"))
	if err != nil {
		return Fixture{}, fmt.Errorf("read meta.yaml: %w", err)
	}
	var m meta
	if err := yaml.Unmarshal(metaData, &m); err != nil {
		return Fixture{}, fmt.Errorf("parse meta.yaml: %w", err)
	}
	if m.Status == 0 {
		m.Status = 200
	}

	bodyPath := filepath.Join(dir, "response.json")
	templatePath := filepath.Join(dir, "response.json.tmpl")
	hasBody, err := fileExists(bodyPath)
	if err != nil {
		return Fixture{}, fmt.Errorf("stat response.json: %w", err)
	}
	hasTemplate, err := fileExists(templatePath)
	if err != nil {
		return Fixture{}, fmt.Errorf("stat response.json.tmpl: %w", err)
	}
	switch {
	case hasBody && hasTemplate:
		return Fixture{}, fmt.Errorf("only one of response.json or response.json.tmpl is allowed")
	case !hasBody && !hasTemplate:
		return Fixture{}, fmt.Errorf("expected response.json or response.json.tmpl")
	}

	wasmPath := filepath.Join(dir, "match.wasm")
	if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
		wasmPath = ""
	}

	f := Fixture{
		ID:           m.ID,
		Provider:     m.Provider,
		Version:      m.Version,
		Stream:       m.Stream,
		Status:       m.Status,
		TemplateSeed: m.TemplateSeed,
		WASMPath:     wasmPath,
	}

	if hasBody {
		body, err := os.ReadFile(bodyPath)
		if err != nil {
			return Fixture{}, fmt.Errorf("read response.json: %w", err)
		}
		f.ResponseBody = body
	} else {
		body, err := os.ReadFile(templatePath)
		if err != nil {
			return Fixture{}, fmt.Errorf("read response.json.tmpl: %w", err)
		}
		if err := f.SetResponseTemplate(body); err != nil {
			return Fixture{}, err
		}
	}

	return f, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}
