package anthropic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"

	"zolem.dev/zolem/internal/response"
)

func streamResponse(w http.ResponseWriter, model string, tokens []string, inputTokens int) {
	sse := response.NewSSEWriter(w)
	sse.SetHeaders()

	msgID := "msg_zolem_" + fmt.Sprintf("%016x", pseudoRandID())

	msgStart, _ := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []any{}, "model": model,
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": inputTokens, "output_tokens": 1},
		},
	})
	sse.WriteEvent("message_start", msgStart)
	sse.Flush()

	cbStart, _ := json.Marshal(map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]string{"type": "text", "text": ""},
	})
	sse.WriteEvent("content_block_start", cbStart)
	sse.Flush()

	sse.WriteEvent("ping", []byte(`{"type":"ping"}`))
	sse.Flush()

	for _, tok := range tokens {
		delta, _ := json.Marshal(map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]string{"type": "text_delta", "text": tok},
		})
		sse.WriteEvent("content_block_delta", delta)
		sse.Flush()
	}

	sse.WriteEvent("content_block_stop", []byte(`{"type":"content_block_stop","index":0}`))
	sse.Flush()

	msgDelta, _ := json.Marshal(map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]int{"output_tokens": len(tokens)},
	})
	sse.WriteEvent("message_delta", msgDelta)
	sse.Flush()

	sse.WriteEvent("message_stop", []byte(`{"type":"message_stop"}`))
	sse.Flush()
}

var pseudoCounter uint64

func pseudoRandID() uint64 {
	return atomic.AddUint64(&pseudoCounter, 1)
}
