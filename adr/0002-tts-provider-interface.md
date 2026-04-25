# ADR 0002: Single-provider TTS on Gemini, with progressive PCM streaming

Date: 2026-04-19
Revised: 2026-04-25
Status: Accepted (revised)

## Context

Logos narrates each completed debate step as audio. Only Gemini TTS has
ever been used in practice, and the upcoming expressive-delivery work
(inline control tags like `[excited]`, `[whispers]`, `[laughs]`) is
specific to Gemini's vocabulary. A portable abstraction that claimed to
accept any provider would either leak Gemini-isms into the interface or
force a lowest-common-denominator API that throws the tags away.

Two facts about Gemini shape the transport:

- **Sample rate is provider- and voice-dependent**, and it must be
  known before any audio bytes flow — the HTTP response needs
  `Content-Type: audio/L16;rate=N;...` before the body, and the
  browser's `AudioContext({sampleRate: N})` must match the rate or the
  clip re-samples audibly mid-playback.
- **A WAV container encodes total `dataLen` in its RIFF header**,
  which we don't know until synthesis completes. Streaming WAV would
  either buffer the whole clip (defeating streaming) or emit a
  placeholder length (most clients don't tolerate it).

## Decision

A small, Gemini-specific package that exposes two concrete functions
and owns the lazy client lifecycle. No interface. No registry. The
handler calls them directly.

### 1. Two package-level functions; the rest is unexported

```go
// backend/internal/tts/gemini.go
func Synthesize(ctx context.Context, text, voice, style string) ([]byte, error)
func SynthesizeStream(ctx context.Context, text, voice, style string,
    onStart func(sampleRate int), w io.Writer) error
```

`Synthesize` returns a self-contained WAV. `SynthesizeStream` writes
raw 16-bit little-endian mono PCM to `w` as chunks arrive from the
upstream. Both share a process-wide `*geminiClient` created once, on
first call, behind a mutex — the client holds an HTTP/2 pool and
short-lived auth tokens, and we don't want per-request construction.

The handler at
[backend/internal/server/handler_tts.go](../backend/internal/server/handler_tts.go)
always calls `SynthesizeStream` — Gemini always supports it, so the
non-streaming path exists only for callers that want a complete WAV
blob (tests, the occasional non-HTTP consumer).

### 2. Stream raw PCM, not chunked WAV

The wire format is:
- Streaming path: `Content-Type: audio/L16;rate=N;channels=1`, body
  is concatenated PCM frames.
- Non-streaming path: `Content-Type: audio/wav`, body is a complete
  WAV wrapped by `wrapWAV(pcm, ...)` from
  [tts.go:51](../backend/internal/tts/tts.go#L51).

The cost is that the browser can't use a plain `<audio src="...">`
tag for the streaming path — see decision 4.

### 3. The `onStart(sampleRate)` callback

Rather than buffer the first chunk to peek at the rate, the streaming
function invokes `onStart(rate)` exactly once, the moment it's known
from the upstream MIME. The handler uses that callback to set
headers and call `WriteHeader`; subsequent PCM is written straight to
the response writer.

### 4. Voice resolution: one voice per philosopher

Per-philosopher voices live in each philosopher YAML under
`tts_voice: <Gemini voice id>` (e.g. `Rasalgethi`, `Kore`). The
resolver at
[backend/internal/tts/voices.go](../backend/internal/tts/voices.go)
reads `philosopher.Data.TTSVoice` and falls back to `DefaultGeminiVoice`
when empty. Only the debater roles (`a`, `b`) are speakable; the
moderator/critic/judge are text-only today.

### 5. Frontend playback via Web Audio, not `<audio>`

The streaming response can't be fed to an `<audio>` element: the
element expects a container format with a parseable header (WAV, MP3,
etc.), and we're sending headerless PCM specifically because we
couldn't write a header up front (decision 2).

The frontend at
[frontend/src/pages/LivePage.tsx](../frontend/src/pages/LivePage.tsx)
uses the Fetch Streams + Web Audio APIs to:
- Read `Uint8Array` chunks from the response, carrying any unaligned
  trailing byte forward to keep 16-bit frame alignment.
- Convert each aligned chunk to `Float32Array` ([-1, 1]) and wrap
  it in a one-channel `AudioBuffer`, scheduled head-to-tail on the
  shared `AudioContext`.
- On stream completion, concatenate all chunks into a single
  `AudioBuffer` so subsequent seeking and replay can use one source
  node instead of replaying the chain.

## Consequences

### Positive

- **No plumbing tax.** No interface, no registry, no `init()`-time
  provider selection, no per-provider voice map. The design matches
  the actual provider count (one).
- **Gemini features can be used freely.** Inline control tags flow
  straight through as part of `text`; future Gemini-specific
  parameters can be added to `Synthesize`/`SynthesizeStream` without
  worrying about the lowest common denominator.
- **Time-to-first-audio is 1–3s** for multi-paragraph turns, not the
  30–60s of the old monolithic path. The streaming transport is the
  same; the change here is that we always take it.

### Negative

- **Adding a second provider later will require a real refactor.**
  Reintroducing an interface, a registry, and a provider-aware voice
  field is real work. We're betting that day won't come, and that if
  it does, we'll have concrete evidence of what the interface needs
  to generalize over (rather than the speculative shape the original
  ADR proposed).
- **Mid-stream errors are still unrecoverable.** Once `onStart` has
  fired, the response status is 200 and headers are flushed; a later
  upstream failure can only be logged, not signalled to the client
  as an error response. The frontend hears truncated audio.

## Revision history

- 2026-04-19 — original version: pluggable `Synthesizer` /
  `StreamSynthesizer` interfaces, an `init()`-time registry, and
  OpenAI / ElevenLabs / Mistral provider files alongside Gemini.
- 2026-04-25 — rewritten in place. Since the original decision, only
  Gemini had been used; the other three implementations were never
  exercised in production. With inline expressive tags landing as a
  Gemini-coupled feature, we removed the abstraction to match
  reality. Decisions 2–5 (streaming PCM, `onStart`, voice per
  philosopher, Web Audio playback) survive from the original design.

## Implementation

- [backend/internal/tts/tts.go](../backend/internal/tts/tts.go) —
  `client` lazy singleton, `wrapWAV` / `sampleRateFromMime` helpers.
- [backend/internal/tts/gemini.go](../backend/internal/tts/gemini.go)
  — `Synthesize` / `SynthesizeStream` package API and the private
  `geminiClient`.
- [backend/internal/tts/voices.go](../backend/internal/tts/voices.go)
  — per-philosopher voice resolution.
- [backend/internal/server/handler_tts.go](../backend/internal/server/handler_tts.go)
  — the single endpoint that calls into `tts.SynthesizeStream`.
- [frontend/src/pages/LivePage.tsx](../frontend/src/pages/LivePage.tsx)
  — Web Audio chunk scheduling and post-stream buffer unification.
