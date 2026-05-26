package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"zolem.dev/zolem/internal/fixture"
	runtimecfg "zolem.dev/zolem/internal/runtime"
)

var responsesUpgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

type responsesWSFrame struct {
	Type     string `json:"type"`
	Generate *bool  `json:"generate"`
}

func (h *Handler) handleResponses(w http.ResponseWriter, r *http.Request) {
	if writeForcedProfileError(r.Context(), w) {
		return
	}
	if !validResponsesAuthorization(r.Header.Get("Authorization")) {
		writePermissionDenied(w)
		return
	}

	conn, err := responsesUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	runtimecfg.MarkWebSocketUpgraded(r.Context())
	defer conn.Close()

	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		runtimecfg.RecordWebSocketFrameReceived(r.Context())

		var frame responsesWSFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			continue
		}
		if frame.Type != "response.create" {
			continue
		}

		var events []json.RawMessage
		if frame.Generate != nil && !*frame.Generate {
			events = prewarmResponseEvents()
		} else {
			events, err = h.responseCreateEvents(r.Context(), payload)
			if err != nil {
				events = responseFailedEvents(err.Error())
			}
		}
		if err := writeResponseEvents(r.Context(), conn, events); err != nil {
			return
		}
	}
}

func validResponsesAuthorization(auth string) bool {
	return strings.TrimSpace(auth) == "Bearer sk-test"
}

func (h *Handler) responseCreateEvents(ctx context.Context, payload []byte) ([]json.RawMessage, error) {
	if runtimecfg.UsesFixtures(ctx) {
		matchReq := fixture.MatchRequest{
			Provider: "openai", Version: "v1-responses",
			Labels: labelsFromContext(ctx),
			Body:   json.RawMessage(payload),
		}
		matched, _ := h.matcher.Match(ctx, matchReq)
		if matched != nil {
			body, err := renderFixtureBodyBytes(ctx, matched)
			if err != nil {
				return nil, err
			}
			var events []json.RawMessage
			if err := json.Unmarshal(body, &events); err != nil {
				return nil, fmt.Errorf("fixture %q response must be a JSON array of Responses API events: %w", matched.ID, err)
			}
			return events, nil
		}
	}

	tokens := h.generator.Generate(30)
	return textResponseEvents(strings.Join(tokens, "")), nil
}

func writeResponseEvents(ctx context.Context, conn *websocket.Conn, events []json.RawMessage) error {
	for _, event := range events {
		if err := conn.WriteMessage(websocket.TextMessage, event); err != nil {
			return err
		}
		runtimecfg.RecordWebSocketFrameSent(ctx)
	}
	return nil
}

func prewarmResponseEvents() []json.RawMessage {
	return marshalResponseEvents([]map[string]any{
		{
			"type":            "response.created",
			"sequence_number": 0,
			"response": map[string]any{
				"id":     "resp_prewarm",
				"status": "in_progress",
				"output": []any{},
			},
		},
		{
			"type":            "response.completed",
			"sequence_number": 1,
			"response": map[string]any{
				"id":     "resp_prewarm",
				"status": "completed",
				"output": []any{},
			},
		},
	})
}

func textResponseEvents(text string) []json.RawMessage {
	responseID := fmt.Sprintf("resp_zolem%d", time.Now().UnixNano())
	messageID := fmt.Sprintf("msg_zolem%d", time.Now().UnixNano())
	message := map[string]any{
		"type":   "message",
		"id":     messageID,
		"role":   "assistant",
		"status": "completed",
		"content": []any{
			map[string]any{"type": "output_text", "text": text},
		},
	}
	return marshalResponseEvents([]map[string]any{
		{
			"type":            "response.created",
			"sequence_number": 0,
			"response": map[string]any{
				"id":     responseID,
				"status": "in_progress",
				"output": []any{},
			},
		},
		{
			"type":            "response.output_item.added",
			"sequence_number": 1,
			"output_index":    0,
			"item": map[string]any{
				"type":    "message",
				"id":      messageID,
				"role":    "assistant",
				"content": []any{},
				"status":  "in_progress",
			},
		},
		{
			"type":            "response.output_text.delta",
			"sequence_number": 2,
			"item_id":         messageID,
			"output_index":    0,
			"content_index":   0,
			"delta":           text,
		},
		{
			"type":            "response.output_text.done",
			"sequence_number": 3,
			"item_id":         messageID,
			"output_index":    0,
			"content_index":   0,
			"text":            text,
		},
		{
			"type":            "response.output_item.done",
			"sequence_number": 4,
			"output_index":    0,
			"item":            message,
		},
		{
			"type":            "response.completed",
			"sequence_number": 5,
			"response": map[string]any{
				"id":     responseID,
				"status": "completed",
				"output": []any{message},
			},
		},
	})
}

func responseFailedEvents(message string) []json.RawMessage {
	responseID := fmt.Sprintf("resp_zolem%d", time.Now().UnixNano())
	return marshalResponseEvents([]map[string]any{
		{
			"type":            "response.failed",
			"sequence_number": 0,
			"response": map[string]any{
				"id":     responseID,
				"status": "failed",
				"error": map[string]any{
					"type":    "server_error",
					"message": message,
				},
				"output": []any{},
			},
		},
	})
}

func marshalResponseEvents(events []map[string]any) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		data, _ := json.Marshal(event)
		out = append(out, data)
	}
	return out
}
