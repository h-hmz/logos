# ADR 0002: Single-provider TTS on Gemini, with progressive PCM streaming

Date: 2026-06-15
Revised: 2026-07-06
Status: Accepted

## Context

Logos narrates each completed debate step as audio. Only Gemini TTS is used,
and its expressive-delivery features (inline control tags like `[excited]`,
`[whispers]`, `[laughs]`) are specific to Gemini's vocabulary. A portable
abstraction that claimed to accept any provider would either leak Gemini-isms
into the interface or force a lowest-common-denominator API that throws the tags
away. Synthesis goes through the OpenAI-compatible `/audio/speech` endpoint
(OpenRouter by default — see [ADR 0007](0007-llm-tts-via-openrouter-for-spend-caps.md)),
which resolves `google/gemini-3.1-flash-tts-preview` to Gemini's weights.

Three facts about Gemini shape the transport:

- **The sample rate must be known before any audio bytes flow** — the HTTP
  response needs `Content-Type: audio/L16;rate=N;...` before the body, and the
  browser's `AudioContext({sampleRate: N})` must match the rate or the clip
  re-samples audibly mid-playback. OpenRouter's `/audio/speech` does not surface
  a MIME rate header, so the rate is fixed at 24 kHz (16-bit, mono).
- **A WAV container encodes total `dataLen` in its RIFF header**, which we don't
  know until synthesis completes. Streaming a WAV would either buffer the whole
  clip (defeating streaming) or emit a placeholder length most clients reject.
- **Gemini TTS caps output at 3840 audio tokens (~2 minutes) per API call**,
  signalled via `finish_reason: "OTHER"` rather than `MAX_TOKENS`. Long debate
  turns exceed this and are truncated mid-sentence with no error returned.

Persistence and caching of narrations (serve a stored clip on replay instead of
re-synthesising) are layered on top of this transport — see
[ADR 0012](0012-narration-persistence.md).

## Decision

A small, Gemini-specific package with no interface and no registry: the handler
calls one streaming function directly, and the package owns a lazy client.

### 1. One package-level streaming function

```go
func SynthesizeStream(ctx context.Context, text, voice, style string,
    onStart func(sampleRate int), w io.Writer) error
```

It writes raw 16-bit little-endian mono PCM to `w` as chunks arrive from the
upstream. It is backed by a process-wide client created once, on first call,
behind a mutex — the client holds an HTTP/2 pool and short-lived auth tokens, so
we don't want per-request construction. There is deliberately **no `Synthesizer`
interface and no provider registry**: the design matches the actual provider
count (one), and reintroducing an abstraction is cheaper to do later, with real
evidence of what it must generalise over, than to carry speculatively now.

### 2. Stream raw PCM, not WAV

The wire format is `Content-Type: audio/L16;rate=24000;channels=1` with a body
of concatenated PCM frames. A WAV header can't be written up front (it needs the
total length), and we want bytes flowing immediately — so we send headerless
PCM. The cost is that the browser can't use a plain `<audio src="...">` tag (see
decision 5).

### 3. The `onStart(sampleRate)` callback

`SynthesizeStream` invokes `onStart(rate)` exactly once, the moment before the
first bytes are written. The handler uses it to set headers and call
`WriteHeader`; subsequent PCM is written straight to the response writer.

### 4. Voice resolution: one voice per philosopher

Per-philosopher voices live in each philosopher YAML under `tts_voice`
(e.g. `Rasalgethi`, `Kore`) and `tts_style`. The resolver at
[backend/internal/tts/voices.go](../backend/internal/tts/voices.go) reads
`philosopher.Data.TTSVoice`/`TTSStyle`; an empty voice falls back to
`DefaultGeminiVoice` at synthesis time. Only the debater roles (`a`, `b`) are
speakable; the moderator/critic/judge are text-only.

### 5. Frontend playback via Web Audio, not `<audio>`

The streaming response can't be fed to an `<audio>` element, which expects a
container with a parseable header — we send headerless PCM (decision 2). The
frontend ([frontend/src/components/PlayButton.tsx](../frontend/src/components/PlayButton.tsx),
[frontend/src/api.ts](../frontend/src/api.ts)) uses Fetch Streams + Web Audio to:
- read `Uint8Array` chunks, carrying any unaligned trailing byte forward to keep
  16-bit frame alignment;
