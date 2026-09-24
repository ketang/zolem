package typesafe

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// choiceOptions returns a choice question's option names in the order they
// appear in the request JSON. Request-schema validation already guarantees
// criteria is a JSON object with 2-255 string-valued keys by the time this is
// called from the handler, but callers reaching this from a fixture-only path
// still get a real error rather than a panic on malformed input.
//
// Order matters for the lorem backend, which favors "the first option" — a
// notion that only exists if key order is preserved, which a plain
// map[string]string unmarshal would lose.
func choiceOptions(criteria json.RawMessage) ([]string, error) {
	keys, err := orderedObjectKeys(criteria)
	if err != nil {
		return nil, fmt.Errorf("choice criteria must be a JSON object: %w", err)
	}
	if len(keys) < 2 {
		return nil, fmt.Errorf("choice criteria must have at least 2 options")
	}
	return keys, nil
}

// scoreLevels returns a score question's level descriptions in array order.
// Each element is either a JSON string or a {"what":..., "examples":...}
// object, per the confirmed contract.
func scoreLevels(criteria json.RawMessage) ([]json.RawMessage, error) {
	var levels []json.RawMessage
	if err := json.Unmarshal(criteria, &levels); err != nil {
		return nil, fmt.Errorf("score criteria must be a JSON array: %w", err)
	}
	if len(levels) < 2 || len(levels) > 10 {
		return nil, fmt.Errorf("score criteria must have between 2 and 10 levels")
	}
	return levels, nil
}

// levelLabel renders a single score level description as display text, for
// the legend field of a synthesized answer.
func levelLabel(level json.RawMessage) string {
	var s string
	if err := json.Unmarshal(level, &s); err == nil {
		return s
	}
	var obj struct {
		What string `json:"what"`
	}
	if err := json.Unmarshal(level, &obj); err == nil && obj.What != "" {
		return obj.What
	}
	return string(level)
}

// orderedObjectKeys returns a JSON object's top-level keys in source order.
// encoding/json's map decoding loses key order, which matters here because
// "the first option" (lorem backend) and "the first level" are meaningful
// only if original order is preserved.
func orderedObjectKeys(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}

	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected a string object key")
		}
		keys = append(keys, key)

		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}
