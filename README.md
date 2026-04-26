# Logos — Multi-Agent Philosophical Debate

A structured debate system built with [Google ADK](https://google.github.io/adk-docs/) and Vertex AI. Two philosophers debate a topic head-to-head across configurable rounds. A critic intervenes with a logical analysis, each philosopher responds, and a judge delivers a structured verdict.

---

## Architecture

```
backend/
├── main.go                         # entry point — config loading, server startup
├── go.mod
├── Dockerfile
├── .env                            # credentials (gitignored)
├── agents/
│   └── philosophers/               # philosopher repository — one YAML file per philosopher
└── internal/
    ├── config/       config.go     # .env loading
    ├── modelcache/   modelcache.go # lazy model.LLM cache (string ID → ADK model object)
    ├── philosopher/  philosopher.go # YAML loader, Key() normalizer, Make() factory
    ├── critic/       critic.go     # FallacyTaxonomy, MakeCritic() factory
    ├── judge/        judge.go      # MakeJudge() factory
    ├── debate/       debate.go     # Step, State, Store, BuildSteps()
    └── server/       server.go     # Chi router — all HTTP handlers
                      sse.go        # SSE streaming helper

frontend/                           # React + Vite (proxies to backend:8000)
```

All agent prompts live in their respective package. `server.go` contains no prompts — only wiring.

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

## Session State

All inter-agent communication passes through ADK's `session.InMemoryService`. No tools, no external memory — pure state key substitution in prompts via `{key}` syntax.

| Key | Written by | Read by |
|---|---|---|
| `topic` | seeded at debate creation | all agents |
| `{name_key}_r{n}` | philosopher turn (e.g. `karl_marx_r2`) | next philosopher, critic, judge |
| `critic_feedback` | Critic | both critic-response turns, judge |
| `{name_key}_critic_response` | philosopher critic-response turn | judge |
| `final_verdict` | Judge | (terminal) |

Each debate gets its own `session.InMemoryService` instance — state is fully isolated between concurrent debates. A per-debate mutex serializes concurrent `/next` calls.

---

## Agents

### Moderator
Inline `llmagent` in `internal/debate/debate.go`. Introduces the pair, states the topic, explains the format, and announces who opens. Reads `{topic}`. Writes to `moderator_intro`.

### Philosopher — `philosopher.Make()`
Defined in `internal/philosopher/philosopher.go`. One `LlmAgent` is created per debate turn; each instance is given a unique `AgentName` and `OutputKey`.

The prompt is assembled from the philosopher's YAML fields:

| YAML field | Role in prompt |
|---|---|
| `name` | Speaker identity |
| `school` | Philosophical tradition |
| `identity` | Core worldview block |
| `key_thinkers` | Thinkers to reference |
| `epistemic_sources` | *(optional)* Foundational sources for theological traditions (Quran, Bible, Upanishads…) |

The factory also injects:
- `{topic}` from session state
- `{opponent_arg_key}` — the opponent's previous turn (if any)
- `{critic_key}` — the critic's feedback (for post-critic turns)
- `DebateRules(maxWords)` — shared rules block with the configured word limit

#### `philosopher.Key(name)`
Normalizes a display name to a safe identifier used for all state keys and ADK agent names:

```go
philosopher.Key("John Stuart Mill")   // → "john_stuart_mill"
philosopher.Key("Al-Ghazali")         // → "al_ghazali"
philosopher.Key("Simone de Beauvoir") // → "simone_de_beauvoir"
```

This allows full display names in YAML while keeping state keys and ADK agent names alphanumeric.

### Critic — `critic.MakeCritic()`
Defined in `internal/critic/critic.go`. Receives the full debate transcript via `{name_key}_r{n}` substitutions.

Evaluates reasoning quality using `FallacyTaxonomy`:

- Ad hominem, Straw man, Circular reasoning
- Appeal to authority, False dichotomy, Equivocation
- Non sequitur, Appeal to tradition

**Theological exception**: Citing scripture (Quran, Bible, Upanishads, etc.) is not automatically flagged as "Appeal to Authority". The critic applies a three-way distinction:
- **Valid internal move** — citing one's own scripture to ground a claim within one's tradition. Not a fallacy; noted as a faith-based premise.
- **Contested cross-tradition premise** — citing scripture as if it should bind an opponent who doesn't share that revelation. Flagged as an unargued premise requiring independent justification.
- **Still a fallacy** — citing a scholar or theologian (not scripture) as proof without engaging their argument.

Output per debater: strongest argument, key weakness, detected fallacies, pointed challenge. Writes to `critic_feedback`.

### Judge — `judge.MakeJudge()`
Defined in `internal/judge/judge.go`. Receives the complete record: all rounds, critic feedback, and both critic responses.

Produces a structured verdict:
1. **Summary of core positions**
2. **Key points of clash** — sharpest moments of direct disagreement
3. **Position shifts** — any concessions or evolution across rounds
4. **Argument rankings** — who made the stronger case and why
5. **Synthesis** — a genuine philosophical position integrating the best of both
6. **Confidence assessment** — score 1–10 with explanation
7. **Open questions** — unresolved threads for further inquiry

Writes to `final_verdict`.

---

## Adding a Philosopher

Drop a new `.yaml` file in `backend/agents/philosophers/`. Name the file after `philosopher.Key(name)` (e.g. `friedrich_nietzsche.yaml`). No code changes required — the loader scans the directory at startup.

Required fields:

```yaml
name: Friedrich Nietzsche
school: Existentialism / Perspectivism
identity: |
  You believe God is dead and with him the foundations of Western morality...
key_thinkers: Nietzsche, Schopenhauer, Heraclitus, Dostoevsky
```

Optional field for theological traditions:

```yaml
epistemic_sources: |
  The Quran is the direct and literal word of God, transmitted through the Prophet Muhammad...
```

---

## API

The backend exposes three endpoints. The frontend proxies all of them transparently.

### `GET /philosophers`
Returns all loaded philosophers.

```json
[{"name": "Karl Marx", "school": "Marxism"}, ...]
```

### `POST /debates`
Creates a new debate session. Returns the full step manifest.

```json
{
  "topic": "Is capitalism inevitable?",
  "debater_a": "Karl Marx",
  "debater_b": "Friedrich Hayek",
  "num_rounds": 2,
  "max_words": 250
}
```

### `POST /debates/{id}/next`
Executes the next step in the pipeline. Supports both JSON and SSE streaming (`Accept: text/event-stream`).

**JSON response:**
```json
{
  "key": "karl_marx_r1",
  "label": "Karl Marx — Round 1",
  "content": "...",
  "turn_input_tokens": 812,
  "turn_output_tokens": 243,
  "total_input_tokens": 812,
  "total_output_tokens": 243,
  "done": false
}
```

**SSE response** (when `Accept: text/event-stream`): streams `event: token` chunks during generation, then closes with `event: done` containing the same JSON payload above.

Token counts are tracked cumulatively on the debate state and returned on every response.

---

## Configuration

| What | Where | Effect |
|---|---|---|
| Model | `MODEL` env var | Gemini model id used for all agents; defaults to `gemini-3-flash-preview` |
| Philosopher pool | `backend/agents/philosophers/*.yaml` | Add / remove / edit — restart to pick up changes |
| Max rounds | `POST /debates` — `num_rounds` | 1–5 |
| Word limit | `POST /debates` — `max_words` | Philosopher turns; critic gets `2×`, moderator gets `0.8×` |
| Data directory | `DATA_DIR` env var | Override path to `agents/` (default: binary directory) |

### Environment Variables

All env vars are read from `backend/.env` (see [`backend/.env.example`](backend/.env.example)).

**Main app — pick one auth path**

| Variable | Required? | Purpose |
|---|---|---|
| `GOOGLE_CLOUD_PROJECT` | Vertex path | GCP project id. Uses Application Default Credentials (`gcloud auth application-default login`) — no key file needed. |
| `GOOGLE_CLOUD_LOCATION` | Vertex path | GCP region (e.g. `global`, `us-central1`). |
| `GOOGLE_GENAI_USE_VERTEXAI` | Vertex path | Set to `TRUE` to route model calls through Vertex AI. |
| `GOOGLE_API_KEY` | API-key path | Gemini API key. Used when not on Vertex; also the fallback TTS key (see below). |

**Text-to-speech (all optional)**

| Variable | Default | Purpose |
|---|---|---|
| `TTS_ENABLED` | `FALSE` | Set to `TRUE` to expose the `/debates/{id}/tts` endpoint. TTS runs on Google's Gemini TTS models; no other providers are supported. |
| `TTS_MODEL` | `gemini-2.5-pro-preview-tts` | Gemini TTS model id. Pro preview is API-only (not on Vertex); use `gemini-2.5-flash-preview-tts` to run TTS on Vertex. |
| `TTS_API_KEY` | *(falls back to `GOOGLE_API_KEY`)* | TTS-specific Gemini API key. Lets the main app run on Vertex while TTS runs on the Gemini API. |

**Server**

| Variable | Default | Purpose |
|---|---|---|
| `MODEL` | `gemini-3-flash-preview` | Gemini model id used for all debate agents. |
| `PORT` | `8000` | HTTP listen port. |
| `DATA_DIR` | *(binary directory)* | Path to the directory containing `agents/`. |

---

## Running

### Prerequisites

Authenticate with Google Cloud:
```bash
gcloud auth application-default login
```

Configure `backend/.env`:
```ini
GOOGLE_CLOUD_PROJECT="your-gcp-project-id"
GOOGLE_CLOUD_LOCATION="us-central1"
GOOGLE_GENAI_USE_VERTEXAI=TRUE
```

### Backend
```bash
cd backend && go run .
```

### Frontend (development)
```bash
cd frontend && yarn dev
```

The Vite dev server proxies `/philosophers` and `/debates` to `localhost:8000` — no CORS configuration needed in development.

### Docker (Cloud Run)
```bash
cd backend && docker build -t logos .
docker run -p 8080:8080 logos
```
