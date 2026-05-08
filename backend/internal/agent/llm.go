package agent

import "context"

// Role string constants. Identifies the speaker in a chat message.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one entry in a chat-completion request.
type Message struct {
	Role    string
	Content string
}

// Request is what `LLM.Generate` consumes.
// Typical LLM fields would land here:
// temperature, max_tokens, top_p, stop, response_format
type Request struct {
	Messages []Message
}

// Usage reports token accounting for one Generate call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// LLM is the provider-agnostic chat interface. The production
// implementation is openAICompatLLM (see openai_compat.go).
//
// A zero Usage means the provider did not report usage for the call.
type LLM interface {
	Name() string
	Generate(ctx context.Context, req Request) (text string, usage Usage, err error)
}
