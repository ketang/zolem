package anthropic

// anthropicToolChoiceRequiresCall reports whether tool_choice mandates a call.
// It returns the specific tool name requested, or "" for any tool.
func anthropicToolChoiceRequiresCall(tc *AnthropicToolChoice) (required bool, name string) {
	if tc == nil {
		return false, ""
	}
	switch tc.Type {
	case "any":
		return true, ""
	case "tool":
		return true, tc.Name
	default:
		return false, ""
	}
}

// pickAnthropicTool returns the tool to call. If name is non-empty, it finds
// that specific tool; otherwise it returns the first tool in the list.
func pickAnthropicTool(tools []AnthropicTool, name string) *AnthropicTool {
	if len(tools) == 0 {
		return nil
	}
	if name == "" {
		return &tools[0]
	}
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}
