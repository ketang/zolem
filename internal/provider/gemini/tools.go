package gemini

// geminiToolCallRequired reports whether the request's ToolConfig mandates a
// function call (mode == "ANY"). Returns the first matching FunctionDeclaration
// to call, or nil if no call is required.
//
// Only mode "ANY" synthesizes a function call. Modes "AUTO" (let the model
// decide) and "NONE" return nil and fall through to the lorem/backend text
// path: the local runtime does not run a model, so it cannot decide to call a
// function on the request's behalf. An SDK that expects a function call in
// AUTO mode will therefore receive a text response. See
// TestFunctionCallModeAUTO_ReturnsText.
func geminiToolCallRequired(req GenerateContentRequest) *FunctionDeclaration {
	if req.ToolConfig == nil || req.ToolConfig.FunctionCallingConfig == nil {
		return nil
	}
	if req.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		return nil
	}
	allowed := req.ToolConfig.FunctionCallingConfig.AllowedFunctionNames
	for i := range req.Tools {
		for j := range req.Tools[i].FunctionDeclarations {
			fd := &req.Tools[i].FunctionDeclarations[j]
			if len(allowed) == 0 || containsString(allowed, fd.Name) {
				return fd
			}
		}
	}
	return nil
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
