# ADR 0008: Replace Google ADK with an in-house agent layer

Date: 2026-04-30
Status: Accepted

## Context

The debate runtime was built on `google.golang.org/adk` v1.0.0 — the
Go port of Google's Agent Development Kit. ADK provides
`llmagent.New(...)`, a `runner.Run` execution loop, an
`InMemoryService` for session state, and a `model.LLM` abstraction
for swappable LLMs.

Two facts shape the decision:

1. **The Go ADK ships a single concrete model implementation:
   `model/gemini`.** There is no `model/openai`, `model/anthropic`,
   no LiteLLM bridge. Using any non-Gemini backend requires writing
   a custom `model.LLM` adapter that translates between ADK's
   genai-shaped types (`genai.Content`, `genai.Part`) and the
   target API.
2. **The codebase uses only ADK's thinnest features.** The
   `llmagent.Config` fields actually populated across all five
   factories are: `Name`, `Description`, `Model`, `Instruction`,
   `OutputKey`. There are no `Tools` definitions, no
   `GenerateContentConfig` (temperature/top_p/etc.), no multi-agent
   handoffs, no agent-driven state mutation. The runner is invoked
   exactly once per pipeline step, with `StreamingMode: SSE`.

Combined with the OpenRouter migration in
[ADR 0007](0007-llm-tts-via-openrouter-for-spend-caps.md), the cost
of keeping ADK is a **200–400 line translation layer** (genai ↔ OpenAI
chat completions, plus tool-call format mapping if RAG is added
later). The benefit of keeping ADK is essentially zero — its
distinctive features (subagents, tool loops, evaluation, A2A) are
not used and not on the roadmap.

## Decision

Replace ADK with a small `internal/agent` package. The package
provides everything the debate pipeline actually needs:

