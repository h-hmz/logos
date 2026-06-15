# ADR 0012: Persist narrations to object storage, content-addressed, proxied

Date: 2026-06-15
Status: Accepted (partially revises [ADR 0002](0002-tts-provider-interface.md))

## Context

TTS today is fully ephemeral ([ADR 0002](0002-tts-provider-interface.md)): the
`/tts` handler streams headerless PCM with `Cache-Control: no-store` and keeps
nothing. The frontend buffers a clip *within a session*, but a reload, a second
device, or a return visit re-synthesizes from scratch — re-paying the TTS
provider for bytes we already produced. We want a completed
[narration](../CONTEXT.md) (the synthesized audio rendering of a step's text) to
survive so a later replay serves a stored copy instead of re-synthesizing.

The hard part is not storage; it is that persistence **decouples *getting the
bytes* from *paying for the synthesis***. The ephemeral model gets cost-bounding
for free: every fetch is a synthesis, so one control (the planned per-generation
quota) bounds the entire surface. A free replay is, from an Economic-Denial-of-
Sustainability lens, *unbounded free egress*. The cost-saving feature and the
EDoS vector are the same mechanism — so persistence cannot be reasoned about
independently of how replay egress is bounded.

A second constraint: [ADR 0007](0007-llm-tts-via-openrouter-for-spend-caps.md)
deliberately shed GCP dependencies so local dev needs no Google Cloud setup.
Persisting to Cloud Storage must not leak into the local/test path.

## Decision

Persist each completed narration to object storage, content-addressed, and serve
replays by **proxying** the bytes through the backend. The synthesis transport of
ADR 0002 (progressive PCM on a miss) is unchanged; persistence is layered on top.
Audio is stored as **raw PCM for now**, with compression (Opus) as a planned
second phase (§3).

### 1. Content-addressed identity

A narration's bytes are a pure function of four inputs, so the key is exactly
those: `hash(normalized_text, voice, style, model)`. `normalized_text` is the
*post-`stripParentheticals`* string actually synthesized; `voice`/`style` come
from role resolution; `model` from config. The object lives at
`{debateID}/{hash}.pcm`. `stepKey` is deliberately **excluded**: a retry that
changes a step's text yields a new hash (a new object; the old one is orphaned),
and identical text self-dedupes. The `{debateID}/` prefix costs nothing real
(cross-debate byte-identical turns are essentially impossible) and buys clean
per-debate lifecycle. The format lives in the object name, not the hash, so a
later format (`.opus`) coexists cleanly under the same key.

### 2. Write on a miss: tee into a buffer, Put on success only

The store interface is asymmetric — `Open(key) → io.ReadCloser` for the hot read
path (stream straight to the client, near-zero per-hit memory) and
`Put(key, []byte)` for the cold write path. On a miss the PCM is written to the
client (progressive first-audio, unchanged) **and** accumulated into an in-memory
`bytes.Buffer`; the buffer is `Put` **only on full success**. So a single-shot,
atomic object create — no streaming writer, no abort, no half-finalized objects —
and we never cache a truncated clip, which incidentally neutralizes ADR 0002's
"mid-stream errors leave unrecoverable truncated audio" wart for the persisted
copy. The cost is a transient ~14 MB buffer per in-flight synthesis — negligible
at this scale; streaming upload stays a later optimization if memory shows
pressure. Two concurrent misses for the same key both `Put`; last-write-wins is
harmless — the bytes are identical (content-addressed, immutable).

### 3. Store PCM now; Opus is a planned second phase

Stored/served audio is **raw PCM** today. A hit is therefore byte-identical to a
miss (`audio/L16;rate=24000`), so it needs **no encoder, no new dependency, and
no frontend change**. The deliberately-deferred next phase is **Opus (~32 kbps,
~14× smaller)**, which is the only thing that meaningfully cuts the egress bill,
the per-byte replay charge (§6), and storage — but it carries an ffmpeg
dependency (against ADR 0007's ethos), a buffer-then-encode write path (§2 would
gain a memory buffer), and a frontend `decodeAudioData` branch. We build PCM
first and add Opus when the egress saving is worth those costs. Until then the
egress charge (§6) is simply sized against the larger PCM bytes — correctness is
unaffected; replays just cost ~14× more than they eventually will (small in
absolute terms, and EDoS stays bounded).

### 4. Read on a hit: proxy, never signed URLs

The backend reads the stored PCM and streams it to the client itself, as
`audio/L16` — the wire the frontend already consumes, so the existing `streamTTS`
path is unchanged. Signed GCS URLs were rejected: they mint a bearer capability
that **escapes the auth + allowlist gate** and cannot be per-user rate/charge
bounded — the worst option under the EDoS framing that drives this ADR. Proxying
keeps every byte under the same gate as a debate read and keeps the single `/tts`
endpoint. The GCS bucket is co-located with Cloud Run so the GCS→Cloud Run hop is
free and proxying adds no egress over a signed URL — only trivial instance time.

### 5. Storage seam mirrors the Repository pattern

A `NarrationStore` interface (in-memory | GCS) is selected by the existing
`STORAGE_BACKEND` switch, exactly like `debate.Repository` and `quota.Store`; the
GCS branch requires a new `TTS_BUCKET` and authenticates via ADC like Firestore.
**Local dev and CI use the in-memory store — no GCS, no ADC, no GCP account** —
and with PCM there is no encoder, so there is nothing else to stub. (The Opus
phase will add a second seam: ffmpeg in prod, a passthrough/PCM encoder
locally/CI, so it too stays out of the local path.)

### 6. EDoS coupling: persistence is not deploy-safe alone

Because replays are free of synthesis, replay egress must be bounded separately.
The control is a **per-byte egress charge applied on every call — miss and hit —
calibrated to real egress (~$0.12/GB plus margin)**, re-coupling bytes to the
quota. It must be per-byte (a flat fee lets an attacker loop the longest clip)
and ≥ real egress (a too-small fee under-bounds it). **Calibrating and wiring
this charge is Phase-2 work (extends
[ADR 0009](0009-cost-based-per-user-quota.md)) and is a hard prerequisite to
deploying the persistence in this ADR.** This deliberately reverses the earlier
plan's "billable = cache miss only": a replay costs egress. (Opus, when it lands,
shrinks this charge ~14×, recovering most of the "free replay" feel.)

