package specs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
)

// NormalizeOpenAPI extracts the supported request schema from an upstream
// OpenAPI document and converts it into the JSON Schema shape expected by the
// validator.
func NormalizeOpenAPI(provider, version string, data []byte) ([]byte, error) {
	doc, err := loadOpenAPIDocument(data)
	if err != nil {
		return nil, fmt.Errorf("parse openapi document for %s/%s: %w", provider, version, err)
	}

	schemaRef, err := lookupRequestSchema(doc, provider, version)
	if err != nil {
		return nil, fmt.Errorf("extract openapi request schema for %s/%s: %w", provider, version, err)
	}

	normalized, err := normalizeSchemaRef(schemaRef)
	if err != nil {
		return nil, fmt.Errorf("normalize openapi schema for %s/%s: %w", provider, version, err)
	}

	normalized["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	payload, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized schema for %s/%s: %w", provider, version, err)
	}
	return payload, nil
}

func loadOpenAPIDocument(raw []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(raw)
	if err != nil {
		return nil, err
	}
	if err := doc.Validate(context.Background()); err != nil {
		return nil, err
	}
	return doc, nil
}

func lookupRequestSchema(doc *openapi3.T, provider, version string) (*openapi3.SchemaRef, error) {
	path, method, err := supportedOpenAPIOperation(provider, version)
	if err != nil {
		return nil, err
	}

	pathItem := doc.Paths.Value(path)
	if pathItem == nil {
		return nil, fmt.Errorf("operation not found: %s %s", method, path)
	}

	operation := pathItem.GetOperation(method)
	if operation == nil {
		return nil, fmt.Errorf("operation not found: %s %s", method, path)
	}
	if operation.RequestBody == nil || operation.RequestBody.Value == nil {
		return nil, fmt.Errorf("request body not found: %s %s", method, path)
	}

	content := operation.RequestBody.Value.Content
	if mediaType := content.Get("application/json"); mediaType != nil && mediaType.Schema != nil {
		return mediaType.Schema, nil
	}
	return nil, fmt.Errorf("application/json request body schema not found: %s %s", method, path)
}

func supportedOpenAPIOperation(provider, version string) (path string, method string, err error) {
	switch provider + ":" + version {
	case "openai:v1", "openrouter:v1":
		return "/v1/chat/completions", "POST", nil
	default:
		return "", "", fmt.Errorf("no supported openapi operation configured for %s:%s", provider, version)
	}
}

func normalizeSchemaRef(ref *openapi3.SchemaRef) (map[string]any, error) {
	if ref == nil {
		return nil, fmt.Errorf("schema ref is nil")
	}
	if ref.Value == nil {
		return nil, fmt.Errorf("schema ref has no value")
	}

	schema := ref.Value
	result := map[string]any{}

	if schema.Type != nil {
		types := schema.Type.Slice()
		switch {
		case schema.Nullable && len(types) == 0:
			result["type"] = []string{"null"}
		case schema.Nullable && len(types) == 1:
			result["type"] = []string{types[0], "null"}
		case schema.Nullable:
			result["type"] = append(append([]string{}, types...), "null")
		case len(types) == 1:
			result["type"] = types[0]
		case len(types) > 1:
			result["type"] = append([]string{}, types...)
		}
	}

	if schema.Title != "" {
		result["title"] = schema.Title
	}
	if schema.Description != "" {
		result["description"] = schema.Description
	}
	if schema.Format != "" {
		result["format"] = schema.Format
	}
	if schema.Default != nil {
		result["default"] = schema.Default
	}
	if len(schema.Enum) > 0 {
		result["enum"] = append([]any(nil), schema.Enum...)
	}
	if len(schema.Required) > 0 {
		result["required"] = append([]string(nil), schema.Required...)
	}
	if schema.Min != nil {
		result["minimum"] = *schema.Min
	}
	if schema.Max != nil {
		result["maximum"] = *schema.Max
	}
	if schema.ExclusiveMin {
		result["exclusiveMinimum"] = true
	}
	if schema.ExclusiveMax {
		result["exclusiveMaximum"] = true
	}
	if schema.MultipleOf != nil {
		result["multipleOf"] = *schema.MultipleOf
	}
	if schema.MinLength != 0 {
		result["minLength"] = schema.MinLength
	}
	if schema.MaxLength != nil {
		result["maxLength"] = *schema.MaxLength
	}
	if schema.Pattern != "" {
		result["pattern"] = schema.Pattern
	}
	if schema.MinItems != 0 {
		result["minItems"] = schema.MinItems
	}
	if schema.MaxItems != nil {
		result["maxItems"] = *schema.MaxItems
	}
	if schema.UniqueItems {
		result["uniqueItems"] = true
	}
	if schema.MinProps != 0 {
		result["minProperties"] = schema.MinProps
	}
	if schema.MaxProps != nil {
		result["maxProperties"] = *schema.MaxProps
	}

	if schema.Items != nil {
		items, err := normalizeSchemaRef(schema.Items)
		if err != nil {
			return nil, fmt.Errorf("normalize items: %w", err)
		}
		result["items"] = items
	}

	if len(schema.Properties) > 0 {
		properties := make(map[string]any, len(schema.Properties))
		for name, propertyRef := range schema.Properties {
			property, err := normalizeSchemaRef(propertyRef)
			if err != nil {
				return nil, fmt.Errorf("normalize property %q: %w", name, err)
			}
			properties[name] = property
		}
		result["properties"] = properties
	}

	if schema.AdditionalProperties.Has != nil && !*schema.AdditionalProperties.Has {
		result["additionalProperties"] = false
	} else if schema.AdditionalProperties.Schema != nil {
		additional, err := normalizeSchemaRef(schema.AdditionalProperties.Schema)
		if err != nil {
			return nil, fmt.Errorf("normalize additionalProperties: %w", err)
		}
		result["additionalProperties"] = additional
	}

	if len(schema.AllOf) > 0 {
		allOf := make([]any, 0, len(schema.AllOf))
		for _, child := range schema.AllOf {
			value, err := normalizeSchemaRef(child)
			if err != nil {
				return nil, fmt.Errorf("normalize allOf: %w", err)
			}
			allOf = append(allOf, value)
		}
		result["allOf"] = allOf
	}
	if len(schema.AnyOf) > 0 {
		anyOf := make([]any, 0, len(schema.AnyOf))
		for _, child := range schema.AnyOf {
			value, err := normalizeSchemaRef(child)
			if err != nil {
				return nil, fmt.Errorf("normalize anyOf: %w", err)
			}
			anyOf = append(anyOf, value)
		}
		result["anyOf"] = anyOf
	}
	if len(schema.OneOf) > 0 {
		oneOf := make([]any, 0, len(schema.OneOf))
		for _, child := range schema.OneOf {
			value, err := normalizeSchemaRef(child)
			if err != nil {
				return nil, fmt.Errorf("normalize oneOf: %w", err)
			}
			oneOf = append(oneOf, value)
		}
		result["oneOf"] = oneOf
	}
	if schema.Not != nil {
		notValue, err := normalizeSchemaRef(schema.Not)
		if err != nil {
			return nil, fmt.Errorf("normalize not: %w", err)
		}
		result["not"] = notValue
	}

	return result, nil
}
