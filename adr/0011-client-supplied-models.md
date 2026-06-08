# Client-supplied models

Every generation — advancing a turn, retrying a step, or a tutor reply — names
the model it runs on. The backend validates that model against the allowlist
(`models.Allowed`), the sole authority over which models may run, and records it
on the step (ADR 0010). There is no server-side default model and a debate stores
no per-role model: the frontend owns model selection. It holds a per-debater
model (the two debaters may differ) and a single referee model for the moderator,
critic, and judge; it sends the right one on each request and, on rehydrate,
reconstructs each role's current model from `turns[].model`, defaulting roles it
hasn't seen yet. The allowlist is small and fixed, so the frontend keeps its own
hardcoded copy to populate its pickers rather than fetching it; `models.go` stays
the validation authority, and a client entry that drifts out of sync is rejected
with a **400**.

## Consequences

- **The allowlist is the only authority over models.** Changing the available set
  means changing `models.go` (and the frontend's mirror of it); a non-allowlisted
  or empty model is a **400**.
- **"One model per debater" is a frontend convention, not a backend guarantee.** A
  client may send different models across a debater's turns; only the user's own
  debate is affected.
- **Every LLM call needs a valid allowlisted model, and the frontend always has
  one** (its hardcoded list plus a default), so there is no "no model to send"
  failure path and no runtime dependency on a models endpoint.
- **A rejected provider key is distinct from a rejected model.** A 401/403 from
  the model provider surfaces as a **502** ("the model provider rejected the
  server's API key"); an unallowed/empty model is the **400** above.
