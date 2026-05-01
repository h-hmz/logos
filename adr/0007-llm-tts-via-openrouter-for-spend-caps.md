# ADR 0007: Route LLM and TTS through OpenRouter for hard spend caps

Date: 2026-04-30
Revised: 2026-05-01
Status: Accepted (revised — provider-agnostic implementation, OpenRouter as recommended default)

## Context

Logos is moving from a private dev deployment to a public-facing app
(authenticated via Firebase). Both the debate LLM calls and the TTS
calls currently hit Google directly: `google.golang.org/genai` for TTS
(see [ADR 0002](0002-tts-provider-interface.md)) and ADK's
`model/gemini` for the debate agents.

Google's pay-as-you-go billing has **no hard spend cap**. Budget
*alerts* exist on Google Cloud, but they only notify — they do not
stop usage. Two threat vectors are live the moment we go public:

1. **Application abuse** — a logged-in user (or a script) hammering
   `/debates/*/next` and `/tts` to drain credits.
2. **Key compromise** — env var leak from CI, container image, or
   server breach. The Gemini API and Vertex API both honour a
   stolen key until rotated.

Application-level rate limits (per-user quotas tied to Firebase UID)
mitigate (1) but not (2). For (2) the only structural fix is a
provider that **physically cannot bill more than a deposited amount.**

## Decision

Route all LLM and TTS calls through an **OpenAI-compatible** API,
configured by default for **OpenRouter** on its prepaid credit model.
Both surfaces use OpenAI's standard endpoints:

- LLM: `POST /chat/completions` (debate agents + tutor)
- TTS: `POST /audio/speech` (Gemini TTS reachable as
  `google/gemini-3.1-flash-tts-preview`)

Single API key (`LLM_API_KEY`), single base URL (`LLM_BASE_URL`,
defaulting to `https://openrouter.ai/api/v1`), single bill, single
point of cost control.

The implementation is provider-agnostic — pointing `LLM_BASE_URL` at
any OpenAI-compatible endpoint (OpenAI, Together AI, Groq, Azure
OpenAI, vLLM, Ollama, etc.) makes the client work against it. This
ADR documents the **chosen default** (OpenRouter) and the reason
(prepaid hard cap); the code does not hardcode it.

### 1. Prepaid credits, auto-refill OFF

OpenRouter charges accounts only against credits previously deposited.
A 5.5% fee is applied at the point of credit purchase (`$100`
deposited costs `$105.50`); the inference itself is at provider list
price. When credits hit zero, the API returns 402 and stops. There
is no overdraft.

We deliberately **do not** enable OpenRouter's optional auto-refill,
which would re-introduce a soft cap. Refilling is a manual operator
action.

### 2. Same Gemini models, no quality regression

OpenRouter routes `google/gemini-3.1-flash-tts-preview` to the same
underlying Google model we use today, including the inline audio
control tags (`[whispers]`, `[laughs]`, etc.) and 70+ language
coverage. LLM debate calls similarly route to the same
`google/gemini-3-flash-preview` weights.

### 3. Provider portability is now structural

Switching to a different LLM provider (Anthropic Claude, OpenAI GPT,
Mistral, etc.) becomes a model-string change in the env. The agent
layer ([ADR 0008](0008-in-house-agent-layer-replacing-adk.md)) is
provider-agnostic by design.

### 4. Local development uses `local-gibberish`

`MODEL=local-gibberish` skips OpenRouter entirely and runs a mock
that requires no network and no API key. Default for local dev,
test environments, and anyone running the project without billing.

## Consequences

### Positive

- **Hard spend cap by construction.** Both threat vectors above are
  bounded by deposited credits.
- **Single billing relationship.** One vendor, one invoice, one
  dashboard for usage observability.
- **Provider-agnostic going forward.** A/B testing models across
  providers is a config change.
- **No Google Cloud setup required for local dev.** Project ID,
  Vertex location, ADC — all gone.

### Negative

- **5.5% premium on credit purchases.** The cost of the cap.
- **Single point of failure.** OpenRouter outage = full LLM + TTS
  outage. Mitigation: gibberish fallback for development; for
  production, accept this until volume justifies a multi-router setup.
- **~50–150ms latency hop** through OpenRouter's edge.
- **Manual refill operationally required.** Credits can run out;
  service degrades to 402 errors. We accept this as the price of
  the cap and add a low-balance alert (separate task).
- **No `Seed` parameter on TTS.** OpenAI-format `/audio/speech`
  doesn't expose it. Prosody varies slightly between identical runs;
  see [ADR 0002 revision](0002-tts-provider-interface.md).

## Implementation

- [backend/internal/agent/openai_compat.go](../backend/internal/agent/openai_compat.go) — LLM client (any OpenAI-compatible provider)
- [backend/internal/tts/openai_compat.go](../backend/internal/tts/openai_compat.go) — TTS client (same)
- [backend/internal/config/config.go](../backend/internal/config/config.go) — `LLMAPIKey()` and `LLMBaseURL()` accessors; `DefaultLLMBaseURL` constant
- See [ADR 0008](0008-in-house-agent-layer-replacing-adk.md) for the agent abstraction this rests on.

## Revision history

- 2026-04-30 — original. Coupled the LLM/TTS clients to OpenRouter
  by name (file `openrouter.go`, type `openrouterLLM`, env var
  `OPENROUTER_API_KEY`).
- 2026-05-01 — refactored to be honest about what the implementation
  is: an OpenAI-compatible client. Renamed the file/type to
  `openai_compat`, made the base URL configurable via `LLM_BASE_URL`
  (defaults to OpenRouter), and renamed the env var to `LLM_API_KEY`.
  The **decision** to default to OpenRouter for the prepaid spend
  cap is unchanged; only the implementation naming is.
