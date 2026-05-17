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
// upstream attempts the implementation made — matching MONITORING_PLAN.md's
// user-perceived-failure semantics rather than a per-attempt retry log.
//
// LLM streaming is not yet wired (Chat.Completions.New, not NewStreaming),
// so ttft_ms is set equal to duration_ms; the schema stays forward-compatible
// with a later streaming migration that supplies a real first-token timestamp.
type instrumentedLLM struct{ inner llm }

func (i *instrumentedLLM) name() string { return i.inner.name() }

func (i *instrumentedLLM) generate(ctx context.Context, req request) (string, Usage, error) {
	ensureInstruments()
	startT := time.Now()
	model := i.inner.name()
	modelAttr := metric.WithAttributes(attribute.String("model", model))

	text, usage, err := i.inner.generate(ctx, req)
	dur := time.Since(startT)
	durMs := dur.Milliseconds()

	if err != nil {
		class := classifyLLMError(err)
		slog.LogAttrs(ctx, slog.LevelWarn, "llm_error",
			slog.String("event", "llm_error"),
			slog.String("model", model),
			slog.Int64("duration_ms", durMs),
			slog.String("error_class", class),
		)
		if llmErrors != nil {
			llmErrors.Add(ctx, 1, metric.WithAttributes(
				attribute.String("model", model),
				attribute.String("error_class", class),
			))
		}
		return text, usage, err
	}

	slog.LogAttrs(ctx, slog.LevelInfo, "llm_usage",
		slog.String("event", "llm_usage"),
		slog.String("model", model),
		slog.Int("input_tokens", usage.InputTokens),
		slog.Int("output_tokens", usage.OutputTokens),
		slog.Int64("duration_ms", durMs),
		slog.Int64("ttft_ms", durMs),
	)
	if inputTokens != nil {
		inputTokens.Add(ctx, int64(usage.InputTokens), modelAttr)
	}
	if outputTokens != nil {
		outputTokens.Add(ctx, int64(usage.OutputTokens), modelAttr)
	}
	if ttftSeconds != nil {
		ttftSeconds.Record(ctx, dur.Seconds(), modelAttr)
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
