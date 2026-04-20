# ADR 0002: Pluggable TTS providers with an optional streaming capability

Date: 2026-04-19
Status: Accepted

## Context

Logos narrates each completed debate step as audio. The TTS market
splits along two axes that matter for our pipeline:

- **Latency model.** Some providers (OpenAI, ElevenLabs WebSocket
  endpoints) emit audio progressively as it's synthesised — first
  bytes in 1–3 seconds for a multi-paragraph turn. Others (Gemini)
  return the full clip in one response, with end-to-end latency
  scaling with text length (30–60s for long turns).
- **Voice identity.** Every provider has its own voice catalogue and
  ID format. There is no portable "Adam, warm baritone" identifier;
  swapping providers means re-picking voices for every philosopher
  and every non-debater role.

We want to (a) keep handlers free of provider-specific code, (b)
exploit progressive playback when the provider supports it, and (c)
ship the choice of voices alongside each philosopher YAML rather than
hard-coding it for one vendor.

## Decision

Three coupled decisions, in order of how much they constrain
everything else.

### 1. Two interfaces: required and optional

```go
type Synthesizer interface {
    Synthesize(ctx context.Context, text, voice, style string) ([]byte, error)
}

type StreamSynthesizer interface {
    Synthesizer
    SynthesizeStream(ctx context.Context, text, voice, style string,
        onStart func(sampleRate int), w io.Writer) error
}
```

Every provider implements `Synthesize` (returns a self-contained WAV
byte slice). Providers that *can* stream additionally implement
`StreamSynthesizer`. The handler does a single type assertion to
pick the streaming path when available, and falls back otherwise.

