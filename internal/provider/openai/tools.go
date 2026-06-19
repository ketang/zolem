package openai

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

var toolCallCounter uint64

func newToolCallID() string {
	return "call_zolem_" + fmt.Sprintf("%016x", atomic.AddUint64(&toolCallCounter, 1))
}

// toolChoiceRequiresCall reports whether tool_choice mandates a function call.
// It also returns the specific function name requested, or "" for any function.
func toolChoiceRequiresCall(tc json.RawMessage) (required bool, name string) {
	if len(tc) == 0 {
		return false, ""
	}
	var s string
	if err := json.Unmarshal(tc, &s); err == nil {
		return s == "required", ""
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(tc, &obj); err == nil && obj.Type == "function" {
		return true, obj.Function.Name
	}
	return false, ""
}

// pickTool returns the tool to call. If name is non-empty, it finds that tool;
// otherwise it returns the first tool in the list.
func pickTool(tools []Tool, name string) *Tool {
	if len(tools) == 0 {
		return nil
	}
	if name == "" {
		return &tools[0]
	}
	for i := range tools {
		if tools[i].Function.Name == name {
			return &tools[i]
		}
	}
	return nil
}
