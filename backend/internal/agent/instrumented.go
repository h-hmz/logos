package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/openai/openai-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// instrumentedLLM wraps any llm implementation to emit llm_usage / llm_error
// log events on every generate call. Sits outside the implementation's own
// retry loop, so one event is emitted per agent turn regardless of how many
// upstream attempts the implementation made.
type instrumentedLLM struct{ inner llm }

func (i *instrumentedLLM) name() string { return i.inner.name() }

type ctxKey int

const (
	// ctxKeyAgentName carries the calling Agent's Name through ctx so this
	// wrapper can stamp a low-cardinality `role` label on every metric.
	ctxKeyAgentName ctxKey = iota
)

// withAgentName tags ctx with the agent's display Name. Set by Agent.Run.
func withAgentName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyAgentName, name)
}

// agentNameFromContext returns the Name stamped by withAgentName.
func agentNameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyAgentName).(string)
	return v
}

// roleFromAgentName maps an Agent.Name to a stable, low-cardinality role
// label for metrics. The default "debater" bucket absorbs all per-turn
// philosopher agents (e.g. "socrates_r1", "kant_CriticResponse") so the
// per-instance names collapse into one series per (model, role) pair.
func roleFromAgentName(name string) string {
	switch name {
	case "Moderator":
		return "moderator"
	case "Critic":
		return "critic"
	case "Judge":
		return "judge"
	case "Noctua":
		return "tutor"
	default:
		return "debater"
	}
}

func (i *instrumentedLLM) generate(ctx context.Context, req request, onToken func(string)) (string, Usage, error) {
	ensureInstruments()
	startT := time.Now()
	model := i.inner.name()
	role := roleFromAgentName(agentNameFromContext(ctx))
	attrs := metric.WithAttributes(
		attribute.String("model", model),
		attribute.String("role", role),
	)

	var ttft time.Duration
	wrappedOnToken := func(delta string) {
		if ttft == 0 {
			ttft = time.Since(startT)
		}
		if onToken != nil {
			onToken(delta)
		}
	}

	text, usage, err := i.inner.generate(ctx, req, wrappedOnToken)
	dur := time.Since(startT)
	durMs := dur.Milliseconds()
	if ttft == 0 {
		ttft = dur
	}

	if err != nil {
		class := classifyLLMError(err)
		slog.LogAttrs(ctx, slog.LevelWarn, "llm_error",
			slog.String("event", "llm_error"),
			slog.String("model", model),
			slog.String("role", role),
			slog.Int64("duration_ms", durMs),
			slog.String("error_class", class),
		)
		if llmErrors != nil {
			llmErrors.Add(ctx, 1, metric.WithAttributes(
				attribute.String("model", model),
				attribute.String("role", role),
				attribute.String("error_class", class),
			))
		}
		return text, usage, err
	}

	slog.LogAttrs(ctx, slog.LevelInfo, "llm_usage",
		slog.String("event", "llm_usage"),
		slog.String("model", model),
		slog.String("role", role),
		slog.Int("input_tokens", usage.InputTokens),
		slog.Int("output_tokens", usage.OutputTokens),
		slog.Int64("duration_ms", durMs),
		slog.Int64("ttft_ms", ttft.Milliseconds()),
	)
	if inputTokens != nil {
		inputTokens.Add(ctx, int64(usage.InputTokens), attrs)
	}
	if outputTokens != nil {
		outputTokens.Add(ctx, int64(usage.OutputTokens), attrs)
	}
	if ttftSeconds != nil {
		ttftSeconds.Record(ctx, ttft.Seconds(), attrs)
	}
	return text, usage, nil
}

// classifyLLMError maps an error from the underlying llm into a stable, low-
// cardinality bucket suitable for a log-based metric label. Buckets:
//
//	timeout       — upstream or DNS timeout
//	canceled      — caller cancelled (SSE client disconnect, parent ctx done)
//	rate_limited  — HTTP 429 from provider
//	bad_request   — HTTP 4xx (auth, payment, schema, etc.)
//	upstream_5xx  — HTTP 5xx
//	network       — connection refused / DNS / non-timeout transport error
//	other         — everything else (including protocol-level "empty choices")
//
// canceled is split from timeout so SSE client disconnects don't pollute the
// timeout-alert signal.
func classifyLLMError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 429:
			return "rate_limited"
		case apiErr.StatusCode >= 500:
			return "upstream_5xx"
		case apiErr.StatusCode >= 400:
			return "bad_request"
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "timeout"
		}
		return "network"
	}
	return "other"
}
