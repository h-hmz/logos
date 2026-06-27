# Architecture

A guide to understand the building blocks that make this codebase. For setup, env vars, API contracts, and the YAML format see [README.md](README.md). For the rationale behind specific decisions see the [ADRs](adr/). This document is the bridge between them — what the moving parts are, how they fit together, and how a request flows.

---

## Bird's-eye view

Logos is a turn-based philosophy debate engine. Two AI philosophers argue a topic across configurable rounds, a critic reviews their reasoning, each philosopher responds to the critic, and a judge issues a structured verdict.

The whole backend is one HTTP service in Go. **Each request advances the debate by exactly one step.** There is no in-memory session — every request loads the full debate from storage, runs one step, and writes it back. That single property drives almost every other shape in the code.

---

## The architectural commitment

The backend runs on Cloud Run with no sticky sessions. That means:

- A request for debate X may land on a process that has never seen debate X before.
- Two requests for the same debate may land on two different processes simultaneously.
- A process may be killed mid-debate; the next request hits a fresh one.

Three patterns exist to make that work:

1. **Stateless hydration.** The on-disk shape (`Persisted`) is the source of truth. The in-memory working shape (`Runtime`) is rebuilt fresh on every request from `Persisted` via [`debate.Hydrate`](backend/internal/debate/debate.go). When the request finishes, the working shape is discarded.
2. **Optimistic concurrency.** Every `Persisted` carries a `Version`. Writes happen inside a Firestore transaction that rejects on version drift. Conflicts surface as HTTP 409 — see [ADR 0004](adr/0004-concurrency-repo-occ.md).
3. **`{key}` substitution and `OutputKey` writes.** Inter-step communication runs through a per-request `agent.State` map, not through function arguments. Agents read prior step outputs by template (`{socrates_r1}`) and write their own output back by key. Every request reseeds this map from the debate's `StepRecords`. See [ADR 0003](adr/0003-state-key-substitution.md).

Anything that breaks one of these three breaks the architecture. Refactors should preserve them.

---

## Code map

