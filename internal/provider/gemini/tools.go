package gemini

// geminiToolCallRequired reports whether the request's ToolConfig mandates a
// function call (mode == "ANY"). Returns the first matching FunctionDeclaration
// to call, or nil if no call is required.
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
