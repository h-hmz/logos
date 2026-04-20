# ADR 0003: Conversational context via a named-slot scratchpad, not a rolling message history

Date: 2026-04-19
Status: Accepted

## Context

Each turn in a Logos debate is a separate LLM call, and every call
needs context from earlier turns: the moderator's topic, what the
opponent just said, the critic's feedback, and (sometimes) a user
interjection. There are three common ways to carry that context
forward:

1. **Rolling message history.** Accumulate a `[user, assistant, ...]`
   array and replay it on every turn. Simple, but every agent sees
   every prior message — provenance ("who said what") is conveyed only
   by role tags, and the window grows unboundedly.
2. **Agent-managed retrieval.** Give each agent tools to fetch past
   context from a vector store or memory service when it decides it
   needs to. Flexible, but introduces non-determinism: what an agent
   sees varies with its own tool-call choices.
3. **Named-slot scratchpad.** Keep a flat `key → string` map of prior
   turn outputs, and let each prompt explicitly name the slots it
   wants interpolated. No history replay, no retrieval — just
   substitution.

Logos is a fixed pipeline: moderator → A/B rounds → critic → A/B
responses → judge. The set of inputs each step needs is known at
build time. We don't need flexibility; we need the debate to be
reproducible and the per-turn prompt to be auditable.

## Decision

We use **option 3**, built on top of Google ADK's session-state and
`OutputKey` mechanism. No message history is ever passed to the
model; each turn is a standalone prompt with named slots filled
from persisted prior outputs.

### The round trip

For each debate we persist `Persisted.StepOutputs map[string]string`
— a flat dictionary of completed turns. Keys are deterministic
(`moderator_intro`, `karl_marx_r1`, `critic_feedback`,
`john_stuart_mill_critic_response`, `final_verdict`). Values are the
raw generated text.

One full turn cycle:

1. **Prompt is authored with named slots.** Every agent's
   `Instruction` is a plain Go-formatted string containing ADK
   template placeholders like `{topic}`, `{karl_marx_r1}`,
   `{critic_feedback}`, `{pending_interjection}`. Each placeholder
   names a key the agent expects to find in session state.
   See [philosopher.go:115-141](../backend/internal/philosopher/philosopher.go#L115-L141).
2. **The session is seeded on every request.** `Hydrate` creates a
   fresh `session.InMemoryService` session and writes three groups
   of entries into its state map: the static `{topic}`, every prior
   entry of `StepOutputs` flattened in as its own key, and the
   current `{pending_interjection}` (empty string if none). That is
   the agent's entire universe of context for the turn.
   See [debate.go:138-152](../backend/internal/debate/debate.go#L138-L152).
3. **ADK substitutes before dispatch.** When the runner executes the
   agent, ADK walks the `Instruction` string, replaces each `{key}`
   with the string value at `state[key]`, and sends the resulting
   text to Vertex. Any placeholder whose key is missing stays
   literal — which is why we always seed an empty
   `pending_interjection`.
4. **The output is written back under a declared key.** Every agent
   is constructed with an `OutputKey` (e.g. `"karl_marx_r1"` at
   [debate.go:278](../backend/internal/debate/debate.go#L278)).
   ADK writes the generated text into session state under that key
   as the turn completes. The service layer then copies that value
   into `Persisted.StepOutputs[step.Key]` and commits via the repo
   ([service.go:210-213](../backend/internal/debate/service.go#L210-L213)).

The next request hydrates a brand new session from `StepOutputs` and
the loop repeats. Session state itself is throwaway — the durable
memory lives in `Persisted`, which is JSON-serialisable and stored
through the ADR 0001 repository.

### What we rely on from ADK, and what we don't

- **We use:** session state as a string map, `{key}` template
  substitution on `Instruction`, and `OutputKey` for capturing turn
  output into state. These are load-bearing.
- **We don't use:** ADK's event history, memory services, or any
  agent-driven state mutation. A pure `strings.Replace` loop plus a
  map would reproduce the behaviour we depend on; ADK is a
  convenience, not the architecture.

## Consequences

### Positive

- **Every turn's prompt is reconstructible from `Persisted` alone.**
  Debugging a weird output means reading the `Instruction` template
  and looking up the referenced keys in `StepOutputs`. No replay,
  no tool-call trace to untangle.
- **Provenance is structural.** An opponent's argument arrives under
  a heading (`## Your Opponent's Last Argument`) with its text
  substituted in directly; the model cannot confuse who said what,
  because the role is baked into the surrounding prose rather than
  into a `role: assistant` tag shared by both debaters.
- **Rehydration is cheap and stateless.** `Hydrate` is a map copy and
  a session create — no vector DB, no embeddings, no eviction
  policy. This matches Cloud Run's scale-to-zero model: any instance
  can serve the next turn given only the persisted snapshot.
- **The prompt is an allowlist, not a denylist.** A critic cannot
  accidentally quote a user interjection it wasn't supposed to see,
  because its `Instruction` simply never references that key.

### Negative

- **The two main costs interact.** Because agents can only see keys
  the template names, the obvious way to give them "memory of round
  1 in round 3" is to add `{name}_r1` to the round-3 template — and
  every key we add inflates the prompt by that turn's full text.
  The tradeoff is explicit: rigidity is the knob that controls
  prompt bloat. We accept bloat growing linearly with round count
  rather than adding a summariser (which would reintroduce the
  determinism problems option 2 has).
- **Literal braces in agent output can collide with the template
  engine.** A philosopher writing `{...}` in prose would produce a
  placeholder on the *next* turn if that substring happened to
  match a state key. In practice keys are long and
  underscore-separated (`john_stuart_mill_r2`), so collisions are
  implausible, but the failure mode is silent substitution rather
  than an error.
- **No cross-debate memory.** Everything an agent knows lives in one
  debate's `StepOutputs`. If we ever want agents to reference prior
  debates or external source material, this design gives us no
  hook for it — we'd need to add option 2 alongside, not extend
  this.

## Implementation

- [backend/internal/debate/debate.go](../backend/internal/debate/debate.go)
  — `Persisted.StepOutputs`, `Hydrate` (session seeding), and
  `BuildSteps` (per-agent `OutputKey` wiring).
- [backend/internal/debate/service.go](../backend/internal/debate/service.go)
  — copies the post-turn output into `StepOutputs` and commits.
- [backend/internal/philosopher/philosopher.go](../backend/internal/philosopher/philosopher.go)
  — `Make` assembles the `{opponent}` / `{critic}` /
  `{pending_interjection}` slot set per turn.
