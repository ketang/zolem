package specs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

var geminiDiscoveryMethods = []string{
	"models.generateContent",
	"models.streamGenerateContent",
}

type discoveryDocument struct {
	Resources map[string]discoveryResource `json:"resources"`
	Schemas   map[string]discoverySchema   `json:"schemas"`
}

type discoveryResource struct {
	Methods   map[string]discoveryMethod   `json:"methods"`
	Resources map[string]discoveryResource `json:"resources"`
}

type discoveryMethod struct {
	ID      string        `json:"id"`
	Request *discoveryRef `json:"request"`
}

type discoveryRef struct {
	Ref string `json:"$ref"`
}

type discoverySchema struct {
	Type                 string                     `json:"type"`
	Ref                  string                     `json:"$ref"`
	Description          string                     `json:"description,omitempty"`
	Enum                 []string                   `json:"enum,omitempty"`
	Required             []string                   `json:"required,omitempty"`
	Properties           map[string]discoverySchema `json:"properties,omitempty"`
	Items                *discoverySchema           `json:"items,omitempty"`
	AdditionalProperties *discoverySchema           `json:"additionalProperties,omitempty"`
}

// LoadProviderSchema normalizes provider-specific source documents before
// compiling them into the validator.
func LoadProviderSchema(validator *Validator, provider, version string, data []byte) error {
	if provider == "gemini" {
		normalized, err := NormalizeGeminiDiscovery(version, data)
		if err != nil {
			return err
		}
		return validator.LoadRaw(provider, version, normalized)
	}
	return validator.LoadRaw(provider, version, data)
}

// NormalizeGeminiDiscovery extracts the Gemini generateContent request schema from
// a Google Discovery document and converts it into JSON Schema.
func NormalizeGeminiDiscovery(version string, data []byte) ([]byte, error) {
	var doc discoveryDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse discovery document for gemini/%s: %w", version, err)
	}

	reqRef, err := geminiRequestRef(doc)
	if err != nil {
		return nil, fmt.Errorf("extract gemini/%s request schema: %w", version, err)
	}

	root, defs, err := discoveryToJSONSchema(doc.Schemas, reqRef)
	if err != nil {
		return nil, fmt.Errorf("normalize gemini/%s discovery schema: %w", version, err)
	}

	payload := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
	}
	for k, v := range root {
		payload[k] = v
	}
	if len(defs) > 0 {
		payload["$defs"] = defs
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, fmt.Errorf("encode normalized gemini/%s schema: %w", version, err)
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func geminiRequestRef(doc discoveryDocument) (string, error) {
	methods := collectDiscoveryMethods(doc.Resources)
	var reqRef string
	for _, want := range geminiDiscoveryMethods {
		method, ok := methods[want]
		if !ok {
			return "", fmt.Errorf("method %q not found", want)
		}
		if method.Request == nil || method.Request.Ref == "" {
			return "", fmt.Errorf("method %q has no request schema", want)
		}
		if reqRef == "" {
			reqRef = method.Request.Ref
			continue
		}
		if method.Request.Ref != reqRef {
			return "", fmt.Errorf("method %q uses request schema %q, expected %q", want, method.Request.Ref, reqRef)
		}
	}
	if reqRef == "" {
		return "", fmt.Errorf("no request schema found")
	}
	return reqRef, nil
}

func collectDiscoveryMethods(resources map[string]discoveryResource) map[string]discoveryMethod {
	out := make(map[string]discoveryMethod)
	var walk func(map[string]discoveryResource)
	walk = func(nodes map[string]discoveryResource) {
		for _, resource := range nodes {
			for name, method := range resource.Methods {
				out[name] = method
				if method.ID != "" {
					out[method.ID] = method
				}
			}
			walk(resource.Resources)
		}
	}
	walk(resources)
	return out
}

func discoveryToJSONSchema(all map[string]discoverySchema, rootRef string) (map[string]any, map[string]any, error) {
	b := discoveryBuilder{
		all:     all,
		defs:    make(map[string]any),
		visited: make(map[string]bool),
	}
	root, err := b.schemaFromRef(rootRef)
	if err != nil {
		return nil, nil, err
	}
	return root, b.defs, nil
}

type discoveryBuilder struct {
	all     map[string]discoverySchema
	defs    map[string]any
	visited map[string]bool
}

func (b discoveryBuilder) schemaFromRef(name string) (map[string]any, error) {
	schema, ok := b.all[name]
	if !ok {
		return nil, fmt.Errorf("schema %q not found", name)
	}
	if !b.visited[name] {
		b.visited[name] = true
		def, err := b.convert(schema)
		if err != nil {
			return nil, fmt.Errorf("schema %q: %w", name, err)
		}
		b.defs[name] = def
	}
	return map[string]any{"$ref": "#/$defs/" + name}, nil
}

func (b discoveryBuilder) convert(schema discoverySchema) (map[string]any, error) {
	if schema.Ref != "" {
		return b.schemaFromRef(schema.Ref)
	}

	out := make(map[string]any)
	if schema.Description != "" {
		out["description"] = schema.Description
	}
	if len(schema.Enum) > 0 {
		out["enum"] = slices.Clone(schema.Enum)
	}

	switch schemaType(schema) {
	case "object":
		out["type"] = "object"
		if len(schema.Required) > 0 {
			out["required"] = slices.Clone(schema.Required)
		}
		if len(schema.Properties) > 0 {
			props := make(map[string]any, len(schema.Properties))
			for name, child := range schema.Properties {
				normalized, err := b.convert(child)
				if err != nil {
					return nil, fmt.Errorf("property %q: %w", name, err)
				}
				props[name] = normalized
			}
			out["properties"] = props
		}
		if schema.AdditionalProperties != nil {
			normalized, err := b.convert(*schema.AdditionalProperties)
			if err != nil {
				return nil, fmt.Errorf("additionalProperties: %w", err)
			}
			out["additionalProperties"] = normalized
		}
	case "array":
		out["type"] = "array"
		if schema.Items == nil {
			return nil, fmt.Errorf("array schema missing items")
		}
		items, err := b.convert(*schema.Items)
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		out["items"] = items
	case "string", "integer", "number", "boolean":
		out["type"] = schemaType(schema)
	default:
		return nil, fmt.Errorf("unsupported discovery type %q", schema.Type)
	}

	return out, nil
}

func schemaType(schema discoverySchema) string {
	if schema.Type != "" {
		return schema.Type
	}
	if len(schema.Properties) > 0 || len(schema.Required) > 0 {
		return "object"
	}
	return ""
}
