<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="frontend/public/dark_owl.png">
    <img src="frontend/public/warm_owl.png" alt="Logos owl" width="96">
  </picture>
</p>

<h1 align="center">Logos</h1>

<p align="center"><em>An arena for arguments.</em></p>

Pick two thinkers, put a question on the table, and watch them fight it out. Socrates against Nietzsche on whether the unexamined life is worth living. Ibn Rushd against Al-Ghazali on whether reason can prove faith. Marx against Hayek on whether capitalism is inevitable. A critic calls out every fallacy, each side has to answer for theirs, and a judge settles it, with a scorecard to back the call.

Behind the curtain, Logos is a multi-agent debate engine: a Go backend that orchestrates a moderator, two debaters, a critic and a judge over any OpenAI-compatible model, and a React front end that streams every word as it is written, reads each turn aloud in the speaker's own voice, and keeps an owl on hand to explain the hard parts.

---

## A debate in five acts

1. **The moderator** introduces the match-up and names the fault line the two are bound to collide on.
2. **The rounds**, one to five of them. A opens, B answers, and every turn has to engage the one before it. The text arrives token by token, and before any turn you can **interject**, slipping the next speaker a note: *"Press him on the labour theory of value."*
3. **The critic** reviews the whole exchange: each side's strongest point, its weakest link, the fallacies it committed, and one pointed challenge apiece. Citing your own scripture within your own tradition is not automatically an appeal to authority; expecting your opponent to accept it is flagged as an unargued premise.
4. **The rebuttals.** Each debater must answer the critic's challenge and sharpen their position.
5. **The judge** scores both sides from 1 to 5 on Rigor, Responsiveness, Depth and Persuasion, quotes the turning point verbatim, and closes with a synthesis that takes the best of both (a position of its own, not a diplomatic compromise) along with the questions left open.

The winner falls out of the scorecard totals, so the judge can never crown a side its own scores contradict.

## What's on stage