This shape — required base + optional capability — is deliberate:
it lets a non-streaming provider be a one-method implementation,
while not forcing the streaming path through a lowest-common-
denominator API. See
[handler_tts.go:84-115](../backend/internal/server/handler_tts.go#L84-L115)
for the dispatch.

### 2. Providers self-register via `init()`; construction is lazy

Each provider file calls `tts.Register(name, factory)` from its own
`init()` — e.g.
[gemini.go:26-28](../backend/internal/tts/gemini.go#L26-L28),
[openai.go:31-33](../backend/internal/tts/openai.go#L31-L33). The
top of `main` blank-imports the `tts` package, which transitively
pulls every provider file in, which runs every `init()`, which
populates the registry. Adding a new provider is a single new file
in the package; no edit to `main`, `server`, or any wiring code.

`Factory` is a function, not a constructed client:

```go
type Factory func(ctx context.Context) (Synthesizer, error)
```

Registration itself does nothing but put a closure in a map — it
never touches the network, reads an API key, or opens an HTTP
client. The factory runs exactly once, on the first call to
`tts.Shared(ctx)` ([tts.go:60-83](../backend/internal/tts/tts.go#L60-L83)),
and the result is cached process-wide behind a mutex.

Two properties fall out of this split:

- **Startup is independent of credentials.** The server boots even
  if (say) the OpenAI API key is missing, as long as the *active*
  provider's credentials are present. Unused providers' keys don't
  gate liveness.
- **TTS clients are shared, not per-request.** They hold HTTP/2
  connection pools and sometimes long-lived auth tokens; creating
  one per request would waste both. The `sync.Mutex` around
  construction is only contested on the very first request.

The tradeoff is that the import graph no longer tells you which
providers are compiled in — you have to grep for `Register`. And
two providers registering the same name would silently overwrite
each other; this is a soft footgun the registry doesn't guard
against, acceptable because the name set is small and closed.

### 3. Stream raw PCM, not chunked WAV

The streaming path emits raw 16-bit little-endian mono PCM, not WAV.
This is forced by a structural fact: a WAV file's RIFF header
encodes total `dataLen` *up front*, and we don't know the total
length until synthesis ends. Chunked WAV would require either
buffering the whole clip (defeating the purpose of streaming) or
emitting a header with a placeholder length and hoping clients
tolerate it (most don't).

So the wire format is:
- Streaming path: `Content-Type: audio/L16;rate=N;channels=1`, body
  is concatenated PCM frames.
- Non-streaming path: `Content-Type: audio/wav`, body is a complete
  WAV (the provider's bytes, or `wrapWAV(pcm, ...)` from
  [tts.go:99](../backend/internal/tts/tts.go#L99) when the provider
  hands us PCM).

The cost is that the browser can't use a plain `<audio src="...">`
tag for the streaming path — see decision 4.

### 4. The `onStart(sampleRate)` callback

Sample rate is provider- and even voice-dependent, and it must be
known to *both* sides before any audio bytes flow:

- The HTTP response needs `Content-Type: audio/L16;rate=N;...`,
  which has to be sent before the body — i.e. before the first PCM
  chunk arrives from the upstream.
- The frontend needs to construct an `AudioContext({sampleRate: N})`
  with a matching rate; resampling mid-playback would be audible.

Rather than make every provider buffer the first chunk to peek at
the rate, the provider invokes `onStart(rate)` exactly once, the
moment it knows. The handler uses that callback to set headers and
call `WriteHeader`; subsequent PCM is written straight to the
response writer. See the dispatch in
[handler_tts.go:85-92](../backend/internal/server/handler_tts.go#L85-L92).

### 5. Voice resolution is split: per-philosopher + per-role

The `Synthesizer.Synthesize` signature takes `voice` and `style`,
but those values are provider-specific. We resolve them in two
places, both keyed by the active provider:

- **Per-philosopher** voices live in each philosopher's YAML under
  `tts_voices: {gemini: ..., openai: ..., elevenlabs: ...}` and are
  read off `philosopher.Data.TTSVoices`.
- **Non-debater role** voices (moderator, critic, judge, fallback)
  live in `tts.roleVoices` ([voices.go:13](../backend/internal/tts/voices.go#L13)),
  with the Gemini mapping acting as the default when a provider has
  no override.

The abstraction explicitly does not try to invent a portable voice
identifier. Providers' voice catalogues are too divergent for any
mapping layer to be honest about what swapping would sound like.

### 6. Frontend playback via Web Audio, not `<audio>`

The streaming response can't be fed to an `<audio>` element: the
element expects a container format with a parseable header (WAV,
MP3, etc.), and we're sending headerless PCM specifically because
we couldn't write a header up front (decision 2).

The frontend [LivePage.tsx:144-226](../frontend/src/pages/LivePage.tsx#L144-L226)
uses the Fetch Streams + Web Audio APIs to:
- Read `Uint8Array` chunks from the response, carrying any unaligned
  trailing byte forward to keep 16-bit frame alignment.
- Convert each aligned chunk to `Float32Array` ([-1, 1]) and wrap
  it in a one-channel `AudioBuffer`, scheduled head-to-tail on the
  shared `AudioContext`.
- On stream completion, concatenate all chunks into a single
  `AudioBuffer` so subsequent seeking and replay can use a single
  source node instead of replaying the chain. This is a free win:
  the chunks are already in memory, and seeking a chained schedule
  is awkward.

This is more code than `<audio src="...">`, but it's the price of
the streaming path; the non-streaming WAV path could in principle
use the native element, though we route both through the same code
for uniformity.

## Consequences

### Positive

- **Provider swap is a config change.** `TTS_PROVIDER=openai` vs
  `gemini` flips the entire pipeline; handlers and debate logic
  don't move.
- **Streaming is opt-in per provider.** Adding a new non-streaming
  provider is one method (`Synthesize`); adding a streaming one is
  two. The capability isn't forced on implementations that can't
  honour it.
- **Time-to-first-audio drops by an order of magnitude** on
  streaming providers (1–3s vs 30–60s for long turns). For a UI
  built around watching debates unfold, this is the difference
  between perceived liveness and a loading spinner.

### Negative

- **TTFA is not portable.** Switching from OpenAI to Gemini in
  config silently changes UX from progressive to monolithic. The
  abstraction hides the latency model, but the user sees it. We
  could expose it (e.g. show a "buffering" state per provider), but
  haven't.
- **Voice choices don't migrate.** Adding a new provider means
  filling in `tts_voices.<provider>` for every philosopher YAML
  and adding a row to `roleVoices`. There's no shortcut, by design
  (see 4), but it is friction.
- **Two transport formats.** WAV and raw L16 are both first-class
  on the server and client. A simpler design would pick one;
  decision 2 explains why we can't.
- **Mid-stream errors are unrecoverable.** Once `onStart` has
  fired, the response status is 200 and headers are flushed; a
  later upstream failure can only be logged, not signalled to the
  client as an error response. The frontend just hears truncated
  audio.

## Implementation

- [backend/internal/tts/tts.go](../backend/internal/tts/tts.go) —
  `Synthesizer` / `StreamSynthesizer` interfaces, registry,
  `Shared` lazy singleton, `wrapWAV` / `sampleRateFromMime` helpers.
- [backend/internal/tts/voices.go](../backend/internal/tts/voices.go)
  — per-provider role voices, philosopher voice resolution.
- [backend/internal/tts/{gemini,openai,elevenlabs,mistral}.go](../backend/internal/tts/)
  — provider implementations; only those with progressive APIs
  implement `StreamSynthesizer`.
- [backend/internal/server/handler_tts.go](../backend/internal/server/handler_tts.go)
  — endpoint dispatch (streaming vs WAV).
- [frontend/src/pages/LivePage.tsx](../frontend/src/pages/LivePage.tsx)
  — Web Audio chunk scheduling and post-stream buffer unification.
