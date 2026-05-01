package agent

import (
	"context"
	"encoding/json"
)

// Role string constants. Identifies the speaker in a chat message.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one entry in a chat-completion request. ToolCalls and
// ToolCallID are populated only for assistant messages with function
// calls and for tool-result messages respectively. Both are unused by
// today's debate pipeline; the fields are reserved for the RAG
// follow-up (see ADR 0008 §3).
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// Tool describes a function the model may call. Schema is a JSON
// Schema document for the function's parameters.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ToolCall is a single function invocation requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// Request is what `LLM.Generate` consumes.
type Request struct {
	Model    string
	Messages []Message
	Tools    []Tool
}

// Usage reports token accounting for one Generate call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Chunk is one streamed delta. Usage is set on the chunk that carries
// the final usage report (typically the terminal chunk).
type Chunk struct {
	Delta         string
	ToolCallDelta *ToolCall
	Usage         Usage
}

// LLM is the provider-agnostic streaming chat interface. Implementations:
//   - openAICompatLLM (production, see openai_compat.go)
//   - gibberish.Model (local mock, see internal/gibberish)
//
// A zero Usage means the provider did not report usage for the call.
type LLM interface {
	Name() string
	Generate(
		ctx context.Context,
		req Request,
		onChunk func(Chunk) error,
	) (text string, toolCalls []ToolCall, usage Usage, err error)
}
