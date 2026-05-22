package fixture

import "text/template"

// Fixture represents a loaded canned response with its compiled match module.
type Fixture struct {
	ID           string
	Provider     string
	Version      string
	Stream       bool
	Status       int
	ResponseBody []byte
	Templated    bool
	TemplateSeed *uint64
	templateBody *template.Template
	WASMPath     string          // path to match.wasm; empty if not yet loaded
	Module       *CompiledModule // nil if no match.wasm present
	CELMatcher   *CompiledCELMatcher
}

// SetResponseTemplate parses and stores the fixture response template.
func (f *Fixture) SetResponseTemplate(body []byte) error {
	tmpl, err := parseResponseTemplate(string(body))
	if err != nil {
		return err
	}
	f.Templated = true
	f.ResponseBody = body
	f.templateBody = tmpl
	return nil
}

func (f Fixture) HasMatcher() bool {
	return f.Module != nil || f.WASMPath != "" || f.CELMatcher != nil
}