## Consequences

### Positive

- Each unique narration is synthesized **once, ever** — no re-paying the provider
  on reload / second device / return visit.
- Cross-session replay is **instant and seekable**, served from storage.
- ADR 0002's truncated-audio failure mode is moot for the persisted copy.
- Phase 1 adds **no encoder and no frontend change**; local dev / CI gain **no**
  new dependency. ADR 0007's promise holds.

### Negative

- One genuinely new **prod** dependency — GCS (`cloud.google.com/go/storage`,
  same family as the existing Firestore client).
- **Persistence is not deploy-safe until the §6 egress charge lands.** It may be
  built and run undeployed before then.
- A replay now **costs egress** against quota (reversing "billable = cache miss
  only"), and at PCM size that charge is ~14× larger than it will be after the
  Opus phase. Bounded and small in absolute terms, but real.

### Deferred / rejected alternatives

- **Opus compression — deferred, not rejected** (§3). The intended end state; the
  ~14× saving is deferred only to keep Phase 1 dependency-free.
- **gzip/brotli transport compression instead of a codec — rejected.** It's
  transparent (no ffmpeg, no frontend change), but lossless and audio-blind:
  ~1.2–1.5× on PCM, versus Opus's ~12–16×. PCM has no byte-level redundancy to
  exploit, and only a *lossy/perceptual* codec can discard inaudible data. It is
  not a substitute for the codec; recorded so it isn't re-proposed.
- **Ephemeral + quota only (no persistence).** EDoS-immune with one control, but
  re-pays the provider and re-charges the user on every cross-session replay.
  Rejected in favor of the provider-cost saving and replay UX, with the egress
  charge as the added control.
- **Signed GCS URLs / public bucket** — see §4.
- **WAV instead of raw PCM** — WAV's only edge is a self-describing, playable
  file (the de-scoped archive use); for our own decoder it adds nothing, and raw
  PCM keeps a hit byte-identical to a miss.
- **Async encode worker + queue** (relevant once Opus lands) — 14 MB of PCM does
  not fit a Pub/Sub message, forcing a GCS staging round-trip, a second
  deployable, and eventual-consistency lag on near-term replays. Over-engineered;
  the storage layout is identical, so it stays a cheap future refactor.
- **Age-based GCS lifecycle TTL** — deletes live audio, turning legitimate old
  replays back into paid re-syntheses to save pennies. Narrations are kept
  forever; cleanup is deferred to a future debate-delete (delete the `{debateID}/`
  prefix).

## Implementation

- `backend/internal/tts/` — `NarrationStore` (`Open`/`Put`) interface, in-memory
  + GCS impls, content-addressed keying, and the buffer-and-Put tee layered onto
  `SynthesizeStream`.
- [backend/internal/server/handler_tts.go](../backend/internal/server/handler_tts.go)
  — serve-if-exists (proxy PCM) else synthesize-stream-and-store; the per-byte
  egress charge (Phase 2).
- [backend/main.go](../backend/main.go) — `NarrationStore` construction under the
  `STORAGE_BACKEND` switch; new `TTS_BUCKET`.
- [backend/internal/config/config.go](../backend/internal/config/config.go) —
  `TTS_BUCKET` accessor; egress-rate accessor (Phase 2).
