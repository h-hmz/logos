# Logos — Multi-Agent Philosophical Debate

A structured debate system in Go. Two philosophers debate a topic head-to-head across configurable rounds. A critic intervenes with a logical analysis, each philosopher responds, and a judge delivers a structured verdict.

LLM and TTS calls go through any OpenAI-compatible endpoint — OpenRouter by default for its prepaid hard spend cap (see [ADR 0007](adr/0007-llm-tts-via-openrouter-for-spend-caps.md)). The agent layer is in-house (see [ADR 0008](adr/0008-in-house-agent-layer-replacing-adk.md)) — no framework dependency. Every generation runs on a client-selected, allowlisted model (see [ADR 0011](adr/0011-client-supplied-models.md)) and is metered against a per-user USD budget (see [ADR 0009](adr/0009-cost-based-per-user-quota.md)).

**This README is the reference** — setup, configuration, the HTTP API, and the file-by-file map. For how the moving parts fit together and how a request flows, see [ARCHITECTURE.md](ARCHITECTURE.md); for the domain vocabulary, [CONTEXT.md](CONTEXT.md); for the rationale behind specific decisions, the [ADRs](adr/).

---

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
Defined in [`internal/philosopher/critic.go`](backend/internal/philosopher/critic.go). Receives the full debate transcript via `{name_key}_r{n}` substitutions.

Evaluates reasoning quality using a fallacy taxonomy:

- Ad hominem, Straw man, Circular reasoning
- Appeal to authority, False dichotomy, Equivocation
- Non sequitur, Appeal to tradition

**Theological exception**: Citing scripture (Quran, Bible, Upanishads, etc.) is not automatically flagged as "Appeal to Authority". The critic applies a three-way distinction:
- **Valid internal move** — citing one's own scripture to ground a claim within one's tradition. Not a fallacy; noted as a faith-based premise.
- **Contested cross-tradition premise** — citing scripture as if it should bind an opponent who doesn't share that revelation. Flagged as an unargued premise requiring independent justification.
- **Still a fallacy** — citing a scholar or theologian (not scripture) as proof without engaging their argument.

Output per debater: strongest argument, key weakness, detected fallacies, pointed challenge. Writes to `critic_feedback`.

### Judge — `philosopher.MakeJudge()`
Defined in [`internal/philosopher/judge.go`](backend/internal/philosopher/judge.go). Receives the complete record: all rounds, critic feedback, and both critic responses.

Produces a structured verdict:
1. **Summary of core positions**
2. **Key points of clash** — sharpest moments of direct disagreement
3. **Position shifts** — any concessions or evolution across rounds
4. **Argument rankings** — who made the stronger case and why
5. **Synthesis** — a genuine philosophical position integrating the best of both
6. **Confidence assessment** — score 1–10 with explanation
7. **Open questions** — unresolved threads for further inquiry

Writes to `final_verdict`. The verdict is emitted as a structured JSON object — the model is constrained to the schema — and returned as the step's `content` for the frontend to render section by section.

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

All env vars are read from `backend/.env` (see [`backend/.env.example`](backend/.env.example)).

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

**Auth + storage** (Google Cloud only — LLM/TTS no longer use GCP)

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
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173` | Comma-separated browser origins permitted by CORS. Set to the deployed frontend origin(s), e.g. `https://logos-5dw.pages.dev,https://*.logos-5dw.pages.dev` (the wildcard covers Cloudflare Pages preview deployments). Empty falls back to the local Vite dev origin. |

**Telemetry** (optional — OTel metrics push to any OTLP-compatible backend; Grafana Cloud free tier in our setup, see [`monitoring/README.md`](monitoring/README.md))

| Variable | Default | Purpose |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | *(unset → SDK disabled)* | OTLP/HTTP base URL, e.g. `https://otlp-gateway-prod-eu-west-2.grafana.net/otlp`. When empty, the SDK is a no-op and the backend logs `telemetry disabled`. |
| `OTEL_EXPORTER_OTLP_HEADERS` | *(unset)* | Auth headers, e.g. `Authorization=Basic%20<base64>`. The `%20` (URL-encoded space) is mandatory — raw spaces break env-var parsing on Cloud Run. |

---

## Running

### Backend — local

```bash
cd backend && go run .
```

Requires `LLM_API_KEY` and the other mandatory env vars — `QUOTA_USD_LIMIT`, `TTS_USD_PER_1M_AUDIO_TOKENS`, `TTS_USD_PER_GB_EGRESS` (the server won't start without them; see [`backend/.env.example`](backend/.env.example)). Set `AUTH_BACKEND=none` in `backend/.env` to skip Firebase init — that also bypasses the email/domain allowlist so dev callers aren't locked out.

### Backend — against a real model

Configure `backend/.env`:

```ini
LLM_API_KEY="sk-or-v1-..."
# LLM_BASE_URL defaults to https://openrouter.ai/api/v1; override to use a different provider.

# Required: per-user budget + narration pricing (ADR 0009, 0013)
QUOTA_USD_LIMIT=1.0
TTS_USD_PER_1M_AUDIO_TOKENS=20
TTS_USD_PER_GB_EGRESS=0.12

# Optional: enable Firebase auth + Firestore persistence
# AUTH_BACKEND=firebase
# STORAGE_BACKEND=firestore
# GOOGLE_CLOUD_PROJECT=your-gcp-project-id
```

When using Firebase auth or Firestore, also authenticate locally:

```bash
gcloud auth application-default login
```

### Frontend (development)

```bash
cd frontend && yarn dev
```

The Vite dev server proxies `/philosophers` and `/debates` to the target defined in `frontend/vite.config.ts` — no CORS configuration needed in development. The default target is `localhost:8000`; change `BACKEND` in that file to point at a deployed Cloud Run URL instead.

### Docker (Cloud Run)

```bash
cd backend && docker build -t logos .
docker run -p 8080:8080 --env-file .env logos
```
