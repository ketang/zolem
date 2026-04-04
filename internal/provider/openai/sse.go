package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"zolem.dev/zolem/internal/response"
)

func streamResponse(w http.ResponseWriter, model string, tokens []string, promptTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

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
	}

	stop := "stop"
	finalChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": stop}},
	}
	data, _ = json.Marshal(finalChunk)
	sse.WriteData(data)
	sse.Flush()

	usageChunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{},
		"usage": map[string]int{
			"prompt_tokens": promptTokens, "completion_tokens": len(tokens),
			"total_tokens": promptTokens + len(tokens),
		},
	}
	data, _ = json.Marshal(usageChunk)
	sse.WriteData(data)
	sse.Flush()

	sse.WriteData([]byte("[DONE]"))
	sse.Flush()
}
