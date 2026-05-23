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
	Match        match   `yaml:"match"`
}

type match struct {
	CEL   string   `yaml:"cel"`
	Score *float64 `yaml:"score"`
}

type fixturesYAML struct {
	Provider string                `yaml:"provider"`
	Version  string                `yaml:"version"`
	Fixtures []fixturesYAMLEntryIn `yaml:"fixtures"`
}

type fixturesYAMLEntryIn struct {
	Expression string `yaml:"expression"`
	Fixture    string `yaml:"fixture"`
}

type Loader struct {
	dir string
}

func NewLoader(dir string) *Loader {
	return &Loader{dir: dir}
}

// Load reads all fixture subdirectories. Each subdirectory must contain
// meta.yaml and response.json. match.wasm is optional. If the namespace
// root contains fixtures.yaml, a fixturesYAMLSelector is returned and per
// fixture match.cel/match.wasm files become illegal.
func (l *Loader) Load() ([]Fixture, Selector, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read fixture dir %q: %w", l.dir, err)
	}

	yamlPath := filepath.Join(l.dir, "fixtures.yaml")
	hasYAML, err := fileExists(yamlPath)
	if err != nil {
		return nil, nil, fmt.Errorf("stat fixtures.yaml: %w", err)
	}

	legacy := &LegacySelector{cel: map[string]*CompiledCELMatcher{}}
	var fixtures []Fixture
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f, celMatcher, hasWASMFile, err := loadOne(filepath.Join(l.dir, e.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("fixture %q: %w", e.Name(), err)
		}
		if hasYAML {
			if celMatcher != nil {
				return nil, nil, fmt.Errorf("fixture %q: match.cel is not allowed when fixtures.yaml is present", e.Name())
			}
			if hasWASMFile {
				return nil, nil, fmt.Errorf("fixture %q: match.wasm is not allowed when fixtures.yaml is present", e.Name())
			}
		} else if celMatcher != nil {
			legacy.cel[f.ID] = celMatcher
		}
		fixtures = append(fixtures, f)
	}

	if hasYAML {
		selector, err := loadFixturesYAML(yamlPath, fixtures)
		if err != nil {
			return nil, nil, err
		}
		return fixtures, selector, nil
	}
	return fixtures, legacy, nil
}

func loadFixturesYAML(path string, fixtures []Fixture) (Selector, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixtures.yaml: %w", err)
	}
	var doc fixturesYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse fixtures.yaml: %w", err)
	}
	known := make(map[string]struct{}, len(fixtures))
	for _, f := range fixtures {
		known[f.ID] = struct{}{}
	}
	sel := &fixturesYAMLSelector{}
	for i, entry := range doc.Fixtures {
		if entry.Expression == "" {
			return nil, fmt.Errorf("fixtures.yaml entry %d: missing expression", i)
		}
		if entry.Fixture == "" {
			return nil, fmt.Errorf("fixtures.yaml entry %d: missing fixture", i)
		}
		if _, ok := known[entry.Fixture]; !ok {
			return nil, fmt.Errorf("fixtures.yaml entry %d references unknown fixture %q", i, entry.Fixture)
		}
		m, err := CompileCELMatcher(entry.Expression, 1)
		if err != nil {
			return nil, fmt.Errorf("fixtures.yaml entry %d (%s): %w", i, entry.Fixture, err)
		}
		sel.entries = append(sel.entries, fixturesYAMLEntry{matcher: m, fixtureID: entry.Fixture})
	}
	return sel, nil
}

// loadOne returns the fixture, any compiled per-fixture CEL matcher, and
// whether the directory contained a match.wasm file (independent of whether
// the bytes have been compiled into a Module yet).
func loadOne(dir string) (Fixture, *CompiledCELMatcher, bool, error) {
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.yaml"))
	if err != nil {
		return Fixture{}, nil, false, fmt.Errorf("read meta.yaml: %w", err)
	}
	var m meta
	if err := yaml.Unmarshal(metaData, &m); err != nil {
		return Fixture{}, nil, false, fmt.Errorf("parse meta.yaml: %w", err)
	}
	if m.Status == 0 {
		m.Status = 200
	}

	bodyPath := filepath.Join(dir, "response.json")
	templatePath := filepath.Join(dir, "response.json.tmpl")
	hasBody, err := fileExists(bodyPath)
	if err != nil {
		return Fixture{}, nil, false, fmt.Errorf("stat response.json: %w", err)
	}
	hasTemplate, err := fileExists(templatePath)
	if err != nil {
		return Fixture{}, nil, false, fmt.Errorf("stat response.json.tmpl: %w", err)
	}
	switch {
	case hasBody && hasTemplate:
		return Fixture{}, nil, false, fmt.Errorf("only one of response.json or response.json.tmpl is allowed")
	case !hasBody && !hasTemplate:
		return Fixture{}, nil, false, fmt.Errorf("expected response.json or response.json.tmpl")
	}

	wasmPath := filepath.Join(dir, "match.wasm")
	hasWASM, err := fileExists(wasmPath)
	if err != nil {
		return Fixture{}, nil, false, fmt.Errorf("stat match.wasm: %w", err)
	}
	if !hasWASM {
		wasmPath = ""
	}
	if m.Match.CEL != "" && hasWASM {
		return Fixture{}, nil, false, fmt.Errorf("only one of match.cel or match.wasm is allowed")
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
	var celMatcher *CompiledCELMatcher
	if m.Match.CEL != "" {
		score := float64(1)
		if m.Match.Score != nil {
			score = *m.Match.Score
		}
		celMatcher, err = CompileCELMatcher(m.Match.CEL, score)
		if err != nil {
			return Fixture{}, nil, false, err
		}
	}

	if hasBody {
		body, err := os.ReadFile(bodyPath)
		if err != nil {
			return Fixture{}, nil, false, fmt.Errorf("read response.json: %w", err)
		}
		f.ResponseBody = body
	} else {
		body, err := os.ReadFile(templatePath)
		if err != nil {
			return Fixture{}, nil, false, fmt.Errorf("read response.json.tmpl: %w", err)
		}
		if err := f.SetResponseTemplate(body); err != nil {
			return Fixture{}, nil, false, err
		}
	}

	return f, celMatcher, hasWASM, nil
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
