package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type ChatCompletionRequest struct {
	Model         string                  `json:"model"`
	Messages      []ChatCompletionMessage `json:"messages"`
	Stream        bool                    `json:"stream,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Tools      []Tool          `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
}

type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatCompletionMessage struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
}

type MessageContent struct {
	raw  json.RawMessage
	text string
}

func (c *MessageContent) UnmarshalJSON(data []byte) error {
	c.raw = append(c.raw[:0], data...)
	c.text = ""

	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		c.text = text
		return nil
	}

	var parts []messageContentPart
	if err := json.Unmarshal(trimmed, &parts); err == nil {
		for _, part := range parts {
			if part.Type == "text" {
				c.text += part.Text
			}
		}
		return nil
	}

	return fmt.Errorf("content must be a string or an array of content parts")
}

func (c MessageContent) Text() string {
	return c.text
}

func (c MessageContent) RawJSON() json.RawMessage {
	return append(json.RawMessage(nil), c.raw...)
}

type messageContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
