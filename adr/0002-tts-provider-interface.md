# ADR 0002: Pluggable Text-to-Speech (TTS) architecture with Streaming Support

Date: 2026-04-19
Status: Accepted

## Context

The Logos application includes a Text-to-Speech (TTS) feature to narrate debate steps. We needed a way to synthesize the text generated during a debate into audio.

However, different TTS providers have significantly different capabilities and requirements:
- **Google Gemini TTS**: Generates audio monolithically, resulting in long latency to first audio (e.g., 30–60s) for long debate turns.
- **OpenAI TTS**: Can genuinely stream audio chunks over the network as they are synthesized, allowing for progressive playback.
- **Other Providers**: like Mistral/Voxtral and ElevenLabs, may offer different quality, cost, or voice options.

To avoid tight coupling to any single provider and to exploit advanced capabilities like streaming where available, we needed an abstraction layer. The server needs to stream audio back to the frontend dynamically so that the user experience is smooth when long pieces of text are processed.

## Decision

Introduce a pluggable provider interface in the `tts` package to handle Text-to-Speech synthesis, supporting both monolithic and streaming audio generation.

### 1. `Synthesizer` and `StreamSynthesizer` Interfaces

We define a base interface for monolithic generation and an optional extension interface for streaming:

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

- `Synthesizer`: Produces a self-contained WAV byte slice.
- `StreamSynthesizer`: Emits raw 16-bit little-endian mono PCM progressively. The `onStart` callback allows the HTTP handler to flush partial body chunks or response headers to the client immediately once the sample rate is known.

### 2. Provider Registration via `init()`

Providers (OpenAI, Gemini, Mistral, ElevenLabs) register themselves via their package logic (`init()` func) with a simple factory method system:

```go
func Register(name string, f Factory) { ... }
```

At runtime, the active provider is selected based on the `TTS_PROVIDER` environment variable (managed via the `config` package). A single process-wide shared client is instantiated lazily on the first request to the relevant endpoints.

### 3. Audio Delivery

If a provider implements `StreamSynthesizer`, the server can stream PCM audio dynamically. The `onStart` callback exposes the sample rate out-of-band and makes it possible to specify stream properties so the client knows what is coming. 

If a provider only supports monolithic generation, the system falls back to `Synthesize()` to generate the full WAV payload, wrapping raw PCM with a RIFF/WAVE header via a utility function (`wrapWAV`).

### 4. Frontend AudioContext Streaming

On the frontend browser side, raw stream tracking requires avoiding the native `<audio>` HTML element (which provides poor cross-browser support for raw stream chunking and seeking). Instead, we utilize the Web Audio API alongside the Fetch Streams API:

1. **Fetching and Byte Alignment:** The `fetch` API stream reader yields irregular `Uint8Array` chunks. The client ensures 16-bit block alignment (a required boundary for L16 PCM format) by carrying over unaligned remainder bytes to the next chunk in a temporary buffer.
2. **Buffer Float Translation:** The incoming aligned 16-bit integers (`Int16Array`) are translated directly into 32-bit floats (`Float32Array`) scaled between `-1.0` and `1.0`, which is the format mandated by the Web Audio API.
3. **Pipelining `AudioBufferSourceNode`s:** Instead of waiting for the full transmission to complete, the frontend immediately creates an individual 1-channel `AudioBuffer` and `AudioBufferSourceNode` for each yielded chunk. It schedules these sequence blocks head-to-tail, accurately tracking continuous timing boundaries by accumulating durations (`nextScheduleRef.current = when + buf.duration`). 
4. **Buffer Finalization (Seeking & Replays):** Upon completion of the stream, all arrived chunks are unified into a single full-length `AudioBuffer`. Subsequent replays or timeline seeking operations swap from the fragmented chain sequence to this unified buffer, enabling native-like instant playback and arbitrary timeline scrubbing.

## Consequences

### Positive

- **Flexibility**: We can easily swap out TTS providers via `.env` configuration without changing the HTTP handlers or debate logic.
- **Performant UX**: Open-ended streaming support allows clients to begin playback of audio streams from compatible providers (like OpenAI) almost immediately, dramatically reducing Time to First Audio (TTFA).
- **Graceful Degradation**: Providers that don't support streaming can gracefully fall back to full WAV delivery, ensuring system robustness regardless of the active vendor.

### Negative

- **Protocol Complexity**: Handling raw PCM audio requires parsing properties (e.g. `audio/L16;rate=24000`) and managing the Web Audio API or a custom buffer on the frontend, rather than just pointing an `<audio src="...">` tag.
- **Inconsistent Behaviors**: Swapping from a streaming pipeline (e.g., OpenAI) to a monolithic pipeline (e.g., Gemini) results in drastically different TTFA latencies, which might confuse end-users.
- **Maintenance**: We now manage API adapters for multiple major vendors, increasing maintenance overhead due to external schema changes.

## Implementation

- `backend/internal/tts/tts.go` — Registration logic, `Synthesizer` / `StreamSynthesizer` interfaces, and format utilities.
- `backend/internal/tts/gemini.go`, `openai.go`, `mistral.go`, `elevenlabs.go` — Adopters / implementations.
- `backend/internal/server/server.go` — Endpoints wiring the abstractions to handlers.
