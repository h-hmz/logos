# ADR 0005: Structured logging with request-scoped context

Date: 2026-05-14
Status: Accepted

## Context

The backend originally used `slog` directly with a plain `JSONHandler`.
Log output was structured but unhelpful in practice:

- No correlation between log lines belonging to the same HTTP request.
  A `"service error"` entry sat next to TTS stream logs with no way to
  know they came from the same user action.
- Panics in handlers would crash the goroutine with no log at all; ops
  would only see the 500 at the load balancer.
- A mix of `slog.Error(...)` (no ctx) and `slog.ErrorContext(ctx, ...)`
  sites meant request-scoped metadata would be silently skipped by some
  call sites.
- The JSON keys (`level`, `msg`) were not the special fields Cloud
  Logging recognises, so on Cloud Run there was no severity colouring
  and no request summary line.

The goal was production-grade observability — request correlation,
crash visibility, and a Cloud Run cost/latency dashboard — without
forcing every caller in the domain packages (`debate`, `tts`,
`philosopher`, ...) to thread a `*slog.Logger` through their APIs.

## Decision

### 1. HTTP middleware chain

Wired in `server.New` in this order:

  RequestID → TraceContext → Recover → CORS →
  (public handlers) | authMiddleware → SessionID → (protected handlers)

- `requestIDMiddleware` honours an incoming `X-Request-Id` header (so
  callers / front-ends / upstream proxies can propagate their own) and
  generates a UUID when absent. The ID is stored on `context.Context`
  under an unexported key and echoed on the response header.
- `traceContextMiddleware` parses the W3C `traceparent` header
  (`VERSION-TRACE_ID-SPAN_ID-FLAGS`) that Cloud Run automatically
  populates for incoming requests. Trace id (32 hex) and span id
  (16 hex) flow straight into context — no decimal→hex conversion,
  unlike the legacy `X-Cloud-Trace-Context` header, which is not
  parsed. Malformed or missing headers leave the context empty —
  no fallback trace id is generated; `request_id` remains the
  correlation id of last resort. Mounted early so every subsequent
  log record carries `trace_id`.
- `recoverMiddleware` converts panics to 500s and logs the stack
  trace. It wraps the `ResponseWriter` in a `statusRecorder` (which
  preserves `http.Flusher` so SSE still works) so it can skip writing
  a 500 over a response the handler already committed — e.g. a panic
  mid-SSE-stream. Placed **outside** the handler so CORS headers set
  by middleware remain intact.
- `sessionIDMiddleware` is mounted via `pr.With(sessionIDMiddleware)`
  on the `/debates/{debateID}/*` routes only. It reads chi's bound
  `debateID` URL param and stamps it on the context as the session id.
  `POST /debates` (creation) stays outside the middleware — the
  session id does not exist until the UUID is generated mid-request,
  and the create handler runs no LLM calls.

### 2. Context-aware root `slog.Handler` (`contextHandler` in `main.go`)

`contextHandler` wraps the JSON handler and, on every record, copies
request-scoped values off `context.Context` into log attributes when
present:

- `request_id`
- `uid` — the authenticated Firebase UID.
- `trace_id` — plain 32-hex (the field log-based-metric labels key on).
- `logging.googleapis.com/trace` — the qualified
  `projects/<PROJECT>/traces/<TRACE_ID>` form Cloud Logging requires
  for clickthrough to the Trace UI. Skipped when `config.Project()` is
  empty (local dev).
- `logging.googleapis.com/spanId` — 16-hex span id when present.
- `session_id` — the debate id.

`WithAttrs` and `WithGroup` are overridden to re-wrap the result —
otherwise `slog.With(...)` would return the inner handler and silently
drop the interceptor.

This means **any existing `slog.*Context(ctx, ...)` call anywhere in
the codebase automatically carries these fields**, without the domain
packages importing anything from `server` or knowing that middleware
exists.

