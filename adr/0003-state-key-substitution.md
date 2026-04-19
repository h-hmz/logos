# ADR 0003: Pure "State-Key" Substitution vs. Agent Memory Subsystems

Date: 2026-04-19
Status: Accepted

## Context

In multi-agent systems, managing conversational context (memory) between turns is an architecturally complex problem. The two most common patterns seen in the ecosystem are:
1. **Dynamic Message Histories:** Maintaining an ongoing array of chat messages (`user`, `assistant`, `system`) and appending to it continuously.
2. **Abstract External Memory (RAG / Vector DBs):** Allowing agents to use specialized external tools to store and retrieve past context dynamically when they need it.

In Logos, our goal is to build a very rigid, highly deterministic pipeline where A talks to B, a Critic evaluates them, and a Judge rules. Allowing the LLMs to handle their own memory fetches or handing them a massive, unstructured timeline roll of all messages risks the "lost in the middle" problem or hallucinations about who said what.

## Decision

We chose to explicitly ditch dynamic conversation arrays and external memory subsystems. Instead, we use **Pure State-Key Substitution** powered by `session.InMemoryService` from the Google ADK.

### 1. Templated Key Injections

Agents do not receive a "chat history" payload. Instead, they receive a static `Instruction` prompt that contains explicit template slots wrapped in braces (`{}`). 

For example, a philosopher at `Round 2` receives an instruction parameterized like this:
```markdown
## Your Opponent's Last Argument
{karl_marx_r1}

## Your Task: round 2 — respond to your opponent then advance your argument
Speak in first person...
```

### 2. The `StepOutputs` Dictionary

As the server processes steps linearly, the text output from each step is saved directly into a persistent `map[string]string` called `StepOutputs`. 
The key is determined deterministically (e.g., `karl_marx_r1`).

When a `/next` request arrives, the `Hydrate()` function rebuilds a lightweight ADK session and seeds it with exactly three things:
1. The `{topic}`
2. The flat `StepOutputs` map
3. Any `{pending_interjection}` from the user

The ADK framework intercepts the bracketed keys in the prompt and interpolates the corresponding text strings directly from the session map before sending the payload to the Vertex API.

## Consequences

### Positive

- **Deterministic Context Window:** We have 100% control over exactly what an agent is looking at during any given step. There is never any ambiguity about context leakage. 
- **Zero Hallucination on Identities:** Because the opponent's argument is explicitly injected under the heading `## Your Opponent's Last Argument`, the LLM never incorrectly attributes a quote to the wrong agent.
- **Cheap Rehydration:** Since we just maintain a flat key-value map (`Persisted.StepOutputs`), "hydrating" a session for a scale-to-zero Cloud Run environment takes microseconds and zeroes out the need for complex vector databases or chunking algorithms.
- **Trivial Debugging:** If an agent acts strange, debugging the state simply requires inspecting the JSON map of `StepOutputs` to see exactly what string populated the bracketed variable.

### Negative

- **Rigid Memory:** The agents have no mechanism for "remembering" something from 3 rounds ago unless we explicitly wire a prompt key parameter to supply it (or we supply all previous `{name}_r{n}` keys). They only know what the template string tells them.
- **Prompt Size Bloat:** Because we inject literal text outputs directly into the prompt on every turn, pipeline steps like the Critic or the Judge (which receive `{name}_r1`, `{name}_r2`, etc.) consume significantly more input tokens as the debate drags on (directly increasing API cost and latency).
- **String Escaping Quirks:** Any literal brace `{` or `}` written by an agent in their output might theoretically disrupt the string template engine if not handled gracefully.

## Implementation

- `backend/internal/debate/debate.go` — The `StepOutputs` map, the `Hydrate()` session injection logic.
- `backend/internal/philosopher/philosopher.go` — The `Make` function that conditionally attaches `{OpponentArgKey}`, `{CriticKey}`, and `{pending_interjection}` logic to the prompt.
