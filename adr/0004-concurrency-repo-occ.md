# ADR 0004: Concurrency via repository OCC, no in-instance mutex

Date: 2026-04-19
Status: Accepted

Supersedes the concurrency choice in [ADR 0001 §5](0001-debate-storage-repository-pattern.md#5-concurrency).

## Context

ADR 0001 established `Persisted.Version` as the real cross-instance
guarantee but kept an in-instance `sync.Map` of per-debate mutexes in
`server.go` as a belt-and-braces measure, and deferred the decision of
how handlers should react to `ErrVersionConflict` ("retry or 409").

Two problems with keeping the in-instance lock:

- It provides zero protection once Cloud Run scales past one instance —
  a request for the same debate can land on a peer with an independent
  mutex map. It creates the illusion of safety without the substance.
- It serializes the entire handler, including the multi-second LLM
  call in `/next`, across concurrent requests to the same debate on
  one instance. With repo-level OCC in place, that serialization is
  unnecessary.

We also need a concrete policy for `ErrVersionConflict` per endpoint.

## Decision

**Remove the in-instance per-debate mutex entirely.** Correctness is
carried by `repo.Update`'s version check — implemented identically in
`MemoryRepo` (local dev, `sync.Mutex` around a map) and `FirestoreRepo`
(transactional read-check-write).

**Per-endpoint conflict policy:**

- `/debates/{id}/next` — on `ErrVersionConflict`, return **409
  Conflict**. The LLM generation that preceded the failed commit used
  a stale view of the debate (e.g. missed a concurrent interjection);
  silently retrying would burn tokens and may produce a turn
  inconsistent with the new state. The client refetches and decides.
- `/debates/{id}/interject` — on `ErrVersionConflict`, **retry up to
  5 times** in-handler. The read-modify-write is pure CPU (validate
  the gap, append an interjection, format pending text); redoing it
  against fresh state is cheap and transparent to the client. Only
  return 409 if every attempt conflicts.

## Consequences

### Positive

- Handlers are truly stateless. Scaling up no longer changes
  correctness guarantees, only the rate of conflicts.
- On a single instance, concurrent `/next` calls for the same debate
  can now run their LLM work in parallel; only the commit is
  serialized. (Wasted tokens on the loser, but latency for the winner
  is unchanged.)
- One fewer concurrency primitive in the handler layer — the
  `sync.Map` of mutexes was extra state that had to stay consistent
  with the lifetime of debates.

### Negative

- Concurrent `/next` on one debate will waste one LLM call's tokens
  when both commits race. At our scale (one user per debate) this is
  not expected to happen; if it does, a short-lived "in-progress"
  marker in Firestore could be added to short-circuit before
  generation.
- Clients now need to handle 409 on `/next`. Practically, the frontend
  already serializes `/next` calls per debate, so this is a
  defence-in-depth response rather than a hot path.

## Implementation

- [backend/internal/server/server.go](../backend/server/server.go) —
  removed `locksMu`, `locks`, `lockFor`; `nextTurn` returns 409 on
  `ErrVersionConflict`; `interject` retries up to 5 times.
- Repository OCC unchanged — already shipped in ADR 0001
  ([debate.go](../backend/internal/debate/debate.go),
  [firestore_repo.go](../backend/internal/debate/firestore_repo.go)).