### 3. Cloud Logging special-field format

The root handler is constructed with a `slog.HandlerOptions.ReplaceAttr`
hook (`gcpReplaceAttr`) that rewrites slog's built-in keys at the top
level only: `level` → `severity` (with slog's `WARN` remapped to the
`LogSeverity` enum spelling `WARNING`) and `msg` → `message`. `time` is
left as-is — Cloud Logging accepts RFC 3339 under that key. A
`len(groups) == 0` guard keeps grouped attrs untouched.

### 4. No app-level access log — rely on Cloud Run's platform log

The backend deliberately does **not** emit its own per-request access
log. Cloud Run already writes a request log
(`run.googleapis.com/requests`) for every request, with method, URL,
status, latency, request/response sizes, and the same `trace` id our
records carry. An app-level `http_request` line would be a near
duplicate — same data, measured one layer in — so it was dropped. The
`trace` shared between the platform log and our `slog` records is the
join key: filtering on it returns the platform request entry alongside
our `llm_usage` / error logs.

The cost of this choice is no access log in **local dev**, where there
is no platform log. Acceptable — local debugging leans on the handler
and error logs, not an access log.

### 5. Handler-side error logs use the `Context` variants

`writeServiceError` and `serviceErrorMessage` take `ctx`; `sseWriter`
captures the request context at construction so marshal failures also
carry `request_id`.

### 6. Provider-agnostic LLM instrumentation via decorator

`instrumentedLLM` in the `agent` package wraps the `llm` interface and
emits one structured event per `generate` call:

- `llm_usage` (Info) on success: `event`, `model`, `input_tokens`,
  `output_tokens`, `duration_ms`, `ttft_ms`.
- `llm_error` (Warn) on failure: `event`, `model`, `duration_ms`,
  `error_class` (one of `timeout` / `canceled` / `rate_limited` /
  `bad_request` / `upstream_5xx` / `network` / `other`).

The wrapper is attached at `getLLM` in `agent/registry.go`, so every
agent (philosopher, critic, judge, moderator, tutor) is instrumented
through a single line. It sits **outside** the implementation's retry
loop, so `llm_error` is emitted at most once per agent turn (post-retry,
user-visible). Per-attempt visibility is intentionally not exposed here.

LLM calls are non-streaming today, so `ttft_ms` is set equal to
`duration_ms`. The schema is forward-compatible with a later streaming
migration that supplies a real first-token timestamp.

The fixed-schema usage events feed a **Cloud Monitoring / Grafana
dashboard built on Cloud Logging log-based metrics**, configured
GCP-side outside this repo. The constraints that drove the backend
shape:

- **Backend emits raw usage only.** No pricing knowledge in the
  application code — pricing constants live in the dashboard
  definition, applied as inline multipliers on log-based metrics.
  Updating a price = edit the dashboard, no backend redeploy.
- **Log-based metrics, not push-based.** Reuses the structured logs
  already needed for debugging — no exporter, no flush logic — and
  `trace_id` / `session_id` correlation is free.
- **Minimal label cardinality.** Metrics are labeled on `model` only.
  High-cardinality dimensions (`session_id`, `trace_id`, `request_id`)
  are reachable via Log Analytics SQL queries, not as metric labels —
  including them as labels would explode cardinality charges.
- **`event` is a flat string attribute, not a `slog.Group`.** Cloud
  Logging filters (`jsonPayload.event = "llm_usage"`) are simpler
  against flat fields.

## Consequences

### Positive

- One log query filtered on `request_id=X` (or `trace_id`, or
  `session_id`) returns the complete timeline of a request across
  handler, service, repo, and TTS layers.
- Cloud Run's platform request log plus the recover middleware give
  production the two views ops needs first: "what requests happened"
  and "what crashed".
- Cloud Logging renders the output natively — severity colouring and
  clickthrough from Logs Explorer to the Trace UI.
