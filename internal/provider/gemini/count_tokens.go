package gemini

import (
	"encoding/json"
	"io"
	"net/http"
)

// countTokensRequest accepts both the bare `{"contents":[...]}` form and the
// `{"generateContentRequest":{...}}` wrapper the Gemini SDKs use for
// :countTokens.
type countTokensRequest struct {
	Contents               []Content               `json:"contents"`
	GenerateContentRequest *GenerateContentRequest `json:"generateContentRequest"`
}

type countTokensResponse struct {
	TotalTokens int `json:"totalTokens"`
}

// handleCountTokens serves the :countTokens action, returning the same
// deterministic token estimate the generateContent handler reports as
// promptTokenCount.
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}

	if r.Header.Get("x-goog-api-key") == "" && r.URL.Query().Get("key") == "" {
		writeForbidden(r.Context(), w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeInvalidRequest(r.Context(), w, "failed to read request body")
		return
	}

	var raw countTokensRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		writeInvalidRequest(r.Context(), w, "invalid JSON: "+err.Error())
		return
	}

	req := GenerateContentRequest{Contents: raw.Contents}
	if len(req.Contents) == 0 && raw.GenerateContentRequest != nil {
		req.Contents = raw.GenerateContentRequest.Contents
	}
	if len(req.Contents) == 0 {
		writeInvalidRequest(r.Context(), w, "contents is required")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(countTokensResponse{TotalTokens: estimatePromptTokens(req)})
}
