package ollama

import "encoding/json"

// ChatRequest is the native POST /api/chat request body.
//
// Field notes that differ from the OpenAI-compatible surface:
//   - Stream is a *bool because Ollama defaults it to TRUE when absent, which
//     is the opposite of Go's bool zero value. Resolve it with streamRequested.
//   - Content on a message is always a plain string, never an array of parts.
//   - Think is tri-state: absent is distinct from false.
type ChatRequest struct {
	Model    string          `json:"model"`
	Messages []Message       `json:"messages"`
	Tools    []Tool          `json:"tools,omitempty"`
	Stream   *bool           `json:"stream,omitempty"`
	Format   json.RawMessage `json:"format,omitempty"`
	Options  json.RawMessage `json:"options,omitempty"`
	Think    json.RawMessage `json:"think,omitempty"`
}

// streamRequested reports whether the response should be streamed.
//
// Ollama's /api/chat defaults stream to true when the field is absent. This is
// the opposite of OpenAI's chat-completions API and the opposite of Go's bool
// zero value, so the default lives in exactly one place.
func streamRequested(req ChatRequest) bool {
	return req.Stream == nil || *req.Stream
}

// Message is a single chat message. Roles are system, user, assistant, and
// tool. Images are bare base64 strings with no data: URI prefix.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	Images    []string   `json:"images,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

// Tool is an OpenAI-shaped function declaration.
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall is a returned tool invocation.
//
// Arguments is a json.RawMessage holding a real JSON *object* — unlike OpenAI,
// which encodes arguments as a JSON string. Callers must not string-encode it.
type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ChatResponse is one native /api/chat response object. In streaming mode each
// NDJSON line is one of these; only the final object (Done true) carries the
// metrics block, which is why every metric field is omitempty.
type ChatResponse struct {
	Model      string  `json:"model"`
	CreatedAt  string  `json:"created_at"`
	Message    Message `json:"message"`
	Done       bool    `json:"done"`
	DoneReason string  `json:"done_reason,omitempty"`

	// Durations are nanoseconds, matching Ollama's native unit.
	TotalDuration      int64 `json:"total_duration,omitempty"`
	LoadDuration       int64 `json:"load_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}