- Domain packages stay HTTP-agnostic — they keep calling
  `slog.InfoContext` / `slog.ErrorContext` on the global logger.
- The `X-Request-Id` header on responses lets the frontend log the ID
  alongside user-visible errors, so a user bug report can be joined to
  server logs by ID.
- A future Anthropic-native or Gemini-native LLM implementation gets
  the usage/error events for free — only `classifyLLMError` may grow
  another `errors.As` arm.

### Negative

- The `contextHandler` adds a handful of context lookups per log
  record. Negligible in practice but non-zero.
- `main` imports `server` for the `*FromContext` accessors. It already
  did, so no new coupling, but if we later want `main` independent of
  `server` the keys + accessors can be lifted into an `internal/reqctx`
  package.
- Plain `slog.Info(...)` calls (no `Context`) still bypass the
  interceptor. Current call sites all use `Context`; a lint rule could
  enforce this going forward if it becomes a drift risk.

## Non-goals / deferred

- **Configurable log level.** Info-and-up is hardcoded in `main.go`.
  `LOG_LEVEL` env → `slog.HandlerOptions.Level` is a small follow-up.
- **PII / secret redaction.** Debate topics and model API responses
  could leak into error logs via `%v` wrapping. A redacting
  `slog.Handler` keyed on well-known sensitive field names is on the
  list but not yet required — no such leak has been observed.
- **Distributed tracing spans.** Cloud Run's injected trace id is
  parsed and surfaced, but no spans are emitted. OpenTelemetry would
  give spans for LLM / Firestore / TTS calls; deferred until there is a
  second service to correlate with.
- **TTS instrumentation.** The `instrumentedLLM` decorator pattern will
  apply at the package-level `SynthesizeStream` / `Synthesize` entry
  points when that change lands.
- **TTS per-chunk logs demoted to Debug.** Currently Info; noisy in
  production. Queued behind the configurable-level work since both
  touch the same initialisation path.
- **Service metadata defaults** (`service`, `version`, `env`,
  `instance_id`) as root attributes. Useful once multi-instance,
  unnecessary for the single-service current deployment.

## Implementation

- [backend/main.go](../backend/main.go) — `contextHandler` wrapping
  `slog.NewJSONHandler`; `gcpReplaceAttr` for the special-field key
  rewrites; set as the default logger.
- [backend/internal/server/middleware.go](../backend/internal/server/middleware.go) —
  `requestIDMiddleware`, `recoverMiddleware`, `statusRecorder`, the
  `ctxKey` values, and `RequestIDFromContext` / `UIDFromContext`.
- [backend/internal/server/trace.go](../backend/internal/server/trace.go) —
  `traceContextMiddleware`, `parseTraceparent`,
  `TraceIDFromContext`, `SpanIDFromContext`.
- [backend/internal/server/session.go](../backend/internal/server/session.go) —
  `sessionIDMiddleware`, `SessionIDFromContext`, exported
  `WithSessionID` for callers outside the HTTP path.
- [backend/internal/server/routes.go](../backend/internal/server/routes.go) —
  middleware chain wiring; `/debates/{debateID}/*` routes wrapped with
  `sessionIDMiddleware` via `pr.With(...)`; CORS allows and exposes
  `X-Request-Id`.
- [backend/internal/server/handler_debate.go](../backend/internal/server/handler_debate.go),
  [backend/internal/server/sse.go](../backend/internal/server/sse.go) —
  `slog.Error` → `slog.ErrorContext`; `writeServiceError` /
  `serviceErrorMessage` / `newSSEWriter` take `ctx`.
- [backend/internal/agent/instrumented.go](../backend/internal/agent/instrumented.go) —
  `instrumentedLLM` decorator and `classifyLLMError`.
- [backend/internal/agent/registry.go](../backend/internal/agent/registry.go) —
  wraps the constructed implementation with `instrumentedLLM` before
  caching.
