# Logos — Multi-Agent Philosophical Debate

A structured debate system in Go. Two philosophers debate a topic head-to-head across configurable rounds. A critic intervenes with a logical analysis, each philosopher responds, and a judge delivers a structured verdict.

LLM and TTS calls go through any OpenAI-compatible endpoint — OpenRouter by default for its prepaid hard spend cap (see [ADR 0007](adr/0007-llm-tts-via-openrouter-for-spend-caps.md)). The agent layer is in-house (see [ADR 0008](adr/0008-in-house-agent-layer-replacing-adk.md)) — no framework dependency.

---

## Architecture

```
backend/
├── main.go                              # entry point — config, repo + auth wiring, server startup
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
    │                registry.go         # process-wide LLM cache + factory hook
    ├── auth/        auth.go             # Firebase + noop verifiers for protected routes
    ├── philosopher/ philosopher.go      # YAML loader, Key() normalizer, MakePhilosopher() factory
    │                moderator.go        # MakeModerator() factory
    │                critic.go           # fallacy taxonomy, MakeCritic() factory
    │                judge.go            # MakeJudge() factory
    ├── tutor/       tutor.go            # tutor agent factory (used by /tutor handler)
    ├── debate/      debate.go           # Step, Persisted, Runtime, BuildSteps(), Hydrate()
    │                service.go          # CreateDebate, NextTurn, TutorExplain
    │                repository.go       # Repository interface + sentinel errors
    │                memory_repo.go      # in-process Repository
    │                firestore_repo.go   # Firestore-backed Repository
    ├── tts/         tts.go              # lazy TTS client + sample rate
    │                openai_compat.go    # OpenAI-format /audio/speech client
    │                voices.go           # voice/style resolution per step
    │                store.go            # NarrationStore + in-memory impl (ADR 0012)
    │                gcs_store.go        # GCS-backed NarrationStore
    │                pricing.go          # narration cost model (ADR 0013)
    └── server/      routes.go           # Chi router + auth middleware + JSON helpers
                     handler_debate.go   # /debates, /next
                     handler_tutor.go    # /tutor
                     handler_tts.go      # /tts (audio streaming)
                     handler_philosophers.go
                     middleware.go       # request-id, access log, recover
                     sse.go              # SSE streaming helper

frontend/                                # React + Vite (proxies to backend:8000)
```

All agent prompts live in their respective package; routing wires them together.

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

The runtime is **stateless across requests**. Each `/debates/{id}/next` call:

1. Loads the `Persisted` snapshot from the repository (Firestore in prod, in-memory in dev / tests).
2. Calls `debate.Hydrate` to rebuild a `Runtime`: resolves debater data from the catalog, constructs the pipeline agents for the current model, and seeds an [`agent.State`](backend/internal/agent/state.go) map with `topic` and all prior `StepOutputs`.
3. Runs the next step's `Agent.Run` against that state. The agent resolves `{key}` placeholders in its instruction via `agent.Substitute`, calls the LLM, and writes its output back to `state[OutputKey]`.
4. Persists the new step output and advances `Current` via an optimistic-locked `repo.Update`.

| Key | Written by | Read by |
|---|---|---|
| `topic` | seeded at hydrate from `Persisted.Topic` | all agents |
| `{name_key}_r{n}` | philosopher turn (e.g. `karl_marx_r2`) | next philosopher, critic, judge |
| `critic_feedback` | Critic | both critic-response turns, judge |
| `{name_key}_critic_response` | philosopher critic-response turn | judge |
| `final_verdict` | Judge | (terminal) |

A per-debate optimistic version on `Persisted.Version` serializes concurrent writes; conflicting updates surface as HTTP 409.

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

Writes to `final_verdict`. The handler attempts to extract a JSON object from the output and returns it on the `structured` field of the response.

### Tutor — `tutor.MakeTutor()`
Defined in [`internal/tutor/tutor.go`](backend/internal/tutor/tutor.go). Out-of-band agent invoked from `/debates/{id}/tutor` to explain a passage the user highlighted. Receives the selected text plus the surrounding turn context and a short conversation history so the user can ask follow-ups.

---

## Adding a Philosopher

Drop a new `.yaml` file in [`backend/agents/philosophers/`](backend/agents/philosophers/). Name the file after `philosopher.Key(name)` (e.g. `friedrich_nietzsche.yaml`). No code changes required — the loader scans the directory at startup.

Required fields:

```yaml
name: Friedrich Nietzsche
school: Existentialism / Perspectivism
identity: |
  You believe God is dead and with him the foundations of Western morality...
key_thinkers: Nietzsche, Schopenhauer, Heraclitus, Dostoevsky
```

Optional fields:

```yaml
era: Modern
dates: 1844–1900
one_liner: God is dead, and we have killed him.
epistemic_sources: |
  The Quran is the direct and literal word of God, transmitted through the Prophet Muhammad...
tts_voice: Rasalgethi
tts_style: "Aphoristic, biting, performative — vary tempo sharply."
```

---

## API

