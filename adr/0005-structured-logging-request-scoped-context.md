# ADR 0005: Structured logging with request-scoped context

Date: 2026-04-19
Status: Accepted

## Context

Before this change the backend used `slog` directly with a plain
`JSONHandler`. Log output was structured but unhelpful in practice:

- No correlation between log lines belonging to the same HTTP request.
  A `"service error"` entry sat next to TTS stream logs with no way to
  know they came from the same user action.
- No access log. There was no record of which route was hit, with
  which status, or how long it took — so latency regressions and 500
  spikes were invisible until someone reproduced them locally.
- Panics in handlers would crash the goroutine with no log at all; ops
  would only see the 500 at the load balancer.
- A mix of `slog.Error(...)` (no ctx) and `slog.ErrorContext(ctx, ...)`
  sites meant even once we had request-scoped metadata, some logs
  would silently skip it.

The goal was production-grade observability without forcing every
caller in the domain packages (`debate`, `tts`, `philosopher`, ...) to
thread a `*slog.Logger` through their APIs.

## Decision

**1. Three HTTP middlewares, wired in `server.New` in this order:**

   RequestID → AccessLog → Recover → CORS → handlers

- `requestIDMiddleware` honours an incoming `X-Request-Id` header (so
  callers / front-ends / upstream proxies can propagate their own)
  and generates a UUID when absent. The ID is stored on
  `context.Context` under an unexported key and echoed on the
  response header.
- `accessLogMiddleware` wraps the `ResponseWriter` in a
  `statusRecorder` (preserving `http.Flusher` so SSE still works) and
  emits one `"http_request"` Info record per request with method,
  path, status, bytes, duration, and client IP.
- `recoverMiddleware` converts panics to 500s and logs the stack
  trace. It is placed **inside** the access log so the recovered
  status is still recorded, and **outside** the handler so CORS
  headers set by middleware remain intact.

**2. A context-aware root `slog.Handler` in `main.go`** (`contextHandler`)
wraps the JSON handler and calls `server.RequestIDFromContext(ctx)`
on every record, adding `request_id` as an attribute when present.
`WithAttrs` and `WithGroup` are overridden to re-wrap the result —
otherwise `slog.With(...)` would return the inner handler and silently
drop the interceptor.

This means **any existing `slog.*Context(ctx, ...)` call anywhere in
the codebase automatically carries `request_id`**, without the domain
packages importing anything from `server` or knowing that middleware
exists.

**3. All handler-side error logs use the `Context` variants.**
`writeServiceError` and `serviceErrorMessage` now take `ctx`;
`sseWriter` captures the request context at construction so marshal
failures also carry `request_id`.

## Consequences

### Positive

- One log query filtered on `request_id=X` returns the complete
  timeline of a request across handler, service, repo, and TTS layers.
- Access log + recover middleware give production the two logs ops
  needs first: "what requests happened" and "what crashed".
- Domain packages stay HTTP-agnostic — they keep calling
  `slog.InfoContext` / `slog.ErrorContext` on the global logger.
- The `X-Request-Id` header on responses lets the frontend log the ID
  alongside user-visible errors, so a user bug report can be joined
  to server logs by ID.

### Negative

- The `contextHandler` adds a map lookup per log record. Negligible in
  practice but non-zero.
- `main` now imports `server` for `RequestIDFromContext`. It already
  did, so no new coupling, but if we later want `main` independent of
  `server` the key + accessor can be lifted into an `internal/reqctx`
  package.
- Plain `slog.Info(...)` calls (no `Context`) still bypass the
  interceptor. Current call sites all use `Context`; a lint rule
  could enforce this going forward if it becomes a drift risk.

## Non-goals / deferred

These were considered and intentionally left for later:

- **Configurable log level.** Currently Info-and-up is hardcoded in
  `main.go`. `LOG_LEVEL` env → `slog.HandlerOptions.Level` is a small
  follow-up.
- **PII / secret redaction.** Debate topics and model API responses
  could leak into error logs via `%v` wrapping. A redacting
  `slog.Handler` keyed on well-known sensitive field names is on the
  list but not yet required — no such leak has been observed.
- **Distributed tracing.** `request_id` is a poor man's trace ID.
  OpenTelemetry would give spans for LLM / Firestore / TTS calls and
  carry trace context across services when we grow beyond one
  backend. Deferred until there is a second service to correlate
  with.
- **TTS per-chunk logs demoted to Debug.** Currently Info; noisy in
  production. One-liner change, queued behind the configurable level
  work since both touch the same initialisation path.
- **Service metadata defaults** (`service`, `version`, `env`,
  `instance_id`) as root attributes. Useful once multi-instance,
  unnecessary for the single-service current deployment.

## Implementation

- [backend/internal/server/middleware.go](../backend/internal/server/middleware.go) —
  new file; the three middlewares, `statusRecorder`, and
  `RequestIDFromContext`.
- [backend/internal/server/routes.go](../backend/internal/server/routes.go) —
  `r.Use(requestIDMiddleware)` / `accessLogMiddleware` /
  `recoverMiddleware`; CORS updated to allow and expose
  `X-Request-Id`.
- [backend/main.go](../backend/main.go) — `contextHandler` wrapping
  `slog.NewJSONHandler` as the default logger.
- [backend/internal/server/handler_debate.go](../backend/internal/server/handler_debate.go),
  [backend/internal/server/sse.go](../backend/internal/server/sse.go) —
  migrated `slog.Error` → `slog.ErrorContext`; `writeServiceError` /
  `serviceErrorMessage` / `newSSEWriter` take `ctx`.
