package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/h-hmz/logos/internal/config"
)

// openAICompatLLM implements LLM against any OpenAI-compatible chat
// completions endpoint. Base URL is configurable via LLM_BASE_URL
// (defaults to OpenRouter — see ADR 0007 for the cost-cap rationale).
// The same client is reused for every Generate call.
type openAICompatLLM struct {
	client openai.Client
	name   string
}

func newOpenAICompatLLM(modelID string) (*openAICompatLLM, error) {
	apiKey := config.LLMAPIKey()
	if apiKey == "" {
		return nil, errors.New("agent: LLM_API_KEY not set")
	}
	c := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(config.LLMBaseURL()),
		// OpenRouter-specific attribution headers; harmless for other
		// OpenAI-compatible providers (they ignore unknown headers).
		option.WithHeader("HTTP-Referer", "https://github.com/h-hmz/logos"),
		option.WithHeader("X-Title", "Logos"),
	)
	return &openAICompatLLM{client: c, name: modelID}, nil
}

func (m *openAICompatLLM) Name() string { return m.name }

// Generate streams a chat completion. Retries up to twice on transient
// failures (5xx + 429) with a 250ms delay; non-retryable errors return
// immediately.
func (m *openAICompatLLM) Generate(
	ctx context.Context,
	req Request,
	onChunk func(Chunk) error,
) (string, []ToolCall, Usage, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		text, calls, usage, err := m.generateOnce(ctx, req, onChunk)
		if err == nil {
			return text, calls, usage, nil
		}
		if !isRetryable(err) || attempt == maxAttempts {
			return "", nil, Usage{}, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", nil, Usage{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return "", nil, Usage{}, lastErr
}

func (m *openAICompatLLM) generateOnce(
	ctx context.Context,
	req Request,
	onChunk func(Chunk) error,
) (string, []ToolCall, Usage, error) {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, mm := range req.Messages {
		switch mm.Role {
		case RoleSystem:
			msgs = append(msgs, openai.SystemMessage(mm.Content))
		case RoleUser:
			msgs = append(msgs, openai.UserMessage(mm.Content))
		case RoleAssistant:
			msgs = append(msgs, openai.AssistantMessage(mm.Content))
		case RoleTool:
			msgs = append(msgs, openai.ToolMessage(mm.Content, mm.ToolCallID))
		default:
			return "", nil, Usage{}, fmt.Errorf("agent: unknown role %q", mm.Role)
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: msgs,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	// Tools wiring is reserved for the RAG follow-up; today's pipeline
	// never sets req.Tools, so this branch stays cold.
	// (See ADR 0008 §3.)

	stream := m.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var sb []byte
	var usage Usage
	for stream.Next() {
		ev := stream.Current()
		if len(ev.Choices) > 0 {
			delta := ev.Choices[0].Delta.Content
			if delta != "" {
				sb = append(sb, delta...)
				if onChunk != nil {
					if cerr := onChunk(Chunk{Delta: delta}); cerr != nil {
						return "", nil, Usage{}, cerr
					}
				}
			}
		}
		if ev.Usage.PromptTokens != 0 || ev.Usage.CompletionTokens != 0 {
			usage = Usage{
				InputTokens:  int(ev.Usage.PromptTokens),
				OutputTokens: int(ev.Usage.CompletionTokens),
			}
		}
	}
	if err := stream.Err(); err != nil {
		return "", nil, Usage{}, fmt.Errorf("openai-compat: stream: %w", err)
	}
	return string(sb), nil, usage, nil
}

// isRetryable reports whether an openai-go error should be retried.
// Transient: 5xx upstream errors, 429 rate limiting. Non-transient:
// 4xx (auth, payment, request shape).
func isRetryable(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 429 || apiErr.StatusCode >= 500
	}
	return false
}
