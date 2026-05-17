package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"

	"github.com/h-hmz/logos/internal/config"
)

// openAICompatLLM implements llm against any OpenAI-compatible chat
// completions endpoint. Base URL is configurable via LLM_BASE_URL
// (defaults to OpenRouter — see ADR 0007 for the cost-cap rationale).
// The same client is reused for every generate call.
type openAICompatLLM struct {
	client openai.Client
	id     string
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
	return &openAICompatLLM{client: c, id: modelID}, nil
}

func (m *openAICompatLLM) name() string { return m.id }

// generate runs a chat completion. Retries up to twice on transient failures
// (5xx + 429) with a 250ms delay; non-retryable errors return immediately.
func (m *openAICompatLLM) generate(ctx context.Context, req request, onToken func(string)) (string, Usage, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		text, usage, err := m.generateOnce(ctx, req, onToken)
		if err == nil {
			return text, usage, nil
		}
		if !isRetryable(err) || attempt == maxAttempts {
			return "", Usage{}, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", Usage{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return "", Usage{}, lastErr
}

func (m *openAICompatLLM) generateOnce(ctx context.Context, req request, onToken func(string)) (string, Usage, error) {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.messages))
	for _, mm := range req.messages {
		switch mm.role {
		case roleSystem:
			msgs = append(msgs, openai.SystemMessage(mm.content))
		case roleUser:
			msgs = append(msgs, openai.UserMessage(mm.content))
		default:
			return "", Usage{}, fmt.Errorf("agent: unknown role %q", mm.role)
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    m.id,
		Messages: msgs,
	}
	if req.maxCompletionTokens > 0 {
		params.MaxCompletionTokens = param.NewOpt(int64(req.maxCompletionTokens))
	}
	if rf := req.responseFormat; rf != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   rf.Name,
					Schema: rf.Schema,
					Strict: param.NewOpt(true),
				},
			},
		}
	}

	stream := m.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	acc := openai.ChatCompletionAccumulator{}

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		if len(chunk.Choices) > 0 {
			// Non-streaming callers will pass nil for onToken
			if delta := chunk.Choices[0].Delta.Content; delta != "" && onToken != nil {
				onToken(delta)
			}
		}
	}

	if err := stream.Err(); err != nil {
		return "", Usage{}, fmt.Errorf("openai-compat: %w", err)
	}

	if len(acc.Choices) == 0 {
		return "", Usage{}, errors.New("openai-compat: empty choices")
	}

	usage := Usage{
		InputTokens:  int(acc.Usage.PromptTokens),
		OutputTokens: int(acc.Usage.CompletionTokens),
	}
	return acc.Choices[0].Message.Content, usage, nil
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
