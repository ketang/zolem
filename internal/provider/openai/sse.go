package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ketang/zolem/internal/provider/backend"
	"github.com/ketang/zolem/internal/response"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// streamChatCompletions streams a response via ContentBackend.Stream, emitting
// OpenAI chat.completion.chunk SSE events. It replaces both the old
// streamResponse (lorem/wasm path) and the old handleOllamaStream for live
// generation requests.
func streamChatCompletions(ctx context.Context, w http.ResponseWriter, cb backend.ContentBackend, req backend.GenerateRequest, model string, promptTokens int, includeUsage bool) {
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

	completionTokens := 0
	err := cb.Stream(ctx, req, func(delta string) error {
		completionTokens++
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": delta}, "finish_reason": nil}},
		}
		d, _ := json.Marshal(chunk)
		sse.WriteData(d)
		sse.Flush()
		return nil
	})

	if err != nil {
		errChunk := map[string]any{"error": map[string]string{"message": err.Error(), "type": "server_error"}}
		d, _ := json.Marshal(errChunk)
		sse.WriteData(d)
		sse.Flush()
		return
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
				"prompt_tokens": promptTokens, "completion_tokens": completionTokens,
				"total_tokens": promptTokens + completionTokens,
			},
		}
		data, _ = json.Marshal(usageChunk)
		sse.WriteData(data)
		sse.Flush()
	}

	sse.WriteData([]byte("[DONE]"))
	sse.Flush()
}

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