- **23 thinkers across 2,500 years**, from Confucius, Laozi and Nagarjuna to Ibn Khaldun, Kant, Simone de Beauvoir and Frantz Fanon. Each one is a YAML file: a worldview, the thinkers they draw on and, for theological traditions, the scriptures they argue from. [Adding one](#adding-a-philosopher) takes no code.
- **They speak.** Press *Recite* and a turn is read aloud in a voice and accent cast for its speaker: a soft Edinburgh burr for Hume, a heavy German accent for Marx, a near-whisper for Laozi. The debaters write their own stage directions (`[wearily]`, `[scoffs]`, `[laughs softly]`) and the narrator performs them. Each narration is synthesized once, then replayed from storage.
- **Noctua, the resident owl.** Highlight any passage you don't follow and ask. Noctua explains it in context and suggests where to dig next.
- **You pick the minds behind the masks.** Each debater runs on the model you choose (GPT, Gemini, DeepSeek, Qwen, GLM), and the referees on another. Marx on DeepSeek against Hayek on GPT-5.5 is a perfectly reasonable way to spend an evening.
- **Re-roll** the last turn or the verdict, **auto-play** the whole debate hands-free, and **share** the verdict by link.
- **Every token accounted for.** Each turn shows its tokens, cost, time to first token and generation time (Zen mode tucks them away), and every account has a hard spending budget.

## Behind the curtain

- **No agent framework.** A small in-house agent layer over the OpenAI-compatible API, which replaced Google ADK ([ADR 0008](adr/0008-in-house-agent-layer-replacing-adk.md)). LLM and TTS calls go through OpenRouter by default for its prepaid hard spend cap ([ADR 0007](adr/0007-llm-tts-via-openrouter-for-spend-caps.md)), but any compatible endpoint works: OpenAI, Together, Groq, vLLM, Ollama.
- **Stateless by design.** Every step reloads the debate, rebuilds the agents' shared state from the saved turns, runs exactly one agent and writes back under optimistic locking. Any instance can serve any request, so it scales out on Cloud Run without sticky sessions ([ARCHITECTURE.md](ARCHITECTURE.md), [ADR 0001](adr/0001-debate-storage-repository-pattern.md), [ADR 0004](adr/0004-concurrency-repo-occ.md)).
- **Referees on a schema.** The critic and the judge are constrained to JSON schemas, so the front end renders their output section by section instead of parsing prose.
- **Spend is enforced, not just tracked.** Each generation takes a per-user lock and checks the budget before the first token streams ([ADR 0009](adr/0009-cost-based-per-user-quota.md)); narration is metered reserve-then-settle ([ADR 0013](adr/0013-tts-cost-metering.md)).
- **Closed by default.** Firebase Auth sits behind an email/domain allowlist that denies everyone until configured ([ADR 0006](adr/0006-auth-firebase-verifier.md)). Debates are shareable by unguessable link, but only their owner can advance them.
- **Observable.** Structured logs carry request, session and trace IDs ([ADR 0005](adr/0005-structured-logging-request-scoped-context.md)); OpenTelemetry metrics for tokens, cost, latency and errors, broken down by model and role, feed a Grafana dashboard ([monitoring/](monitoring/)).
- **Go 1.25 · React 18 + Vite · Firestore + Cloud Storage**, or fully in memory for local development.

## Quick start

You need Go 1.25+, Node 20+ and an API key from [OpenRouter](https://openrouter.ai) or any other OpenAI-compatible provider. No Google Cloud account required.

```bash
# Backend, on http://localhost:8000
cd backend
cp .env.example .env
# in .env: set LLM_API_KEY, and add AUTH_BACKEND=none to skip Firebase
go run .
```

```bash
# Frontend, on http://localhost:5173 (second terminal)
cd frontend
corepack enable      # provides the pinned Yarn 4 (on Node 25+, run `npm i -g corepack` first)
yarn install
yarn dev
```

Open http://localhost:5173, pick two thinkers and pose a question. Debates, budgets and narrations live in memory and vanish on restart. Narration is off by default; set `TTS_ENABLED=TRUE` in `backend/.env` to turn it on. For Firebase Auth, Firestore persistence and deployment, see [Running and deploying](#running-and-deploying).

## Find your way around

| Document | What it covers |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | How the pieces fit together and how a request flows end to end |
| [CONTEXT.md](CONTEXT.md) | The domain vocabulary used throughout the code and docs |
| [adr/](adr/) | Architecture decision records: what was decided, and why |
| [monitoring/](monitoring/) | Metrics, the Grafana dashboard, and cost-per-user queries |
| [Reference](#reference), below | Project layout, agents, HTTP API, configuration |

---

# Reference

## Project layout

```
backend/
├── main.go                              # entry point — config, repo/quota/narration/auth wiring, startup
├── go.mod
├── Dockerfile
├── .env                                 # credentials (gitignored)
├── agents/
│   └── philosophers/                    # one YAML file per philosopher
└── internal/
    ├── config/      config.go           # .env loading, env-var accessors
    ├── agent/       state.go            # agent.State + {key} substitution
    │                llm.go              # LLM interface + Tool/ToolCall types
    │                agent.go            # Agent struct + Run
    │                openai_compat.go    # openai-go-backed LLM (any OpenAI-compatible endpoint)
    │                instrumented.go     # OTel-instrumented LLM wrapper
    │                metrics.go          # LLM metric instruments
    │                registry.go         # process-wide LLM cache + factory hook
    ├── models/      models.go           # the model allowlist — models.Allowed, sole authority (ADR 0011)
    ├── auth/        auth.go             # Firebase + noop verifiers for protected routes
    │                allowlist.go        # email/domain allowlist, fail-closed (AUTH_ALLOWLIST)
    ├── philosopher/ philosopher.go      # YAML loader, Key() normalizer, MakePhilosopher() factory
    │                moderator.go        # MakeModerator() factory
    │                critic.go           # fallacy taxonomy, MakeCritic() factory
    │                judge.go            # MakeJudge() factory
    │                render.go           # text/template prompt rendering helper
    ├── tutor/       tutor.go            # tutor agent factory + BuildUserMessage
    ├── debate/      debate.go           # StepRecord, Persisted, Runtime, pipeline(), Hydrate()
    │                service.go          # CreateDebate, NextTurn, RetryTurn, quota gating
    │                tutor.go            # TutorExplain (side channel — not a pipeline step)
    │                repository.go       # Repository interface + sentinel errors
    │                memory_repo.go      # in-process Repository
    │                firestore_repo.go   # Firestore-backed Repository
    ├── quota/       quota.go            # Store interface + ErrOverBudget (ADR 0009/0013)
    │                inflight.go         # per-UID in-flight lock
    │                memory_store.go     # in-process spend store
    │                firestore_store.go  # Firestore-backed spend store (Reserve / Add)
    ├── tts/         tts.go              # lazy TTS client + sample rate
    │                openai_compat.go    # OpenAI-format /audio/speech client
    │                voices.go           # voice/style resolution per step
    │                store.go            # NarrationStore + in-memory impl (ADR 0012)
    │                gcs_store.go        # GCS-backed NarrationStore
    │                pricing.go          # narration cost model — reserve/settle (ADR 0013)
    ├── telemetry/   telemetry.go        # OpenTelemetry SDK init (no-op when unset)
    └── server/      routes.go           # Chi router + middleware + JSON helpers
                     auth.go             # auth middleware + UID context
                     handler_debate.go   # POST /debates, /next, /retry; GET /debates/{id}
                     handler_tutor.go    # /tutor
                     handler_tts.go      # /tts + /narrations (audio streaming, caching)
                     handler_quota.go    # /me/quota
                     handler_philosophers.go
                     middleware.go       # request-id, access log, recover
                     session.go          # session-id middleware
                     trace.go            # trace-context middleware
                     sse.go              # SSE streaming helper

frontend/                                # React + Vite (proxies to backend:8000)
```

All agent prompts live in their respective package; routing wires them together. For the conceptual model behind these packages — stateless hydration, the `{key}` protocol, and how a request flows end-to-end — see [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Debate Pipeline

Each debate is a linear sequence of steps executed one at a time on demand. The pipeline for N rounds:

```
Moderator                 theatrical introduction of the match-up
│
├─ {A} Round 1            A opens — core position, no prior context
├─ {B} Round 1            B responds to A's opening
├─ {A} Round 2            A responds to B's Round 1
├─ {B} Round 2            B responds to A's Round 2
│   ...                   (up to 5 rounds)
│
Critic                    reviews all rounds — strengths, fallacies, per-debater challenge
│
├─ {A} Critic Response    A addresses the critic's challenge and refines their position
├─ {B} Critic Response    B addresses the critic's challenge and refines their position
│
Judge                     structured final verdict
```

Total steps = `2N + 5` (1 moderator + 2N rounds + 1 critic + 2 critic responses + 1 judge).

---

## State and Hydration

The runtime is **stateless across requests**: each `/debates/{id}/next` loads the `Persisted` snapshot, calls [`debate.Hydrate`](backend/internal/debate/debate.go) to rebuild a throwaway `Runtime` (resolved debater data plus an [`agent.State`](backend/internal/agent/state.go) map seeded with `topic` and the content of every prior `StepRecord`), runs one step's `Agent.Run`, then appends a new `StepRecord` and advances via an optimistic-locked `repo.Update`. The full mechanism — the `{key}` ↔ `OutputKey` protocol and the two-request inter-step flow — is documented in [ARCHITECTURE.md](ARCHITECTURE.md).

Agents communicate only through `agent.State` slots:

| Key | Written by | Read by |
|---|---|---|
| `topic` | seeded at hydrate from `Persisted.Topic` | all agents |
| `{name_key}_r{n}` | philosopher turn (e.g. `karl_marx_r2`) | next philosopher, critic, judge |
| `critic_feedback` | Critic | both critic-response turns, judge |
| `{name_key}_critic_response` | philosopher critic-response turn | judge |
| `{step_key}_interjection` | seeded at hydrate when the owner steered that turn | that debater turn only |
| `final_verdict` | Judge | (terminal) |

Each completed step is stored as a `StepRecord` carrying its content, the `model` it ran on, the owner's `interjection` (if any), token counts, and provider cost. The debate's `OwnerUID` is stamped at creation — only the owner may advance or retry it. A per-debate optimistic `Version` serializes concurrent writes; conflicting updates surface as HTTP 409.

---

## Agents

### Moderator — `philosopher.MakeModerator()`
Defined in [`internal/philosopher/moderator.go`](backend/internal/philosopher/moderator.go). Introduces the pair, frames the topic-specific clash, and hands off to A. Reads `{topic}`. Writes `moderator_intro`.

### Philosopher — `philosopher.MakePhilosopher()`
Defined in [`internal/philosopher/philosopher.go`](backend/internal/philosopher/philosopher.go). One `agent.Agent` is created per debate turn; each instance gets a unique `Name` and `OutputKey`.

The prompt is assembled from the philosopher's YAML fields:

| YAML field | Role in prompt |
|---|---|
| `name` | Speaker identity |
| `school` | Philosophical tradition |
| `identity` | Core worldview block |
| `key_thinkers` | Thinkers to reference |
| `epistemic_sources` | *(optional)* Foundational sources for theological traditions (Quran, Bible, Upanishads…) |
| `tts_voice` | *(optional)* Gemini TTS voice id (e.g. `Algenib`, `Kore`, `Rasalgethi`) |
| `tts_style` | *(optional)* Per-philosopher TTS delivery prompt |
| `era`, `dates`, `one_liner` | *(optional)* Catalog metadata exposed to the frontend |

The factory also injects:
- `{topic}` from session state
- `{opponent_arg_key}` — the opponent's previous turn (if any)
- `{critic_key}` — the critic's feedback (for post-critic turns)
- A shared rules block with the configured word limit (`maxWords`)
- An "Expressive Delivery" block describing inline `[bracketed]` TTS cues

#### `philosopher.Key(name)`
Normalizes a display name to a safe identifier used for all state keys and agent names:

```go
philosopher.Key("John Stuart Mill")   // → "john_stuart_mill"
philosopher.Key("Al-Ghazali")         // → "al_ghazali"
philosopher.Key("Simone de Beauvoir") // → "simone_de_beauvoir"
```

This allows full display names in YAML while keeping state keys and agent names alphanumeric.

### Critic — `philosopher.MakeCritic()`
Defined in [`internal/philosopher/critic.go`](backend/internal/philosopher/critic.go). Receives the full transcript of every round.

Evaluates reasoning quality using a fallacy taxonomy:

- Ad hominem, Straw man, Circular reasoning
- Appeal to authority, False dichotomy, Equivocation
- Non sequitur, Appeal to tradition

**Theological exception**: Citing scripture (Quran, Bible, Upanishads, etc.) is not automatically flagged as "Appeal to Authority". The critic applies a three-way distinction:
- **Valid internal move** — citing one's own scripture to ground a claim within one's tradition. Not a fallacy; noted as a faith-based premise.
- **Contested cross-tradition premise** — citing scripture as if it should bind an opponent who doesn't share that revelation. Flagged as an unargued premise requiring independent justification.
- **Still a fallacy** — citing a scholar or theologian (not scripture) as proof without engaging their argument.

Output per debater: strongest argument, key weakness, detected fallacies, pointed challenge — framed by a one-line introduction and closed by an overall assessment of the debate's quality. Writes to `critic_feedback`.

### Judge — `philosopher.MakeJudge()`
Defined in [`internal/philosopher/judge.go`](backend/internal/philosopher/judge.go). Receives the complete record: all rounds, critic feedback, and both critic responses. It judges the debate as a whole (the critic has already assessed each side's reasoning) and is told not to re-summarize what the reader has just read.

Produces a structured verdict:
1. **Scorecard** — four criteria, each scored 1–5 for both debaters with a one-sentence note on the gap: *Rigor* (soundness, freedom from fallacy), *Responsiveness* (engaging the opponent's actual argument and the critic's challenge), *Depth* (originality and insight), *Persuasion* (force, clarity, memorability)
2. **Turning point** — the single decisive moment, quoted verbatim from the transcript, with who said it and why it mattered
3. **Ranking note** — what the totals add up to: what tipped the balance, or why it was a draw
4. **Synthesis** — a genuine philosophical position built from the strongest insights of both sides, not a diplomatic compromise
5. **Open questions** — two or three threads the debate raised but did not settle

The judge never declares a winner: the client derives it from the scorecard column totals, so the result can't contradict the scores. Writes to `final_verdict`. The verdict is emitted as a structured JSON object — the model is constrained to the schema — and returned as the step's `content` for the frontend to render section by section.

### Tutor — `tutor.MakeTutor()`
Defined in [`internal/tutor/tutor.go`](backend/internal/tutor/tutor.go). Out-of-band agent invoked from `/debates/{id}/tutor` to explain a passage the user highlighted. Receives the selected text plus the surrounding turn context and a short conversation history so the user can ask follow-ups.

---

## Models

Every generation — a debate turn, a retry, or a tutor reply — names the model it runs on; there is **no server-side default**. The backend validates that model against `models.Allowed` ([`internal/models/models.go`](backend/internal/models/models.go)), the sole authority over which models may run, and records it on the step. A non-allowlisted or empty model is rejected with **400**. The frontend keeps its own hardcoded mirror of the allowlist to populate its pickers (one model per debater, one referee model shared by the moderator, critic, and judge) and sends the right one on each request. See [ADR 0011](adr/0011-client-supplied-models.md) and the *Model* / *Allowlist* terms in [CONTEXT.md](CONTEXT.md).

---

## Adding a Philosopher

Drop a new `.yaml` file in [`backend/agents/philosophers/`](backend/agents/philosophers/). Name the file after `philosopher.Key(name)` (e.g. `socrates.yaml`). No code changes required — the loader scans the directory at startup.

Required fields:

```yaml
name: Socrates
school: Socratic Philosophy
identity: |
  You believe that virtue is knowledge — that wrongdoing is always a form of ignorance,
  never a deliberate choice by someone who truly understands the good. Your task is the
  elenchus — the cross-examination that exposes contradictions and clears the ground for
  genuine understanding...
key_thinkers: Socrates, Plato, Xenophon, Alcibiades, Chaerephon
```

Optional fields:

```yaml
era: Ancient
dates: c. 470–399 BCE
one_liner: The unexamined life is not worth living.
tts_voice: Sadaltager
tts_style: "Speak with curious cunning — probing, ironic, feigning ignorance while leading. Speak with a sly Ancient Greek cadence."
```

Theological traditions can also set `epistemic_sources` — a multi-line block of foundational scripture the agent grounds its claims in (e.g. the Quran, Bible, or Upanishads). Socrates has none.

---

## API

Every route except the health probes is **protected**. A request must carry `Authorization: Bearer <id_token>` and pass two gates: the **verifier** (`AUTH_BACKEND` — `firebase` validates a Firebase ID token, `none` accepts any non-empty token for local dev) authenticates the caller, then the **allowlist** (`AUTH_ALLOWLIST` — emails and/or domains; empty denies everyone, `*` allows every verified caller) authorizes them.

**Per-debate ownership.** A debate's `OwnerUID` is stamped at creation. Reads stay open to anyone holding the (unguessable) debate ID, but only the owner may advance or retry it — a non-owner write is **403**. See the *Owner* term in [CONTEXT.md](CONTEXT.md).

### `GET /healthz` · `GET /readyz`
Liveness / readiness — `200 OK`, no body. The only public routes.

### `GET /philosophers`
Returns all loaded philosophers (catalog metadata only — `tts_voice`/`tts_style` are not exposed).

```json
[
  {"name": "Karl Marx", "school": "Marxism", "era": "Modern", "dates": "1818–1883", "one_liner": "All history is the history of class struggle."},
  ...
]
```

### `GET /me/quota`
Returns the authenticated caller's lifetime generation budget in USD. `remaining_usd` is clamped at zero.

```json
{"limit_usd": 1.0, "spent_usd": 0.0123, "remaining_usd": 0.9877}
```

### `POST /debates`
Creates a debate (the caller becomes its owner) and returns the step manifest. No model is supplied here — the model is chosen per generation on `/next` and `/retry`.

Request:
```json
{
  "topic": "Is capitalism inevitable?",
  "debater_a": "Karl Marx",
  "debater_b": "Friedrich Hayek",
  "num_rounds": 2,
  "max_words": 250
}
```

Response:
```json
{
  "id": "9b0e…",
  "topic": "Is capitalism inevitable?",
  "debater_a": {"name": "Karl Marx", "school": "Marxism"},
  "debater_b": {"name": "Friedrich Hayek", "school": "Austrian School"},
  "steps": [{"key": "moderator_intro", "label": "Introduction"}, ...],
  "total_steps": 9
}
```

### `GET /debates/{id}`
Rehydrates a debate for a returning client: the same manifest as create, plus a `turns` map (step index → turn payload) holding every completed step's content, model, interjection, token counts, cost, and timing. Open to any caller with the ID.

### `POST /debates/{id}/next`
Advances the debate by one step (owner only). The body names the `model` to run on and, optionally, an `interjection` — a one-time steering note honored by debater turns and ignored by referee steps. Supports JSON and SSE streaming (`Accept: text/event-stream`).

Request:
```json
{"model": "google/gemini-3-flash-preview", "interjection": "Press him on the labor theory of value."}
```

JSON response:
```json
{
  "key": "karl_marx_r1",
  "label": "Karl Marx — Round 1",
  "author": "karl_marx_r1",
  "role": "a",
  "content": "...",
  "step_index": 1,
  "total_steps": 9,
  "done": false,
  "model": "google/gemini-3-flash-preview",
  "interjection": "Press him on the labor theory of value.",
  "turn_input_tokens": 812,
  "turn_output_tokens": 243,
  "turn_cost_usd": 0.0009,
  "total_input_tokens": 812,
  "total_output_tokens": 243,
  "turn_ttft_ms": 410,
  "turn_duration_ms": 5200
}
```

For `critic_feedback` and `final_verdict`, `content` is a JSON object — those agents emit structured output the frontend renders section by section; every other step returns prose. `role` is one of `a`, `b`, `moderator`, `critic`, `judge`. Token counts and cost are reported per-turn (`turn_*`) and cumulative (`total_*`).

SSE response (when `Accept: text/event-stream`): streams `event: token` chunks during generation, then closes with `event: done` carrying the same JSON payload; errors emerge as `event: error`. The quota gate (in-flight lock + budget check) runs **before** any token is streamed, so over-budget and in-flight rejections return a real HTTP status rather than an SSE error.

Statuses: **400** (unknown/empty model or invalid input), **402** (generation quota exhausted), **403** (not the owner), **409** (a generation is already in flight, or the debate changed concurrently), **502** (the model provider rejected the server's API key).

### `POST /debates/{id}/retry`
Re-rolls the **last** completed step in place (owner only), discarding its generation and replacing it with a fresh one — this is how the judge's verdict gets re-rolled, and it is allowed even when the debate is complete. `step_key` must name the current last step (anything else is **409**); `model` is the model to re-roll on, which may differ from the original. Same lock, budget gate, SSE behavior, and statuses as `/next`. The discarded generation's cost stays charged to the owner's quota even though the debate's displayed total only counts the kept one (see [ADR 0010](adr/0010-retry-last-step.md)).

```json
{"step_key": "final_verdict", "model": "google/gemini-3-flash-preview"}
```

### `POST /debates/{id}/tutor`
Asks a tutor agent to explain a passage from a previous turn (open to any caller with the ID; the tutor is not a pipeline step and persists nothing, but it is a metered generation). Supports SSE streaming on the same `Accept` header rule. The reply carries `content`, an optional `suggestions` list of follow-up questions, and `cost_usd`.

```json
{
  "selection": "surplus value",
  "turn_key": "karl_marx_r1",
  "history": [{"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}]
}
```

### `POST /debates/{id}/tts`
Synthesizes the given `text` for the named step and streams it as an `audio/L16;rate=24000;channels=1` PCM stream, using the voice/style resolved from the step's role. Returns **503** when `TTS_ENABLED` is unset.

```json
{"step_key": "karl_marx_r1", "text": "..."}
```

A completed narration is **persisted and content-addressed** ([ADR 0012](adr/0012-narration-persistence.md)): a later replay of identical text/voice/style/model is served straight from storage instead of re-synthesizing. The `X-TTS-Cache` response header reports `hit` (served from storage) or `miss` (freshly synthesized); `X-TTS-Voice` reports the chosen voice. Each call is metered with a reserve-then-settle charge — synthesis + egress on a miss, egress only on a hit ([ADR 0013](adr/0013-tts-cost-metering.md)) — so a call the caller can't afford returns **402** before any bytes flow. Inline parentheticals (e.g. `asabiyya (social solidarity)`) are stripped server-side before synthesis to avoid awkward read-aloud asides.

### `GET /debates/{id}/narrations`
Reports, per narratable step, whether a narration already exists in storage — so the client can warn before a play that will synthesize (a miss, which incurs the synthesis charge) versus an instant replay (a hit). Returns **503** when `TTS_ENABLED` is unset.

```json
{"cached": {"karl_marx_r1": true, "friedrich_hayek_r1": false}}
```

---

## Configuration

| What | Where | Effect |
|---|---|---|
| LLM provider | `LLM_BASE_URL` env var | OpenAI-compatible base URL; defaults to OpenRouter |
| LLM key | `LLM_API_KEY` env var | API key for the chosen provider |
| Model | per generation request | Client-supplied on each call, checked against the allowlist (ADR 0011); no server-side default |
| Philosopher pool | `backend/agents/philosophers/*.yaml` | Add / remove / edit — restart to pick up changes |
| Max rounds | `POST /debates` — `num_rounds` | 1–5 |
| Word limit | `POST /debates` — `max_words` | Philosopher turns; critic gets `2×`, moderator is fixed at 40 words |
| Storage backend | `STORAGE_BACKEND` env var | `memory` (default) or `firestore`; covers debates, quota spend, and narrations alike |
| Auth backend | `AUTH_BACKEND` env var | `firebase` (default) or `none` (local dev only) |
| Auth allowlist | `AUTH_ALLOWLIST` env var | Emails/domains allowed past auth; empty denies all, `*` allows all (dev only) |
| Per-user budget | `QUOTA_USD_LIMIT` env var | Lifetime USD generation budget per Firebase UID; always enforced (ADR 0009) |
| Data directory | `DATA_DIR` env var | Override path to `agents/` (default: binary directory) |

### Environment Variables

All backend env vars are read from `backend/.env` (see [`backend/.env.example`](backend/.env.example)).

**LLM + TTS provider — a single OpenAI-compatible endpoint** (both surfaces share it; TTS-specific knobs are in the next table)

| Variable | Required? | Purpose |
|---|---|---|
| `LLM_API_KEY` | When using a real model | API key for the OpenAI-compatible provider. |
| `LLM_BASE_URL` | No | OpenAI-compatible base URL. Defaults to `https://openrouter.ai/api/v1`. Works with OpenAI, Together AI, Groq, Azure OpenAI, vLLM, Ollama, LM Studio, etc. |

**Text-to-speech**

| Variable | Default | Purpose |
|---|---|---|
| `TTS_ENABLED` | unset (disabled) | Set to `TRUE` to expose `POST /debates/{id}/tts` and `GET /debates/{id}/narrations`. Routes through the same `LLM_BASE_URL`. |
| `TTS_MODEL` | `google/gemini-3.1-flash-tts-preview` | TTS model identifier in provider form. |
| `TTS_BUCKET` | *(none)* | GCS bucket for persisted narrations (ADR 0012). **Required** when `STORAGE_BACKEND=firestore` and TTS is enabled; unused with the in-memory store. |

**Quota + metering — required** (the server refuses to start if any is unset or negative)

| Variable | Default | Purpose |
|---|---|---|
| `QUOTA_USD_LIMIT` | *(required)* | Per-user (Firebase UID) lifetime generation budget in USD, seeded onto each new account and enforced across debate turns, retries, and tutor replies (ADR 0009). |
| `TTS_USD_PER_1M_AUDIO_TOKENS` | *(required)* | TTS synthesis price per 1,000,000 audio tokens; meters narration synthesis from the byte count (ADR 0013). |
| `TTS_USD_PER_GB_EGRESS` | *(required)* | Price per GB for serving narration bytes; metered on every narration call, miss and hit (ADR 0013). |
| `QUOTA_COLLECTION` | `quotas` | Firestore collection for per-user spend (only when `STORAGE_BACKEND=firestore`). |

**Auth + storage** (Google Cloud, optional — LLM and TTS don't depend on it)

| Variable | Default | Purpose |
|---|---|---|
| `AUTH_BACKEND` | `firebase` | `firebase` validates Firebase ID tokens; `none` accepts any non-empty Bearer token (local dev only). |
| `AUTH_ALLOWLIST` | *(empty → deny all)* | Comma-separated emails and/or domains permitted past auth. A bare domain (`example.com`) admits every verified address at that domain; a full address (`alice@other.com`) admits just that user. `*` admits all verified callers (local dev only). Empty denies everyone — **fail-closed**. |
| `GOOGLE_CLOUD_PROJECT` | *(none)* | The GCP project this backend runs in. Used for `STORAGE_BACKEND=firestore` and Cloud Logging trace-clickthrough links. Required when `STORAGE_BACKEND=firestore`. Uses Application Default Credentials (`gcloud auth application-default login`). |
| `FIREBASE_PROJECT` | *(none)* | The project that issues the Firebase ID tokens the auth verifier validates — i.e. the project your frontend signs in against. **Required** when `AUTH_BACKEND=firebase`. May equal `GOOGLE_CLOUD_PROJECT` (single project) or differ from it when Firebase Hosting/Auth live in a separate project (e.g. a free Spark-plan project) than the backend's Cloud Run + Firestore. |
| `STORAGE_BACKEND` | `memory` | `memory` (in-process) or `firestore` (Cloud Run). Selects the backend for debates, per-user quota spend, and persisted narrations alike. |
| `FIRESTORE_DATABASE` | `(default)` | Firestore database id. |
| `FIRESTORE_COLLECTION` | `debates` | Firestore collection name for debates (quota uses `QUOTA_COLLECTION`). |

**Server**

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8000` (`8080` in the Docker image) | HTTP listen port. |
| `DATA_DIR` | *(binary directory)* | Path to the directory containing `agents/`. |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173` | Comma-separated browser origins permitted by CORS. Set to the deployed frontend origin(s), e.g. `https://your-app.pages.dev,https://*.your-app.pages.dev` (the wildcard covers Cloudflare Pages preview deployments). Empty falls back to the local Vite dev origin. |

**Telemetry** (optional — OTel metrics push to any OTLP-compatible backend, e.g. Grafana Cloud's free tier; see [`monitoring/README.md`](monitoring/README.md))

| Variable | Default | Purpose |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | *(unset → SDK disabled)* | OTLP/HTTP base URL, e.g. `https://otlp-gateway-prod-eu-west-2.grafana.net/otlp`. When empty, the SDK is a no-op and the backend logs `telemetry disabled`. |
| `OTEL_EXPORTER_OTLP_HEADERS` | *(unset)* | Auth headers, e.g. `Authorization=Basic%20<base64>`. The `%20` (URL-encoded space) is mandatory — raw spaces break env-var parsing on Cloud Run. |

**Frontend** (`frontend/.env.local` for development, `frontend/.env.production` for builds — both gitignored)

| Variable | Purpose |
|---|---|
| `VITE_API_BASE` | Absolute backend origin (e.g. your Cloud Run URL) for production builds. Leave unset in development so requests go through Vite's proxy. |
| `VITE_FIREBASE_API_KEY` | Firebase web config. When unset, Firebase is disabled: the app skips sign-in and sends a placeholder token, so pair it with `AUTH_BACKEND=none`. |
| `VITE_FIREBASE_AUTH_DOMAIN`, `VITE_FIREBASE_PROJECT_ID` | Firebase web config, required alongside the API key. |
| `VITE_FIREBASE_APP_ID`, `VITE_FIREBASE_MEASUREMENT_ID` | Optional. Setting the app ID also turns on Google Analytics. |

---

## Running and deploying

The [Quick start](#quick-start) runs everything locally and in memory. To go further:

### Firebase Auth and Firestore persistence

In `backend/.env`:

```ini
AUTH_BACKEND=firebase
FIREBASE_PROJECT=your-firebase-project-id
AUTH_ALLOWLIST=you@example.com          # emails and/or domains; empty denies everyone
STORAGE_BACKEND=firestore
GOOGLE_CLOUD_PROJECT=your-gcp-project-id
# TTS_BUCKET=your-narrations-bucket     # required if TTS_ENABLED=TRUE
```

Authenticate locally with Application Default Credentials:

```bash
gcloud auth application-default login
```

Then give the frontend its Firebase web config (the `VITE_FIREBASE_*` [variables](#environment-variables)).

### Frontend in development

The Vite dev server proxies `/philosophers` and `/debates` to `localhost:8000` (the `BACKEND` constant in [`frontend/vite.config.ts`](frontend/vite.config.ts)), so no CORS configuration is needed in development.

### Production build

```bash
cd frontend && yarn build     # outputs frontend/dist
```

Set `VITE_API_BASE` to the backend's URL at build time, and list the deployed origin in the backend's `CORS_ALLOWED_ORIGINS`. A Firebase Hosting config is included ([`frontend/firebase.json`](frontend/firebase.json); point `frontend/.firebaserc` at your own project), but any static host works.

### Docker (Cloud Run)

```bash
cd backend && docker build -t logos .
docker run -p 8080:8080 --env-file .env logos
```