All `/debates/*` routes are protected — clients must send `Authorization: Bearer <id_token>`. Verifier is `firebase` by default; set `AUTH_BACKEND=none` for local dev (any non-empty bearer token is accepted).

### `GET /healthz` · `GET /readyz`
Liveness / readiness — `200 OK`, no body.

### `GET /philosophers`
Returns all loaded philosophers (catalog metadata only — `tts_voice`/`tts_style` are not exposed).

```json
[
  {"name": "Karl Marx", "school": "Marxism", "era": "Modern", "dates": "1818–1883", "one_liner": "All history is the history of class struggle."},
  ...
]
```

### `POST /debates`
Creates a new debate session. Returns the full step manifest.

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

### `POST /debates/{id}/next`
Executes the next step in the pipeline. Supports both JSON and SSE streaming (`Accept: text/event-stream`).

JSON response:
```json
{
  "key": "karl_marx_r1",
  "label": "Karl Marx — Round 1",
  "author": "karl_marx_r1",
  "role": "a",
  "content": "...",
  "structured": null,
  "step_index": 1,
  "total_steps": 9,
  "done": false,
  "turn_input_tokens": 812,
  "turn_output_tokens": 243,
  "total_input_tokens": 812,
  "total_output_tokens": 243
}
```

`structured` is populated for `critic_feedback` and `final_verdict` when a JSON object can be extracted from the model output.

SSE response (when `Accept: text/event-stream`): streams `event: token` chunks during generation, then closes with `event: done` carrying the same JSON payload. Errors emerge as `event: error`.

Token counts are tracked cumulatively on the debate record and returned on every response.

### `POST /debates/{id}/tutor`
Asks a tutor agent to explain a passage from a previous turn. Supports SSE streaming on the same `Accept` header rule.

```json
{
  "selection": "surplus value",
  "turn_key": "karl_marx_r1",
  "history": [{"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}]
}
```

### `POST /debates/{id}/tts`
Streams a `audio/L16;rate=24000;channels=1` PCM stream synthesized from `text`, using the voice/style resolved from the named step's role and the debate's debaters. Returns 503 when `TTS_ENABLED` is unset. The `X-TTS-Voice` response header reports the chosen voice.

```json
{"step_key": "karl_marx_r1", "text": "..."}
```

Inline parentheticals (e.g. `asabiyya (social solidarity)`) are stripped server-side before synthesis to avoid awkward read-aloud asides.

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
| Storage backend | `STORAGE_BACKEND` env var | `memory` (default) or `firestore` |
| Auth backend | `AUTH_BACKEND` env var | `firebase` (default) or `none` (local dev only) |
| Data directory | `DATA_DIR` env var | Override path to `agents/` (default: binary directory) |

### Environment Variables

All env vars are read from `backend/.env` (see [`backend/.env.example`](backend/.env.example)).

**LLM + TTS — single OpenAI-compatible endpoint**

| Variable | Required? | Purpose |
|---|---|---|
| `LLM_API_KEY` | When using a real model | API key for the OpenAI-compatible provider. |
| `LLM_BASE_URL` | No | OpenAI-compatible base URL. Defaults to `https://openrouter.ai/api/v1`. Works with OpenAI, Together AI, Groq, Azure OpenAI, vLLM, Ollama, LM Studio, etc. |

**Text-to-speech (all optional)**

| Variable | Default | Purpose |
|---|---|---|
| `TTS_ENABLED` | unset (disabled) | Set to `TRUE` to expose `POST /debates/{id}/tts`. Routes through the same `LLM_BASE_URL`. |
| `TTS_MODEL` | `google/gemini-3.1-flash-tts-preview` | TTS model identifier in provider form. |

**Auth + storage** (Google Cloud only — LLM/TTS no longer use GCP)

| Variable | Default | Purpose |
|---|---|---|
| `AUTH_BACKEND` | `firebase` | `firebase` validates Firebase ID tokens; `none` accepts any non-empty Bearer token (local dev only). |
| `GOOGLE_CLOUD_PROJECT` | *(none)* | The GCP project this backend runs in. Used for `STORAGE_BACKEND=firestore` and Cloud Logging trace-clickthrough links. Required when `STORAGE_BACKEND=firestore`. Uses Application Default Credentials (`gcloud auth application-default login`). |
| `FIREBASE_PROJECT` | *(none)* | The project that issues the Firebase ID tokens the auth verifier validates — i.e. the project your frontend signs in against. **Required** when `AUTH_BACKEND=firebase`. May equal `GOOGLE_CLOUD_PROJECT` (single project) or differ from it when Firebase Hosting/Auth live in a separate project (e.g. a free Spark-plan project) than the backend's Cloud Run + Firestore. |
| `STORAGE_BACKEND` | `memory` | `memory` (in-process) or `firestore` (Cloud Run). |
| `FIRESTORE_DATABASE` | `(default)` | Firestore database id. |
| `FIRESTORE_COLLECTION` | `debates` | Firestore collection name. |

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

Requires `LLM_API_KEY` and the other mandatory env vars (see [`backend/.env.example`](backend/.env.example)). Set `AUTH_BACKEND=none` in `backend/.env` to skip Firebase init.

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
