package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	model  string
}

func newOpenAICompatLLM(modelID string) (*openAICompatLLM, error) {
	apiKey := config.LLMAPIKey()
	if apiKey == "" {
		return nil, errors.New("agent: LLM_API_KEY not set")
	}
	c := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(config.LLMBaseURL()),
		// OpenRouter-specific attribution headers; harmless for other providers
		option.WithHeader("HTTP-Referer", "https://github.com/h-hmz/logos"),
		option.WithHeader("X-Title", "Logos"),
	)
	return &openAICompatLLM{client: c, model: modelID}, nil
}

func (m *openAICompatLLM) name() string { return m.model }

// generate runs a chat completion. Retries up to twice on transient failures
// (5xx + 429) with a 250ms delay; non-retryable errors return immediately.
func (m *openAICompatLLM) generate(ctx context.Context, req request, onToken func(string)) (string, RunMetrics, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		text, metrics, err := m.generateOnce(ctx, req, onToken)
		if err == nil {
			return text, metrics, nil
		}
		if !isRetryable(err) || attempt == maxAttempts {
			return "", RunMetrics{}, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", RunMetrics{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return "", RunMetrics{}, lastErr
}

func (m *openAICompatLLM) generateOnce(ctx context.Context, req request, onToken func(string)) (string, RunMetrics, error) {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.messages))
	for _, mm := range req.messages {
		switch mm.role {
		case roleSystem:
			msgs = append(msgs, openai.SystemMessage(mm.content))
		case roleUser:
			msgs = append(msgs, openai.UserMessage(mm.content))
		default:
			return "", RunMetrics{}, fmt.Errorf("agent: unknown role %q", mm.role)
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:    m.model,
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

	// OpenRouter reports the generation cost (USD) on the `cost` field of the
	// usage object in the final stream chunk. openai-go doesn't model this
	// non-standard extension, so it lands in JSON.ExtraFields; the accumulator
	// also drops it, so capture it from the chunk directly.
	//
	// NOTE: ExtraFields entries always report Valid()==false (openai-go only
	// marks schema-modeled fields valid), so gate on Raw() instead — it holds
	// the raw JSON number for unmodeled fields all the same.
	var costUSD float64

	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		if raw := chunk.Usage.JSON.ExtraFields["cost"].Raw(); raw != "" {
			if v, err := strconv.ParseFloat(raw, 64); err == nil {
				costUSD = v
			}
		}

		if len(chunk.Choices) > 0 {
			// Non-streaming callers will pass nil for onToken
			if delta := chunk.Choices[0].Delta.Content; delta != "" && onToken != nil {
				onToken(delta)
			}
		}
	}

	if err := stream.Err(); err != nil {
		return "", RunMetrics{}, fmt.Errorf("openai-compat: %w", err)
	}

	if len(acc.Choices) == 0 {
		return "", RunMetrics{}, errors.New("openai-compat: empty choices")
	}

	metrics := RunMetrics{
		InputTokens:  int(acc.Usage.PromptTokens),
		OutputTokens: int(acc.Usage.CompletionTokens),
		CostUSD:      costUSD,
	}
	return acc.Choices[0].Message.Content, metrics, nil
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

// IsProviderAuthError reports whether err is the upstream LLM provider rejecting
// our credentials (HTTP 401/403) — e.g. a missing, invalid, or expired LLM_API_KEY.
func IsProviderAuthError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 401 || apiErr.StatusCode == 403
	}
	return false
}
