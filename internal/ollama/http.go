package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatCompletionChoice struct {
	Message ChatMessage `json:"message"`
}

type chatCompletionResponse struct {
	Choices []chatCompletionChoice `json:"choices"`
}

// HTTPChatCompletion sends a non-streaming chat completion request to the
// Ollama-compatible HTTP API at upstream and returns the assistant's reply.
func HTTPChatCompletion(ctx context.Context, upstream string, messages []ChatMessage, model string) (string, error) {
	reqBody := chatCompletionRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ollama backend unavailable: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama backend error (HTTP %d): %s", resp.StatusCode, body)
	}

	var result chatCompletionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("ollama backend returned unparseable response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("ollama backend returned empty response")
	}

	return result.Choices[0].Message.Content, nil
}
