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

// Generate runs a chat completion. Retries up to twice on transient
// failures (5xx + 429) with a 250ms delay; non-retryable errors return
// immediately.
func (m *openAICompatLLM) Generate(ctx context.Context, req Request) (string, Usage, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		text, usage, err := m.generateOnce(ctx, req)
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

func (m *openAICompatLLM) generateOnce(ctx context.Context, req Request) (string, Usage, error) {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, mm := range req.Messages {
		switch mm.Role {
		case RoleSystem:
			msgs = append(msgs, openai.SystemMessage(mm.Content))
		case RoleUser:
			msgs = append(msgs, openai.UserMessage(mm.Content))
		default:
			return "", Usage{}, fmt.Errorf("agent: unknown role %q", mm.Role)
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    m.name,
		Messages: msgs,
	}
	if req.MaxCompletionTokens > 0 {
		params.MaxCompletionTokens = param.NewOpt(int64(req.MaxCompletionTokens))
	}
	if rf := req.ResponseFormat; rf != nil {
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

	completion, err := m.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", Usage{}, fmt.Errorf("openai-compat: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", Usage{}, errors.New("openai-compat: empty choices")
	}
	usage := Usage{
		InputTokens:  int(completion.Usage.PromptTokens),
		OutputTokens: int(completion.Usage.CompletionTokens),
	}
	return completion.Choices[0].Message.Content, usage, nil
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
