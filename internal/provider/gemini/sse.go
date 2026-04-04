package gemini

import (
	"encoding/json"
	"net/http"

	"zolem.dev/zolem/internal/response"
)

func streamResponse(w http.ResponseWriter, model string, tokens []string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	for i, tok := range tokens {
		isLast := i == len(tokens)-1
		finishReason := "NONE"
		candidateTokenCount := 0
		if isLast {
			finishReason = "STOP"
			candidateTokenCount = len(tokens)
		}

		chunk := GenerateContentResponse{
			Candidates: []Candidate{{
				Content: Content{
					Parts: []Part{{Text: tok}},
					Role:  "model",
				},
				FinishReason: finishReason,
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount:     promptTokens,
				CandidatesTokenCount: candidateTokenCount,
				TotalTokenCount:      promptTokens + candidateTokenCount,
			},
			ModelVersion: model,
		}

		data, _ := json.Marshal(chunk)
		sse.WriteData(data)
		sse.Flush()
	}
}
