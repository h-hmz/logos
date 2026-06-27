# ADR 0013: Meter TTS by reserve-then-settle on a byte-derived cost

Date: 2026-06-15 (amended 2026-06-27 — §6 added: surfacing the synthesis cost)
Status: Accepted (extends [ADR 0009](0009-cost-based-per-user-quota.md); depends on [ADR 0012](0012-narration-persistence.md))

## Context

[ADR 0012](0012-narration-persistence.md) persists narrations and is **not
deploy-safe until TTS is metered** (§6): persistence decouples *getting bytes*
from *paying*, so replay egress is unbounded otherwise, and synthesis itself has
never been metered at all. This ADR closes both holes. TTS differs from the
debate metering of [ADR 0009](0009-cost-based-per-user-quota.md) in three ways
that reshape the design:

1. **No provider-reported cost.** OpenRouter returns no `usage.cost` for
   `/audio/speech` (unlike `/chat/completions`, which ADR 0009 leans on). Cost
   must be derived locally.
2. **Cost is estimable from input.** Audio duration tracks input text length, so
   a per-request cost can be predicted up front — which it cannot for an LLM
   whose output length is unknown. ADR 0009's stated reason for rejecting
   up-front reservation ("output length unpredictable; needs a price table we
   don't maintain") therefore does not apply to TTS.
3. **Zero concurrent overshoot is required.** ADR 0009 accepts a user exceeding
   their budget by up to N generations across N instances (its in-flight lock is
   process-local). For TTS we want that overshoot to be **zero**.

## Decision

### 1. Two-component, byte-derived charge

Charge **synthesis + egress** on a miss, **egress** on a hit. Both are exact
functions of the byte count the handler already has (`buf.Len()` on a miss via
the tee, the `io.Copy` count on a hit). The format is fixed (24 kHz × 16-bit
mono = 48000 B/s) and Gemini bills 25 audio tokens/s, so **1 audio token =
1920 bytes**:

```
audioTokens   = bytes / 1920
synthCostUSD  = audioTokens * USD_PER_AUDIO_TOKEN        // miss only
egressCostUSD = bytes / 1e9   * USD_PER_EGRESS_GB        // every call
```

Both are per-byte, never flat — a flat fee would let an attacker loop the
longest cached clip. Post-hoc this cost is **exact**, not estimated.

### 2. Reserve-then-settle (the overshoot guarantee)

A lock cannot deliver zero overshoot: ADR 0009's `InFlight` is process-local, so
a script spread across Cloud Run instances slips past it. Instead:

1. **Pre-flight estimate.** Predict cost from input length —
   `est = (chars / charsPerSec) * 25 * USD_PER_AUDIO_TOKEN * margin (+ egress)` —
   with `charsPerSec` from ADR 0002's ~13 chars/s heuristic and a `margin`
   chosen so the estimate reliably **exceeds** the actual (duration varies
   ±20–30% with prosody, the style directive, and inline tags).
2. **Atomically reserve** that estimate in the shared quota store *before*
   streaming, using the same optimistic version-checked write the store already
   uses for `Add` (ADR 0009/0004). This is **global**, not a new distributed
   lock: concurrent fan-out depletes the budget transactionally and excess
   requests get **402** — across instances, because the transaction serializes
   globally.
3. **Stream**, then **reconcile** to the exact byte-derived cost via
   `Add(actual − reserved)`, deferred so it always runs (full refund if
   synthesis produced no bytes).

Because the margin keeps `actual ≤ reserved`, the reconcile only ever *refunds*,
so cumulative reservations can never exceed the limit: **overshoot is zero, even
concurrent, even cross-instance.**

### 3. Store gains a `Reserve`; everything else fails open

`quota.Store` gains `Reserve(ctx, uid, amountUSD, initialLimit) (reserved float64, err error)`
— a transactional check-and-increment returning `ErrOverBudget` when
unaffordable. Settlement reuses the existing `Add`. A `Reserve` **store error
fails open** (allow, reserve nothing, charge actual after) — during a store
outage concurrent overshoot reopens, accepted because an outage already breaks
generations (ADR 0009).

### 4. Charge the caller; synthesis stays in the handler

The charge falls on the authenticated caller UID with **no owner check** —
matching the tutor precedent and ADR 0012's "reads open to anyone with the ID."
A narration mutates no debate state, so it does not inherit the `/next` owner
gate. Synthesis stays in the handler (ADR 0002); the Service exposes only a thin
reserve/settle pair over the quota store.

### 5. Rates are configuration; estimator constants are not

`USD_PER_AUDIO_TOKEN` (or per-1M, matching the pricing page) and
`USD_PER_EGRESS_GB` are env vars, validated like `QUOTA_USD_LIMIT`, with no
pricing baked into code. `charsPerSec` and `margin` are internal constants — they
bound, they don't price.

### 6. Surfacing the synthesis cost: derived on read, never persisted

The frontend shows, per narration, the **synthesis** component of §1 only —
egress is a per-play billing concern that never surfaces. Because synthesis cost
is a pure function of the stored clip's byte count — the same property that makes
the key content-addressed ([ADR 0012](0012-narration-persistence.md)) — it is
**derived on read from the blob's size, never persisted.** The
`GET /debates/{debateID}/narrations` status endpoint, which already fans out a
per-step presence check, now returns each cached step's synthesis cost beside
`cached`, computed from the object's byte length; `NarrationStore` gains a
size-returning `Stat` in place of the bare `Exists` (GCS `Attrs`, memory `len` —
still no download). An unsynthesized step has nothing in the store to measure, so
it shows no cost; the frontend never estimates.

This is a **deliberate asymmetry** with LLM cost, which is persisted frozen in
`debate.StepRecord`. Narration synthesis cost is recomputed from bytes at current
rates (§5), so a later rate change retroactively shifts the *displayed* figure —
accepted because it is informational, the real charge was already settled to the
purse (§2), and the magnitude is sub-cent. Persisting the cost into `StepRecord`
at synthesis was rejected: it would store a value already derivable from bytes we
keep regardless, force a debate-state write onto the stateless narration path
(§4, ADR 0012), and pull a TTS concept into the `debate` package the API surface
keeps separate. So the `debate` package stays free of TTS, and the figure is
owner-agnostic — what the narration cost to *make*, shown identically to owner
and guest, never a per-viewer "what this play cost you".

## Consequences

### Positive

- **Zero overshoot** under normal operation, including concurrent and
  cross-instance — the property a lock could not provide.
- **Exact charging.** The byte count gives the exact audio-token count; no
  estimation survives into the recorded spend.
- Closes both holes ADR 0012 left open (unmetered synthesis + free-replay
  egress) with one mechanism.
- **The `debate` package stays free of TTS.** Surfacing reuses §1's byte-derived
  synthesis cost on the read path and persists nothing, so the debate↔tts
  boundary holds (§6).

### Negative

- **A new store operation and a reconcile step.** More moving parts than ADR
  0009's allow-then-charge; the reconcile must be deferred to always release the
  reservation.
- **Over-reservation temporarily holds budget.** The margin reserves more than
  the actual cost; the excess is refunded at settle, but briefly counts against
  the user. Negligible at sub-cent costs against a dollar budget.
- **A hard crash between reserve and settle is not refunded.** The reservation is
  durable (it must be, to bound cross-instance concurrency) but the settle is
  in-process and deferred, so an OOM/SIGKILL mid-synthesis leaves the user
  charged the inflated estimate with no refund and partial/no audio. Graceful
  shutdown (SIGTERM → `httpServer.Shutdown` drain) lets the in-flight handler
  finish and reconcile, so the exposure is hard kills only; magnitude is one
  inflated estimate (cents), and — unlike ADR 0009's accepted lost *deduction* —
  it is **user-unfavorable**. Accepted at this scale; the fix, if it ever bites,
  is a durable pending-reservation record swept on restart.
- **Fail-open reopens overshoot during a store outage.** Accepted, per ADR 0009.
- **Deliberate divergence from ADR 0009.** The debate path allow-then-charges and
  tolerates N-instance overshoot; TTS reserves-then-settles for zero. Two
  metering models, justified by TTS being estimable (§Context).
- **Displayed synthesis cost drifts with rates.** The surfaced per-narration
  figure is derived on read at current rates (§6), not frozen like LLM cost in
  `StepRecord`; a rate change retroactively moves old narrations' *shown* cost.
  Informational only — the settled charge is untouched — and sub-cent.

## Implementation

- `backend/internal/quota/` — `Store.Reserve` (transactional check-and-increment)
  on both `MemoryStore` and `FirestoreStore`; `ErrOverBudget` reused.
- `backend/internal/debate/service.go` — thin `ReserveNarration` / `SettleNarration`
  over the quota store.
- [backend/internal/server/handler_tts.go](../backend/internal/server/handler_tts.go)
  — pre-flight estimate → reserve (402 on `ErrOverBudget`, before headers) →
  stream → deferred settle on the exact byte count.
- [backend/internal/config/config.go](../backend/internal/config/config.go) —
  `USD_PER_AUDIO_TOKEN` and `USD_PER_EGRESS_GB` accessors, validated.
- [backend/internal/tts/store.go](../backend/internal/tts/store.go) —
  `NarrationStore.Stat` (byte size + presence) replacing the `Exists` call site;
  GCS `Attrs`, memory `len`.
- [backend/internal/server/handler_tts.go](../backend/internal/server/handler_tts.go)
  — `narrationsStatus` returns per-step `{cached, synthesis_cost_usd}`, the cost
  being §1's synthesis component over the stored byte count.
- `frontend/` — narrations endpoint becomes per-step objects; the `PlayButton`
  "saved" badge shows the cost; `onSynthesized` refetches the status to fill a
  freshly synthesized step.
