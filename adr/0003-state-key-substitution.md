# ADR 0003: Conversational context via a named-slot scratchpad, not a rolling message history

Date: 2026-04-19
Revised: 2026-05-10
Status: Accepted (revised — implementation details refreshed against current code)

## Context

Each turn in a Logos debate is a separate LLM call, and every call
needs context from earlier turns: the moderator's topic, what the
opponent just said, and the critic's feedback. There are three
common ways to carry that context forward:

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

We use **option 3**, originally built on top of Google ADK's
session-state and `OutputKey` mechanism and now implemented directly
in `internal/agent` (per
[ADR 0008](0008-in-house-agent-layer-replacing-adk.md)). No message
history is ever passed to the model; each turn is a standalone prompt
with named slots filled from persisted prior outputs.

### The round trip

For each debate we persist `Persisted.StepOutputs map[string]string`
— a flat dictionary of completed turns. Keys are deterministic
(`moderator_intro`, `karl_marx_r1`, `critic_feedback`,
`john_stuart_mill_critic_response`, `final_verdict`). Values are the
raw generated text.

One full turn cycle:

1. **Prompt is authored with named slots.** Every agent's
   `Instruction` is a plain Go-formatted string containing template
   placeholders like `{topic}`, `{karl_marx_r1}`, `{critic_feedback}`.
   Each placeholder names a key the agent expects to find in the
   per-request `agent.State` map.
   See [philosopher.go](../backend/internal/philosopher/philosopher.go).
2. **The state map is seeded on every request.** `Hydrate` builds a
   fresh `agent.State` (a `map[string]string`) containing the static
   `{topic}` plus every prior entry of `StepOutputs` flattened in as
   its own key. That is the agent's entire universe of context for
   the turn.
   See [debate.go](../backend/internal/debate/debate.go).
3. **`agent.Run` substitutes before dispatch.** Before calling the
   underlying LLM, `Agent.Run` walks the `Instruction` string,
   replaces each `{identifier}` with the string value at
   `state[identifier]`, and sends the resulting text as the system
   message. Any placeholder whose key is missing causes
   `substitute` to return an error listing every missing key, so
   prompts can only reference slots the hydration step has seeded.
   Implementation:
   [internal/agent/state.go](../backend/internal/agent/state.go)
   (package-private `substitute`).
4. **The output is written back under a declared key.** Every agent
   is constructed with an `OutputKey`. When `Agent.Run` finishes,
   it writes the generated text into the `State` map under that key
   (skipped when `OutputKey == ""`, e.g. for the ephemeral tutor).
   The service layer then copies that value into
   `Persisted.StepOutputs[step.Key]` and commits via the repo. See
   [internal/agent/agent.go](../backend/internal/agent/agent.go) and
   [debate/service.go](../backend/internal/debate/service.go).

The next request hydrates a brand new state map from `StepOutputs`
and the loop repeats. State itself is throwaway — the durable memory
lives in `Persisted`, which is JSON-serialisable and stored through
the ADR 0001 repository.

### What we own

Substitution is implemented in
[internal/agent/state.go](../backend/internal/agent/state.go) as a
regex-based `{identifier}` resolver against an `agent.State`
(`map[string]string`). It is package-private (`substitute`) — only
`Agent.Run` calls it. `OutputKey` write-back happens at the end of
`Agent.Run` in
[internal/agent/agent.go](../backend/internal/agent/agent.go). The
write-back behaviour matches the pre-2026-04-30 ADK semantics; the
missing-key behaviour was tightened from "silent empty string" to
"return an error" in the in-house move (per
[ADR 0008](0008-in-house-agent-layer-replacing-adk.md)) so that an
unseeded slot fails loudly instead of producing a malformed prompt.

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
- **The prompt is an allowlist, not a denylist.** An agent cannot
  accidentally pull in context it wasn't supposed to see, because
  its `Instruction` simply never references that key.

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
- **The substitution regex is now ours to maintain.** Strict
  `\{[A-Za-z_][A-Za-z0-9_]*\}` matching: the opening `{` must be
  immediately followed by an identifier character, so shapes like
  `{ identifier }` (with whitespace) or `{0invalid}` pass through as
  literal text. The strictness has no critical live consumer today —
  critic and judge moved to API-enforced JSON Schema (see ADR 0008
  revision), and no other prompt embeds literal `{ ... }` content —
  but loosening it would silently retroactively eat any such text in
  future prompts.

## Revision history

- 2026-04-19 — original; substitution and `OutputKey` provided by ADK.
- 2026-04-30 — substitution and `OutputKey` write-back moved into
  `internal/agent` (per
  [ADR 0008](0008-in-house-agent-layer-replacing-adk.md)). No
  behavioural change to debaters, critic, or judge prompts; the
  named-slot scratchpad design (option 3) remains the chosen
  approach.
- 2026-05-10 — implementation-detail refresh against current code.
  No design change. Corrects: `State` is `map[string]string` (not
  `map[string]any`); `substitute` is package-private (not exported
  `Substitute`); missing keys now return an error instead of
  resolving to the empty string. Also drops the "guards against JSON
  braces in critic/judge" motivation for the strict regex — the
  structured-outputs migration (commit 659a3bf, 2026-05-09) replaced
  inline JSON examples in those prompts with API-enforced JSON
  Schema, so no current prompt embeds literal `{ ... }` text.
- 2026-06-02 — `BuildSteps` (which constructed every step's agent up
  front) replaced by `pipeline`, which builds each step's agent and its
  `OutputKey` wiring lazily; a turn constructs only the one agent it
  runs. The named-slot scratchpad design is unchanged — each step still
  reads `{key}` placeholders and writes back via its `OutputKey`.

## Implementation

- [backend/internal/agent/state.go](../backend/internal/agent/state.go)
  — `State` map type and the package-private `substitute` resolver.
- [backend/internal/agent/agent.go](../backend/internal/agent/agent.go)
  — `Agent.Run` performs substitution and the `OutputKey` write-back.
- [backend/internal/debate/debate.go](../backend/internal/debate/debate.go)
  — `Persisted.StepOutputs`, `Hydrate` (state seeding), and
  `pipeline` (per-agent `OutputKey` wiring, built lazily per step).
- [backend/internal/debate/service.go](../backend/internal/debate/service.go)
  — copies the post-turn output into `StepOutputs` and commits.
- [backend/internal/philosopher/philosopher.go](../backend/internal/philosopher/philosopher.go)
  — `Make` assembles the `{opponent}` / `{critic}` slot set per turn.
