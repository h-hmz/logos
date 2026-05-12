# ADR 0001: Debate storage via Repository + per-request session hydration

Date: 2026-04-19
Status: Accepted (§5 superseded by [ADR 0004](0004-concurrency-repo-occ.md))

## Context

The backend originally kept active debate state in a process-local map
(`debate.Store`) whose entries held live `agent.Agent` instances and an
ADK `session.Service` created once per debate. This worked for local
single-instance use but breaks on the deployment target — **Google Cloud
Run**:

- Cloud Run instances are ephemeral (scale-to-zero, cold starts, routine
  recycling). In-memory maps are wiped between requests.
- Requests do not have sticky routing by default, so two `/debates/{id}/next`
  calls for the same debate can land on different instances.
- An in-instance `sync.Mutex` cannot serialize work across instances.

We also want to minimize cost (this is a hobby-scale deployment) and avoid
committing to a heavy persistence abstraction before we know we need one.

The ADK Go library ships only `session.InMemoryService()`; there is no
built-in Firestore-backed `session.Service`.

## Decision

Introduce a `debate.Repository` abstraction over debate persistence and
make handlers **stateless across requests** by rebuilding the ADK session
on every request from persisted domain state.

### 1. Split `State` into `Persisted` + `Runtime`

- `Persisted` — JSON-serializable snapshot. Stores debater *names* (not
  full `philosopher.Data`), `NumRounds`, `ModelID`, `MaxWords`, `Current`,
  token counters, `StepOutputs map[string]string`, and a `Version` for
  optimistic concurrency.
- `Runtime` — per-request working copy: embeds `*Persisted` plus the
  non-serializable pieces (`Steps []Step`, `SessionService`, `SessionID`,
  resolved `philosopher.Data` for each debater).

Storing names rather than full philosopher data keeps the document small
and decouples the persisted record from the philosopher catalog; prompt
edits to a philosopher automatically apply to in-flight debates.

### 2. `Repository` interface

```go
type Repository interface {
    Get(ctx, id) (*Persisted, error)
    Create(ctx, *Persisted) error
    Update(ctx, *Persisted) error // optimistic: fails on Version mismatch
}
```

Memory impl (`MemoryRepo`) is provided now; a Firestore impl will follow
in a later change. Errors: `ErrNotFound`, `ErrAlreadyExists`,
`ErrVersionConflict`.

### 3. `Hydrate(ctx, p, philMap) (*Runtime, error)`

Per request, handlers call `Hydrate` which:

1. Resolves debater names against the in-process philosopher catalog.
2. Calls `BuildSteps(...)` to construct pipeline agents (cheap —
   `modelcache` keeps the Gemini client warm within an instance).
3. Creates a fresh `session.InMemoryService()` and calls `Create` with a
   state map seeded from `topic` and every entry in `StepOutputs`.

The session lives for one request and is discarded afterward.

### 4. Why not persist ADK sessions themselves?

Two options were considered for ADK session persistence:

- **(a) Custom Firestore-backed `session.Service`** — one doc per ADK
  event. Preserves full event-level fidelity (tool calls, intermediate
  events, state deltas).
- **(b) Rebuild the session each request from step outputs** — persist
  only domain state; reseed a fresh in-memory session per request.

We chose **(b)**. Rationale:

- The server already treats *step outputs* (the final text per pipeline
  step) as the unit of progress; no intermediate ADK events are
  consumed elsewhere.
- Option (b) needs zero ADK-internal knowledge — no risk of drift if
  ADK's event/state-delta semantics change.
- Handlers become stateless, which is the native Cloud Run shape.
- Cost is negligible: one Firestore read + one write per `/next` call,
  dominated by the LLM call by orders of magnitude. `InMemoryService`
  itself is a map with a mutex; per-request allocation is microseconds.
- Option (a) remains available later if event-level history becomes
  a product requirement.

### 5. Concurrency

> **Superseded by [ADR 0004](0004-concurrency-repo-occ.md).** The
> in-instance mutex described below has been removed; correctness now
> relies solely on `Persisted.Version` checked by `repo.Update`, with
> a 409 conflict response on `/next`.

Within an instance, a `sync.Map` of per-debate mutexes serializes
concurrent `/next` calls for the same debate. Across
instances, `Persisted.Version` is intended to carry optimistic
concurrency control (implemented in `MemoryRepo`; Firestore impl will
do the same via transaction).

If cross-instance contention becomes real (unlikely at our scale —
only one user acts on a debate at a time), handlers can retry on
`ErrVersionConflict` or surface 409 to the client.

### 6. `BuildStepMeta` / `TotalSteps`

`createDebate` needs the pipeline's step keys and labels but **not**
the agents. `BuildStepMeta(a, b, numRounds)` derives them with pure
string formatting. This avoids allocating agents (and the associated
instruction templates) when only metadata is needed.
`TotalSteps(n) = 5 + 2n` is the closed-form count.

## Consequences

### Positive

- Cloud-Run-safe: no in-memory state required across requests.
- Storage is pluggable via one interface with two errors worth handling.
- Per-request overhead on top of the LLM call is sub-millisecond.
- Tests no longer need to fabricate `Step` lists; they build
  `Persisted` records and let `BuildStepMeta` derive step keys.

### Negative

- Handlers re-run `BuildSteps` on each `/next`, re-allocating agent
  objects. Cost is small (no LLM calls) but non-zero.
- Full ADK event history is lost — only finalized step text survives.
  Intermediate events (token-level streaming, tool calls if we add
  them) will need a separate persistence path if ever required.
- In-instance locking is best-effort across instances. The
  `Persisted.Version` field carries the real guarantee; handlers must
  be prepared to see `ErrVersionConflict` once a Firestore impl lands
  with multi-instance traffic.
- The philosopher catalog must contain both debaters at hydration
  time; renaming a philosopher would orphan in-flight debates. This
  is acceptable given the catalog is developer-managed and rarely
  changes.

## Implementation

- [backend/internal/debate/debate.go](../backend/internal/debate/debate.go) —
  `Persisted`, `Runtime`, `Repository`, `MemoryRepo`, `Hydrate`,
  `BuildStepMeta`, `TotalSteps`.
- [backend/internal/server/server.go](../backend/internal/server/server.go) —
  handlers refactored around `repo.Get` → `Hydrate` → run → `repo.Update`.
- [backend/main.go](../backend/main.go) — wires `NewMemoryRepo()`.