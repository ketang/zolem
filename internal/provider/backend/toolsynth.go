package backend

import "encoding/json"

// SynthArgs generates fake JSON arguments that conform to a JSON Schema.
// It fills required properties (or all properties when required is absent)
// using constant leaf values by type: string→"lorem ipsum", number/integer→42,
// boolean→true, array→[], object→recurses. Returns "{}" for non-object or
// unrecognised schemas.
func SynthArgs(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return json.RawMessage("{}")
	}
	var s struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return json.RawMessage("{}")
	}
	if s.Type != "" && s.Type != "object" {
		return json.RawMessage("{}")
	}

	keys := s.Required
	if len(keys) == 0 {
		for k := range s.Properties {
			keys = append(keys, k)
		}
	}

	result := make(map[string]any, len(keys))
	for _, k := range keys {
		propSchema, ok := s.Properties[k]
		if !ok {
			result[k] = "lorem ipsum"
			continue
		}
		result[k] = synthLeaf(propSchema)
	}
	out, _ := json.Marshal(result)
	return out
}

func synthLeaf(schema json.RawMessage) any {
	var s struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return "lorem ipsum"
	}
	switch s.Type {
	case "string":
		return "lorem ipsum"
	case "number", "integer":
		return 42
	case "boolean":
		return true
	case "array":
		return []any{}
	case "object":
		keys := s.Required
		if len(keys) == 0 {
			for k := range s.Properties {
				keys = append(keys, k)
			}
		}
		result := make(map[string]any, len(keys))
		for _, k := range keys {
			propSchema, ok := s.Properties[k]
			if !ok {
				result[k] = "lorem ipsum"
				continue
			}
			result[k] = synthLeaf(propSchema)
		}
		return result
	default:
		return "lorem ipsum"
	}
}
