// internal/specs/validator.go
package specs

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidationError wraps schema validation failures.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return "validation failed: " + strings.Join(e.Errors, "; ")
}

// Validator holds compiled JSON schemas per (provider, version).
type Validator struct {
	mu      sync.RWMutex
	schemas map[string]*jsonschema.Schema
}

func NewValidator() *Validator {
	return &Validator{schemas: make(map[string]*jsonschema.Schema)}
}

func (v *Validator) LoadNormalized(provider, version string, schema NormalizedSchema) error {
	return v.LoadRaw(provider, version, schema.Bytes)
}

// LoadRaw compiles and stores a schema from raw JSON bytes.
func (v *Validator) LoadRaw(provider, version string, data []byte) error {
	// v6 AddResource takes a parsed any, not an io.Reader
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parse schema JSON: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	uri := "mem://" + provider + "-" + version + ".json"
	if err := compiler.AddResource(uri, doc); err != nil {
		return fmt.Errorf("add resource: %w", err)
	}
	schema, err := compiler.Compile(uri)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	v.mu.Lock()
	v.schemas[provider+":"+version] = schema
	v.mu.Unlock()
	return nil
}

// Validate validates body against the schema for (provider, version).
// Returns nil if no schema is loaded (pass-through) or if validation passes.
func (v *Validator) Validate(provider, version string, body []byte) error {
	v.mu.RLock()
	schema, ok := v.schemas[provider+":"+version]
	v.mu.RUnlock()
	if !ok {
		return nil
	}

	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return &ValidationError{Errors: []string{"invalid JSON: " + err.Error()}}
	}
	if err := schema.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if ok := asValidationError(err, &ve); ok {
			msgs := collectMessages(ve)
			return &ValidationError{Errors: msgs}
		}
		return &ValidationError{Errors: []string{err.Error()}}
	}
	return nil
}

func asValidationError(err error, out **jsonschema.ValidationError) bool {
	if ve, ok := err.(*jsonschema.ValidationError); ok {
		*out = ve
		return true
	}
	return false
}

func collectMessages(ve *jsonschema.ValidationError) []string {
	var msgs []string
	msgs = append(msgs, ve.Error())
	for _, c := range ve.Causes {
		msgs = append(msgs, collectMessages(c)...)
	}
	return msgs
}
