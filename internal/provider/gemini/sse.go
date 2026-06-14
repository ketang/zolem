package gemini

import (
	"context"
	"encoding/json"
	"net/http"

	"zolem.dev/zolem/internal/response"
	runtimecfg "zolem.dev/zolem/internal/runtime"
)

func streamResponse(ctx context.Context, w http.ResponseWriter, model string, tokens []string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()
	delay := runtimecfg.StreamDelayForRequest(ctx)

	if len(tokens) == 0 {
		chunk := GenerateContentResponse{
			Candidates: []Candidate{{
				Content:      Content{Parts: []Part{{Text: ""}}, Role: "model"},
				FinishReason: "STOP",
				Index:        0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount: promptTokens,
				TotalTokenCount:  promptTokens,
			},
			ModelVersion: model,
		}
		data, _ := json.Marshal(chunk)
		sse.WriteData(data)
		sse.Flush()
		return
	}

	for i, tok := range tokens {
		isLast := i == len(tokens)-1
		finishReason := ""
		candidateTokenCount := 0
		if isLast {
			finishReason = "STOP"
			candidateTokenCount = response.CountNonEmpty(tokens)
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
		if delay != nil {
			if err := delay(ctx); err != nil {
				return
			}
		}
	}
}
