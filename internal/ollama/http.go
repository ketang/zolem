package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

type streamDelta struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// HTTPChatCompletionStream sends a streaming chat completion request and
// invokes fn for each content delta. Returns when the stream ends or on error.
func HTTPChatCompletionStream(ctx context.Context, upstream string, messages []ChatMessage, model string, fn func(delta string) error) error {
	reqBody := chatCompletionRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("ollama backend unavailable: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("ollama backend unavailable: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama backend unavailable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama backend error (HTTP %d): %s", resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var delta streamDelta
		if err := json.Unmarshal([]byte(payload), &delta); err != nil {
			continue
		}
		if len(delta.Choices) == 0 {
			continue
		}
		content := delta.Choices[0].Delta.Content
		if content == "" {
			continue
		}
		if err := fn(content); err != nil {
			return err
		}
	}

	return scanner.Err()
}
