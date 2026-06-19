package anthropic

import (
	"bytes"
	"encoding/json"
	"strings"
)

type MessagesRequest struct {
	Model      string               `json:"model"`
	MaxTokens  int                  `json:"max_tokens"`
	Messages   []Message            `json:"messages"`
	System     MessageContent       `json:"system,omitempty"`
	Stream     bool                 `json:"stream,omitempty"`
	Tools      []AnthropicTool      `json:"tools,omitempty"`
	ToolChoice *AnthropicToolChoice `json:"tool_choice,omitempty"`
}

type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type AnthropicToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type Message struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
}

type MessageContent struct {
	Text   string
	Blocks []ContentBlock
}

func (c *MessageContent) UnmarshalJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		c.Text = text
		c.Blocks = nil
		return nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	c.Text = ""
	c.Blocks = blocks
	return nil
}

func (c MessageContent) PlainText() string {
	if c.Text != "" {
		return c.Text
	}
	var parts []string
	for _, block := range c.Blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, " ")
}

type MessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
