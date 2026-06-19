package gemini

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ketang/zolem/internal/provider/backend"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// streamGenerateContent streams a response via ContentBackend.Stream, emitting
// Gemini-native GenerateContentResponse SSE events. It replaces both the old
// streamResponse (lorem/wasm path) and the old handleOllamaStream for live
// generation requests.
func streamGenerateContent(ctx context.Context, w http.ResponseWriter, cb backend.ContentBackend, req backend.GenerateRequest, model string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	completionTokens := 0
	err := cb.Stream(ctx, req, func(delta string) error {
		completionTokens++
		chunk := GenerateContentResponse{
			Candidates: []Candidate{{
				Content: Content{Parts: []Part{{Text: delta}}, Role: "model"},
				Index:   0,
			}},
			UsageMetadata: UsageMetadata{
				PromptTokenCount: promptTokens,
			},
			ModelVersion: model,
		}
		data, _ := json.Marshal(chunk)
		sse.WriteData(data)
		sse.Flush()
		return nil
	})

	if err != nil {
		errData, _ := json.Marshal(map[string]any{
			"error": map[string]any{"code": 502, "message": err.Error(), "status": "INTERNAL"},
		})
		sse.WriteData(errData)
		sse.Flush()
		return
	}

	finalChunk := GenerateContentResponse{
		Candidates: []Candidate{{
			Content:      Content{Parts: []Part{{Text: ""}}, Role: "model"},
			FinishReason: "STOP",
			Index:        0,
		}},
		UsageMetadata: UsageMetadata{
			PromptTokenCount:     promptTokens,
			CandidatesTokenCount: completionTokens,
			TotalTokenCount:      promptTokens + completionTokens,
		},
		ModelVersion: model,
	}
	data, _ := json.Marshal(finalChunk)
	sse.WriteData(data)
	sse.Flush()
}

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