Top-level entry points and what each package is for. The detailed file-by-file map lives in [README.md § Project layout](README.md#project-layout).

| Package | Responsibility |
|---|---|
| [`backend/main.go`](backend/main.go) | Process entry point. Loads config; builds the auth verifier + email/domain allowlist, the debate repository, the per-user quota store, and the narration store; initializes telemetry; wires the HTTP server. The `run(ctx, w, args)` shape is for testability. |
| [`internal/agent/`](backend/internal/agent/) | The in-house agent layer (replaced Google ADK — see [ADR 0008](adr/0008-in-house-agent-layer-replacing-adk.md)). Defines the `Agent` struct, the `LLM` interface, the `State` scratchpad, and `{key}` substitution. Provider-agnostic. |
| [`internal/agent/openai_compat.go`](backend/internal/agent/openai_compat.go) | The production `LLM` implementation, backed by the openai-go SDK against any OpenAI-compatible endpoint (OpenRouter by default — see [ADR 0007](adr/0007-llm-tts-via-openrouter-for-spend-caps.md)). |
| [`internal/debate/`](backend/internal/debate/) | The domain layer. `Persisted`, `Runtime`, `StepRecord`, `Service`. The pipeline builder lives here, not in the agent factories — `pipeline` (enumerated cheaply via `BuildStepMeta`) is the one place that knows the shape of a debate. `Service` also owns the per-user quota gate and the retry path. |
| [`internal/philosopher/`](backend/internal/philosopher/) | Loads YAML philosopher data from disk and exposes the agent factories for every non-tutor pipeline role: `MakePhilosopher`, `MakeModerator`, `MakeCritic`, `MakeJudge`. Prompts are kept here as `text/template` strings rendered via the package-local `render` helper — see [`philosopher.go`](backend/internal/philosopher/philosopher.go). The critic and judge live alongside philosopher because their prompts share the same debater-data shape and the same `{key}` substitution conventions. |
| [`internal/tutor/`](backend/internal/tutor/) | Side-channel tutor agent invoked when a user highlights text. Not part of the pipeline. |
| [`internal/server/`](backend/internal/server/) | HTTP layer: chi router, auth middleware (verifier + allowlist), JSON helpers. Handlers translate HTTP into `Service` calls and back; ownership and quota checks live in the service, not here. They contain no business logic. |
| [`internal/tts/`](backend/internal/tts/) | Text-to-speech synthesis routed through the same OpenAI-compatible base URL as the LLM ([ADR 0002](adr/0002-tts-provider-interface.md)), plus content-addressed narration persistence ([ADR 0012](adr/0012-narration-persistence.md)) and byte-derived cost metering ([ADR 0013](adr/0013-tts-cost-metering.md)). |
| [`internal/quota/`](backend/internal/quota/) | Per-user USD spend metering: a `Store` (in-memory \| Firestore) plus a process-local in-flight lock. Debate generations allow-then-charge; TTS reserves-then-settles for zero overshoot. See [ADR 0009](adr/0009-cost-based-per-user-quota.md), [ADR 0013](adr/0013-tts-cost-metering.md). |
| [`internal/models/`](backend/internal/models/) | The model allowlist (`models.Allowed`) — the sole authority over which models a generation may run on. See [ADR 0011](adr/0011-client-supplied-models.md). |
| [`internal/telemetry/`](backend/internal/telemetry/) | OpenTelemetry SDK setup; a no-op when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset. |
| [`internal/auth/`](backend/internal/auth/), [`internal/config/`](backend/internal/config/) | Firebase ID token verifier (with a no-op variant for local dev — see [ADR 0006](adr/0006-auth-firebase-verifier.md)), the fail-closed email/domain `Allowlist`, and `.env` loading. |

The dependency direction is:

```
server  →  debate  →  agent
              ↘  philosopher / tutor  →  agent
```

`agent` depends on nothing in this project. `debate` and `quota` are the packages that talk to persistence (debate state and per-user spend, respectively). `server` is the only package that knows about HTTP.

---

## Key abstractions

The mental model fits in **four nouns** and **two verbs**.

### The four nouns

| Type | Lives in | Lifetime | Purpose |
|---|---|---|---|
| [`debate.Persisted`](backend/internal/debate/debate.go) | Firestore (or `MemoryRepo`) | Forever, until the debate is deleted | The dumb snapshot. JSON/Firestore-shaped. `OwnerUID`, topic, debater names, `StepRecords` (each step's content, model, interjection, tokens, cost), `Version`. `Current()` is derived as `len(StepRecords)`. Knows nothing about agents or LLMs. |
| [`debate.Runtime`](backend/internal/debate/debate.go) | One request | Built by `Hydrate`, discarded when the handler returns | The working copy. Embeds `*Persisted` and adds the unserializable bits: resolved philosopher data, the `[]Step` agent pipeline, the `agent.State` scratchpad. |
| [`debate.Step`](backend/internal/debate/debate.go) | Inside `Runtime.Steps` | Same as Runtime | One stage of the pipeline. Holds the constructed `*agent.Agent` to run. The companion `StepMeta` is the same shape minus the agent — used wherever you need the *shape* of the pipeline cheaply, without paying to build agents. |
| [`agent.State`](backend/internal/agent/state.go) | Inside `Runtime.State` | Same as Runtime | A `map[string]string` scratchpad keyed by step key. Reseeded on every hydrate. The communication channel between agents. |

The split that matters: **`Persisted` is for storage, `Runtime` is for work**. `Hydrate` is the bridge. Anything you can recompute from `Persisted` does *not* belong on `Persisted`.

> **A note on naming.** The diagrams in this section use `StepOutputs[key]` as shorthand for a step's stored text. The real snapshot field is `StepRecords []StepRecord` (see the *four nouns* table above): the text is one field of each record, alongside the model, interjection, tokens, and cost. Persisting a turn *appends* a `StepRecord`; `Current()` derives as `len(StepRecords)` rather than being stored or incremented.

Visually, the three layers and what flows between them:

```
┌──────────────────────────────────────────────────────────────────────┐
│  Persisted        durable, JSON/Firestore-shaped, survives requests  │
├──────────────────────────────────────────────────────────────────────┤
│  ID, OwnerUID, Topic, DebaterAName, DebaterBName, NumRounds, MaxWords│
│  StepRecords       []StepRecord  (one per completed step, in order;  │
│                    each: content, model, interjection, tokens, cost) │
│  Version           int64     (OCC counter, bumped on every Update)   │
│  Current() = len(StepRecords);  totals summed from StepRecords       │
└──────────────────────────────────────────────────────────────────────┘
         │   repo.Get(id)               ▲   repo.Update(p)
         ▼                              │   (Firestore tx, version-check)
┌──────────────────────────────────────────────────────────────────────┐
│  Runtime          built fresh on every request, discarded afterward  │
├──────────────────────────────────────────────────────────────────────┤
│  *Persisted       (embedded — Runtime and Persisted share fields)    │
│  DebaterA, DebaterB    philosopher.Data  (resolved from catalog)     │
│  Steps                 []Step  (full pipeline, one agent per step)   │
│  State                 agent.State                                   │
└──────────────────────────────────────────────────────────────────────┘
         │   Hydrate seeds State                ▲
         │   from Topic + StepOutputs           │   Agent.Run reads & writes
         ▼                                      │
┌──────────────────────────────────────────────────────────────────────┐
│  agent.State      map[string]string, the per-request scratchpad      │
├──────────────────────────────────────────────────────────────────────┤
│  "topic"                       ← Persisted.Topic                     │
│  "<step_key_for_each_done>"    ← Persisted.StepOutputs[<key>]        │
│                                                                      │
│  Read by:    Agent.Run via Substitute("...{key}...")                 │
│  Written by: Agent.Run sets State[OutputKey] = result.Text           │
└──────────────────────────────────────────────────────────────────────┘
```

### The two verbs

These are how agents talk to each other across pipeline steps. There is no orchestrator passing data around — instead:

- **`{key}` substitution.** An agent's instruction is a template. Before calling the model, [`agent.Substitute`](backend/internal/agent/state.go) replaces every `{foo}` with `state["foo"]`. This is how the round-2 philosopher prompt sees the round-1 opponent's argument: the prompt literally contains `{socrates_r1}`, and `state["socrates_r1"]` was filled at hydrate time from the stored `socrates_r1` step record. Missing keys resolve to the empty string.
- **`OutputKey` writes.** When [`Agent.Run`](backend/internal/agent/agent.go) finishes, it writes the result into `state[OutputKey]`. The handler then appends a `StepRecord` for `step.Key` and saves.

End-to-end, the inter-step protocol traverses two HTTP requests. Watch how `socrates_r2`'s output reaches `kant_r2`:

```
┌─── REQUEST N  (running step "socrates_r2") ──────────────────────────┐
│                                                                      │
│  client ───►  POST /debates/{id}/next                                │
│                                                                      │
│   repo.Get(id)        →  Persisted { Current: 3, Version: 5,         │
│                                      StepOutputs: { kant_r1: ... } } │
│   Hydrate(p, philMap) →  Runtime + State                             │
│       State = { topic: "...", kant_r1: "...rational ethics..." }     │
│                                                                      │
│   step = Runtime.Steps[3]              (the "socrates_r2" agent)     │
│   step.Agent.Run(ctx, State, ...)                                    │
│       ├─ system = Substitute(instruction, State)                     │
│       │     "...{topic}..."     →  "...free will..."                 │
│       │     "...{kant_r1}..."   →  "...rational ethics..."           │
│       ├─ LLM.Generate(...)      →  returns full text + usage         │
│       └─ State["socrates_r2"] = result.Text   ◄── OutputKey write    │
│                                                                      │
│   p.StepOutputs["socrates_r2"] = result.Text  ◄── copy into snapshot │
│   p.Current = 4                                                      │
│   repo.Update(p)        →  Firestore tx: Version 5 → 6               │
│                                                                      │
│  client ◄───  200 OK {payload}                                       │
└──────────────────────────────────────────────────────────────────────┘

      (request finishes; Runtime + State garbage-collected)

┌─── REQUEST N+1  (running step "kant_r2") ────────────────────────────┐
│                                                                      │
│  client ───►  POST /debates/{id}/next                                │
│                                                                      │
│   repo.Get(id)        →  Persisted { Current: 4, Version: 6,         │
│                                      StepOutputs: { ...,             │
│                                          socrates_r2: "..." } }      │
│   Hydrate(p, philMap) →  fresh Runtime + State                       │
│       State["socrates_r2"] ← p.StepOutputs["socrates_r2"]            │
│                                              ◄── seeded for lookup   │
│                                                                      │
│   step = Runtime.Steps[4]              (the "kant_r2" agent)         │
│   step.Agent.Run(ctx, State, ...)                                    │
│       └─ {socrates_r2} in kant_r2's instruction template now         │
│          resolves to socrates' previous turn — without any explicit  │
│          message-passing across requests.                            │
└──────────────────────────────────────────────────────────────────────┘
```

Once you internalize this two-request loop, the rest of the codebase is mostly plumbing.

---

## Request lifecycles

Four kinds of request matter. Three drive the pipeline; one is a side channel.

### `POST /debates` — Create

Handler → [`Service.CreateDebate`](backend/internal/debate/service.go).

1. Validate input (topic non-empty, both philosophers exist, distinct, rounds in range).
2. Apply defaults (`num_rounds=2`, `max_words=250`).
3. Build a fresh `Persisted` with a UUID, the caller's UID as `OwnerUID`, and no `StepRecords`.
4. `repo.Create(p)` — sets `Version=1`.
5. Return the record + a `BuildStepMeta` manifest (cheap — no agents constructed).

**No agents are built on this path.** That's intentional: agent construction reads env vars, formats kilobyte-scale instructions, and registers LLM clients. We defer it to the first `/next` call.

### `POST /debates/{id}/next` — Advance one step (the hot path)

Handler → [`Service.NextTurn`](backend/internal/debate/service.go).

Before any generation work the service runs two gates: it checks the caller is the debate's **owner** (`ErrForbidden` → 403), then acquires the per-UID in-flight lock and budget check (`ErrGenerationInFlight` → 409, `ErrOverBudget` → 402). On the SSE path these run before the stream opens, so the rejections are real HTTP statuses rather than SSE error events.

1. **Load.** `repo.Get(debateID)` returns a `*Persisted`.
2. **Bounds check.** If `Current >= TotalSteps(NumRounds)`, return a validation error.
3. **Hydrate.** [`Hydrate(p, philMap)`](backend/internal/debate/debate.go) builds the `Runtime`: resolves debater data and seeds `State` with `topic` and the content of every prior `StepRecord`. Agents are built lazily — only the one step the turn runs (the pipeline's other `build` closures are never invoked).
4. **Run the agent.** `step.Agent.Run(ctx, rt.State, kickoffMessage(role))`. Inside, this:
   - Substitutes `{key}` placeholders against `State`.
   - Builds a 2-message request (system + a role-specific kickoff user message, e.g. `"Open the debate now."`).
   - Calls `LLM.Generate`, which returns the full completion text and token usage.
   - Returns `RunResult{Text, Usage}`. Side effect: `state[OutputKey]` is updated.
5. **Persist the turn.** Append a `StepRecord` for `step.Key` (content, model, token counts, cost); `Current()` advances automatically as `len(StepRecords)`; `repo.Update`.
6. **Build the response.** Map step key to a frontend-facing `Role` (`"a"`, `"b"`, `"moderator"`, `"critic"`, `"judge"`). For `critic_feedback` and `final_verdict`, the agent emits structured JSON directly as the step `content`, which the frontend renders section by section.

The per-step agent build happens on every request. That sounds expensive but isn't: the `LLM` client is cached process-wide by [`agent.Get`](backend/internal/agent/registry.go), so per-request work is just struct allocation and string formatting on instruction templates.

### `POST /debates/{id}/retry` — Re-roll the last step

Handler → [`Service.RetryTurn`](backend/internal/debate/service.go). Reuses the same owner check, in-flight lock, budget gate, and SSE path as `/next` (see [ADR 0010](adr/0010-retry-last-step.md)).

1. Validate the named `step_key` is the debate's current **last** record (else `ErrNotLastStep` → 409); an empty key, or an empty debate with nothing to retry, is a 400.
2. Run the generation on a clone with that last record popped, so it sees every prior step but not its own old output. The named model re-rolls it (it may differ from the original).
3. On success, replace the record in place with a single optimistic-version `repo.Update`; a failed re-roll leaves the original record — and the whole debate — untouched.

Allowed even when the debate is complete — that is how the judge's verdict gets re-rolled. The discarded generation's provider cost stays on the owner's quota; only the kept record counts toward the debate's displayed total.

### `POST /debates/{id}/tutor` — Side channel

Handler → [`Service.TutorExplain`](backend/internal/debate/tutor.go).

The tutor is **not** a pipeline step. It does not advance the debate, does not append a `StepRecord`, does not persist any state. The full chat history is held client-side and resent on every request.

1. Validate input (selection length, history size, history role values).
2. `repo.Get` to fetch the debate's step outputs.
3. Locate the selected turn by key; find the immediately preceding non-moderator turn for context.
4. Build a tutor-specific user message via [`tutor.BuildUserMessage`](backend/internal/tutor/tutor.go) that includes the selection, both turn texts, and the chat history.
5. Run the tutor agent and parse the suggestions block out of the final reply, returning the clean content and the suggestions list as separate fields.

---

## Cross-cutting concerns

### Optimistic concurrency control

`Persisted.Version` is incremented on every successful `repo.Update`. The Firestore implementation reads the current version inside a transaction and rejects on drift. `Service.NextTurn` does not retry on conflict (a stale `/next` is the user's problem to retry). Conflicts surface as `ErrVersionConflict` → HTTP 409. See [ADR 0004](adr/0004-concurrency-repo-occ.md).

### LLM registry

[`agent.Get(ctx, modelID)`](backend/internal/agent/registry.go) returns a cached `LLM`, constructing one against the OpenAI-compatible client on first call and caching it process-wide. The `modelID` is supplied on each client request (ADR 0011).

### Auth & ownership

Two layers guard every non-health route. The **verifier** (`AUTH_BACKEND`: a Firebase ID-token verifier, or a no-op for local dev — [ADR 0006](adr/0006-auth-firebase-verifier.md)) authenticates the caller and yields a UID; the **allowlist** (`auth.Allowlist`, parsed from `AUTH_ALLOWLIST`) then authorizes by email/domain and is **fail-closed** — an empty allowlist admits no one. Authorization to *mutate a specific debate* is separate and lives in the service: a debate stores its creator's `OwnerUID`, reads stay open to anyone holding the ID, and only the owner may advance or retry (`ErrForbidden` → 403). See the *Owner* term in [CONTEXT.md](CONTEXT.md).

### Quota & metering

Every generation is metered against the caller's per-UID lifetime USD budget, held in a `quota.Store` (in-memory | Firestore) seeded from `QUOTA_USD_LIMIT`. Debate turns, retries, and tutor replies **allow-then-charge**: a per-UID in-flight lock serializes a user's generations (`ErrGenerationInFlight` → 409), the budget is checked (`ErrOverBudget` → 402), the generation runs, and its provider-reported cost is added afterward ([ADR 0009](adr/0009-cost-based-per-user-quota.md)). TTS instead **reserves-then-settles** on a byte-derived cost — reserving an estimate up front and reconciling to the exact byte count after streaming — for zero overshoot even across instances ([ADR 0013](adr/0013-tts-cost-metering.md)). The store fails open: if it can't be read, the generation is allowed and the lost charge is logged.

### Error mapping

Service-layer errors are typed:

- `*ValidationError` → HTTP 400 (includes an unknown or empty model — ADR 0011).
- `ErrOverBudget` → HTTP 402 (per-user quota exhausted — ADR 0009/0013).
- `ErrForbidden` → HTTP 403 (caller is not the debate's owner).
- `ErrNotFound` → HTTP 404.
- `ErrVersionConflict`, `ErrGenerationInFlight`, `ErrNotLastStep` → HTTP 409.
- `ErrProviderAuth` → HTTP 502 (the model provider rejected the server's API key).
- Anything else → HTTP 500 with the original error logged structurally.

Handlers must check for these specific kinds before falling through to a generic 500.

### Logging

All logs go through `slog`. A request-id middleware seeds `ctx`, and every handler logs with `slog.InfoContext(ctx, ...)` so the request id propagates automatically. See [ADR 0005](adr/0005-structured-logging-request-scoped-context.md).

---

## Where to look next

- **API contract, env vars, YAML format, deployment** — [README.md](README.md).
- **The domain vocabulary** — [CONTEXT.md](CONTEXT.md) (Generation, Turn, Retry, Interjection, Owner, Model, Allowlist, Narration).
- **Why a particular decision was made** — [`adr/`](adr/) (numbered, dated, scoped). Start with [0008](adr/0008-in-house-agent-layer-replacing-adk.md) for the agent layer, [0003](adr/0003-state-key-substitution.md) for the `{key}` substitution model, and [0001](adr/0001-debate-storage-repository-pattern.md) for the repository pattern.
- **The pipeline structure** — [`debate.pipeline`](backend/internal/debate/debate.go) is the one function that lays out every step in order (enumerate it cheaply via `BuildStepMeta`). Read it once and you know the whole match.
- **The agent contract** — [`agent.Agent.Run`](backend/internal/agent/agent.go) is ~30 lines. Reading it tells you everything an agent does.
- **The hydration pattern** — [`debate.Hydrate`](backend/internal/debate/debate.go) shows what a fresh `Runtime` contains and where each piece comes from.