- convert each aligned chunk to `Float32Array` ([-1, 1]) in a one-channel
  `AudioBuffer`, scheduled head-to-tail on the shared `AudioContext`;
- on completion, concatenate all chunks into one `AudioBuffer` so seeking and
  replay use a single source node.

### 6. One API call per narration; the 2-minute cap is accepted (rev 2026-07-06)

`SynthesizeStream` sends the whole text as a single `/audio/speech` call.
Audio beyond the model's ~2-minute per-call output cap is silently truncated,
and we accept that truncation: most narratable turns fit comfortably, and a
long one loses its tail rather than arbitrary middles.

The original decision split the text into segments of at most 800 characters
(≈ 60 s at 150 wpm) and issued one call per segment, concatenating the PCM.
In practice this **skipped whole chunks of text at segment boundaries**: the
character count only proxies the audio-token cap, and slow style directives
plus inline delivery tags (`[dramatic pause]`, `[sighs]` — seconds of audio
from a handful of characters) pushed segments past it. The truncation is
invisible through the OpenAI-format endpoint (raw bytes, no `finish_reason`),
so the truncated segment was concatenated with the next and — worse —
persisted to the narration store (ADR 0012), making the skip permanent.
Splitting also produced best-effort voice consistency at boundaries (no `seed`
parameter through OpenRouter). One call removes the boundary artifacts
entirely at the cost of capping narration length at the model's limit.

## Consequences

### Positive

- **No plumbing tax.** No interface, no registry, no `init()`-time provider
  selection, no per-provider voice map.
- **Gemini features flow freely.** Inline control tags pass straight through as
  part of `text`; future Gemini-specific parameters can be added to
  `SynthesizeStream` without a lowest-common-denominator constraint.
- **Time-to-first-audio is 1–3 s** for multi-paragraph turns.
- **No segment-boundary artifacts.** A narration is one model call: no skipped
  chunks, no timbre shifts mid-clip.

### Negative

- **Adding a second provider later requires a real refactor** — an interface, a
  registry, and a provider-aware voice field. We bet that day won't come.
- **Mid-stream errors are unrecoverable.** Once `onStart` has fired, the status
  is 200 and headers are flushed; a later upstream failure can only be logged,
  and the client hears truncated audio.
- **Narrations cap at ~2 minutes of audio.** A turn whose speech exceeds the
  model's 3840-audio-token cap is truncated at the tail, silently — the
  endpoint surfaces no `finish_reason`. Accepted (rev 2026-07-06): losing a
  long turn's tail beats losing arbitrary chunks at segment boundaries.

## Implementation

- [backend/internal/tts/tts.go](../backend/internal/tts/tts.go) — the lazy
  process-wide client singleton; the hardcoded 24 kHz `SampleRate`.
- [backend/internal/tts/openai_compat.go](../backend/internal/tts/openai_compat.go)
  — `SynthesizeStream`, the private `openAICompatTTSClient` (openai-go against
  the configured base URL), and the Gemini prompt/style helper.
- [backend/internal/tts/voices.go](../backend/internal/tts/voices.go) —
  per-philosopher voice resolution.
- [backend/internal/server/handler_tts.go](../backend/internal/server/handler_tts.go)
  — the endpoint that calls `tts.SynthesizeStream` (and serves persisted
  narrations per [ADR 0012](0012-narration-persistence.md)).
- [frontend/src/components/PlayButton.tsx](../frontend/src/components/PlayButton.tsx),
  [frontend/src/api.ts](../frontend/src/api.ts) — Web Audio chunk scheduling and
  post-stream buffer unification.

## Revision history

- 2026-06-15 — original. Decision 6 split long text into ≤800-char segments,
  one API call each, to narrate arbitrarily long turns under the per-call cap.
- 2026-07-06 — decision 6 replaced: one API call per narration, cap accepted.
  Character-based splitting under-estimated audio time (style directives,
  delivery tags), so segments sometimes hit the cap and were silently
  truncated mid-segment — heard as skipped text at segment boundaries and
  cached that way permanently.
