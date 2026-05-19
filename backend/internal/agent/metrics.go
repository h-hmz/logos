package agent

import (
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Lazy-initialized so that the global MeterProvider set by telemetry.Init has
// been installed before we create instruments. Calling Meter() before Init
// would return a no-op meter and silently drop measurements.
var (
	instrumentsOnce sync.Once
	inputTokens     metric.Int64Counter
	outputTokens    metric.Int64Counter
	ttftSeconds     metric.Float64Histogram
	llmErrors       metric.Int64Counter
)

func ensureInstruments() {
	instrumentsOnce.Do(func() {
		meter := otel.Meter("github.com/h-hmz/logos/internal/agent")
		var err error

		if inputTokens, err = meter.Int64Counter("llm.input.tokens",
			metric.WithDescription("Input tokens billed per LLM call, by model."),
			metric.WithUnit("{token}"),
		); err != nil {
			slog.Warn("metric registration failed", "name", "llm.input.tokens", "err", err)
		}

		if outputTokens, err = meter.Int64Counter("llm.output.tokens",
			metric.WithDescription("Output tokens generated per LLM call, by model."),
			metric.WithUnit("{token}"),
		); err != nil {
			slog.Warn("metric registration failed", "name", "llm.output.tokens", "err", err)
		}

		// Explicit boundaries tuned for LLM TTFT: 50ms → ~60s. Default OTel
		// boundaries are tuned for sub-second RPC latency and waste resolution
		// in the multi-second range where LLMs actually live.
		if ttftSeconds, err = meter.Float64Histogram("llm.ttft",
			metric.WithDescription("Time to first token per LLM call (currently equals total duration until streaming lands)."),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60),
		); err != nil {
			slog.Warn("metric registration failed", "name", "llm.ttft", "err", err)
		}

		if llmErrors, err = meter.Int64Counter("llm.errors",
			metric.WithDescription("LLM calls that failed, by model, role, and ClassifyLLMError bucket."),
			metric.WithUnit("{error}"),
		); err != nil {
			slog.Warn("metric registration failed", "name", "llm.errors", "err", err)
		}
	})
}