- `State` — `map[string]any` for `{key}` slot substitution and
  `OutputKey` writes (replaces ADK's `session.State`)
- `Substitute(template, state) string` — `{identifier}` placeholder
  resolver (replaces ADK's templating)
- `LLM` interface — provider-agnostic streaming chat (replaces
  ADK's `model.LLM`)
- `Agent` struct + `Run` method — system prompt + user message +
  streaming callback + state writeback (replaces `llmagent` +
  `runner.Run`)
- `Get(modelID)` — process-wide LLM cache (replaces
  `internal/modelcache`)

ADK is removed from `go.mod` along with `google.golang.org/genai`.

### 1. The LLM interface

```go
type Role string  // "system" | "user" | "assistant" | "tool"
type Message struct {
    Role       Role
    Content    string
    ToolCalls  []ToolCall   // populated on assistant messages with tool calls
    ToolCallID string       // populated on tool-result messages
}
type Tool struct {
    Name        string
    Description string
    Schema      json.RawMessage
}
type ToolCall struct {
    ID        string
    Name      string
    Arguments json.RawMessage
}
type Request struct {
    Model    string
    Messages []Message
    Tools    []Tool          // empty today; reserved for RAG
}
type RunMetrics struct { InputTokens, OutputTokens int }
type Chunk struct {
    Delta         string
    ToolCallDelta *ToolCall
    Metrics       *RunMetrics
    Done          bool
}

type LLM interface {
    Name() string
    Generate(ctx context.Context, req Request, onChunk func(Chunk) error) (string, []ToolCall, *RunMetrics, error)
}
```

`Tool` / `ToolCall` are present from day one but unused by current
agents — see §3.

### 2. Why not another framework

- **LangChainGo**: heavy, awkward in Go, less well-maintained than
  the Python original. Same lock-in concern as ADK in the opposite
  direction (LangChain abstractions).
- **Eino** (ByteDance): the closest fit (multi-provider, tool calling,
  Go-idiomatic). Worth re-evaluating *if* the pipeline ever grows
  multi-agent orchestration. For one tool against one DB it's
  more weight than warranted today.
- **Genkit-Go** (Google): pulls us right back into the Google stack
  we just left.

The break-glass path: if multi-agent orchestration with tool
ecosystems ever lands, we can wrap our `LLM` interface around an
Eino model and reuse the agent package on top. The interface shape
was chosen with that route in mind.

### 3. Forward-compatibility for tool calling (RAG)

Anticipated future work: a vector index over the philosophers' source
texts (Aristotle's *Nicomachean Ethics*, etc.) exposed as a
`search_passages(query, philosopher)` tool the philosopher agents can
call mid-turn for citation accuracy.

The package is shaped to absorb this without restructuring:

- `Tool` / `ToolCall` shapes already defined in `llm.go`
- `Request.Tools` already plumbed through to `openAICompatLLM.Generate`
- The tool-execution loop (model → tool call → tool result → model
  again) lives in **one place** — a future `Agent.RunWithTools`
  method, ~80 LOC. It does not touch any of the existing factories
  or the debate runner.

This ADR does not implement tool calling; it commits to **not
foreclosing it**.

### 4. Substitution semantics

`Substitute` matches `\{[a-zA-Z_][a-zA-Z0-9_]*\}` — `{` immediately
followed by an identifier, no whitespace. Missing keys resolve to
empty string (matches ADK behaviour).

The regex is deliberately strict so the JSON schema examples in the
critic and judge instructions (which contain literal `{` `}` braces
with whitespace after `{`) survive unchanged. This invariant is
unit-tested.

### 5. Streaming and usage

`Generate` collapses ADK's `iter.Seq2[Event, error]` into a plain-Go
`(text, toolCalls, metrics, error)` return plus an `onChunk` callback.
Same SSE behaviour at the HTTP boundary: `onChunk` fires only on
delta tokens; the consolidated final text is returned, never
streamed to the client. RunMetrics lands on the terminal chunk via
OpenAI's `stream_options.include_usage=true`.

## Consequences

### Positive

- **Provider-agnostic from day one.** Swapping Anthropic / OpenAI /
  Mistral is a model-string change.
- **Smaller codebase overall.** The `internal/modelcache` package
  and ADK bridging boilerplate are gone; one package
  (`internal/agent`) is added.
- **No framework upgrade churn.** ADK is on v1.0.0 and evolving
  fast; the genai SDK has had several minor breaks. We carry our
  own thin layer instead.
- **Tool calling is now a small additive PR**, not a framework swap.
- **Readable end-to-end debugging.** A turn flow is `Hydrate → build step →
  Run → Substitute → openai-go → SSE` with no hidden runner machinery.

### Negative

- **We own substitution, retry, and streaming logic.** ADK had this
  battle-tested. Mitigated by tight unit tests on `Substitute` and
  by openai-go's mature streaming support.
- **No ADK tool-loop convenience.** When RAG arrives, we write the
  loop ourselves (~80 LOC).
- **No multi-agent orchestration support.** If the product grows in
  that direction, we revisit Eino. Acceptable given the explicit
  step orchestration of `pipeline` (the per-step agent builder).
- **No ADK observability hooks** (events, traces, evaluation
  harness). Replaced by `slog` instrumentation we already use.

## Implementation

- [backend/internal/agent/state.go](../backend/internal/agent/state.go) — `State` + `Substitute`
- [backend/internal/agent/llm.go](../backend/internal/agent/llm.go) — `LLM` interface + Tool/ToolCall types
- [backend/internal/agent/agent.go](../backend/internal/agent/agent.go) — `Agent` + `Run`
- [backend/internal/agent/openai_compat.go](../backend/internal/agent/openai_compat.go) — openai-go-backed `LLM` (any OpenAI-compatible endpoint)
- [backend/internal/agent/registry.go](../backend/internal/agent/registry.go) — `Get(modelID)` cache
- [backend/internal/gibberish/gibberish.go](../backend/internal/gibberish/gibberish.go) — reshaped to implement `agent.LLM`

Cross-references: [ADR 0007](0007-llm-tts-via-openrouter-for-spend-caps.md)
(provider rationale), [ADR 0003](0003-state-key-substitution.md)
(revised to delegate substitution to this package),
[ADR 0002](0002-tts-provider-interface.md) (revised to route TTS via
OpenRouter).
