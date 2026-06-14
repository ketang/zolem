package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"zolem.dev/zolem/internal/response"
	runtimecfg "zolem.dev/zolem/internal/runtime"
)

func streamResponse(ctx context.Context, w http.ResponseWriter, model string, tokens []string, promptTokens int, includeUsage bool) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()
	delay := runtimecfg.StreamDelayForRequest(ctx)

	id := fmt.Sprintf("chatcmpl-zolem%d", time.Now().UnixNano())
	created := time.Now().Unix()

	firstChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]string{"role": "assistant", "content": ""}, "finish_reason": nil}},
	}
	data, _ := json.Marshal(firstChunk)
	sse.WriteData(data)
	sse.Flush()

	for _, tok := range tokens {
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": tok}, "finish_reason": nil}},
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

	stop := "stop"
	finalChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": stop}},
	}
	data, _ = json.Marshal(finalChunk)
	sse.WriteData(data)
	sse.Flush()

	if includeUsage {
		usageChunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{},
			"usage": map[string]int{
				"prompt_tokens": promptTokens, "completion_tokens": response.CountNonEmpty(tokens),
				"total_tokens": promptTokens + response.CountNonEmpty(tokens),
			},
		}
		data, _ = json.Marshal(usageChunk)
		sse.WriteData(data)
		sse.Flush()
	}

	sse.WriteData([]byte("[DONE]"))
	sse.Flush()
}
