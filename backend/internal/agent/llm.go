package agent

import "context"

const (
	roleSystem    = "system"
	roleUser      = "user"
	roleAssistant = "assistant"
	roleTool      = "tool"
)

type message struct {
	role    string
	content string
}

type request struct {
	messages            []message
	responseFormat      *JSONSchema
	maxCompletionTokens int
}

// JSONSchema describes a structured-output constraint. When attached to a
// Config, the provider constrains output to the schema (OpenAI/OpenRouter:
// response_format=json_schema, strict=true).
type JSONSchema struct {
	Name   string         // [a-zA-Z0-9_-]{1,64}
	Schema map[string]any // the JSON Schema document
}

// Usage reports token and cost accounting for one Run call. A zero Usage means the
// provider did not report usage.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// llm is the provider-agnostic chat interface. The production implementation
// is openAICompatLLM (see openai_compat.go).
type llm interface {
	name() string
	generate(ctx context.Context, req request, onToken func(string)) (string, Usage, error)
}
