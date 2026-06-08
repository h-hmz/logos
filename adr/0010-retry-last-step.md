# Retry of the last debate step

A debate's steps are an append-only list of records, each holding one
generation's content, model, tokens, and cost; `Current()` (their count) is the
next step to run. The owner may **retry the last completed step** — re-run its
generation and replace the stored record in place — via a dedicated
`POST /debates/{id}/retry` (and `Service.RetryTurn`) that reuses the same
in-flight lock, budget gate, `charge` wrapper, and SSE streaming as `/next`
(ADR 0009). The retry names the allowlisted model to re-roll on, and that model is recorded on the new step record, so a re-roll can run the step on a different model than its first attempt and only that one step is affected.

Only the **last** step is retryable: the request names the step key it expects to
redo, and the service refuses with **409** when that key is not the debate's
current last record. A non-last key and a key whose step was last but has since
been superseded are indistinguishable from a key alone, and both resolve the same
way — refetch and retry — so they map to the one conflict status, matching the
stale-view contract `/next` already has (ADR 0004). An empty debate (nothing to
retry) and a missing key are **400**. The retry runs against a clone with that
last record popped, so the regenerated step sees every prior step but not its own
old output, and the replacement is committed with a single optimistic-version
`repo.Update` only on success. A retried debater step keeps the interjection its
original generation ran under; referee steps (moderator, critic, judge) ignore
interjections, so for them it is moot. Retry is allowed even when the debate is
complete — that is how the judge's verdict gets re-rolled. The tutor is out of
scope: it is not a pipeline step, so it has no "last step" to retry.

## Considered options

- **Keep attempt history (store every generation, mark one active):** lets users
  compare and revert, but reworks the record schema, hydration, totals, and
  session reconstruction. Rejected for replace-in-place — the simplest thing that
  satisfies the feature; history can be added later if a real need appears.
- **Retry any step, not just the last:** rejected. A step's output feeds later
  steps through the shared state map (ADR 0003), so retrying a middle step would
  orphan every downstream step that consumed it, forcing a cascade re-generation.
  Restricting to the last step makes a retry a self-contained pop-and-rerun with
  no invalidation fan-out.
- **A `?retry=true` flag on `/next`:** rejected. Retry's preconditions are the
  inverse of `next`'s (it requires a completed step and is allowed when the debate
  is *complete*), so a distinct route keeps the two validations from entangling.
- **Refund or skip charging the discarded generation:** rejected (see below).

## Consequences

- **The debate's displayed cost and the owner's quota spend diverge on retry —
  deliberately.** The discarded generation was really billed by the provider, so
  its cost stays counted against the owner's monotonic quota `spent_usd` (ADR
  0009); the debate's own per-step totals, summed from surviving records, show
  only the kept generation. Quota is the real-spend ledger; the debate total is
  the current-content ledger. Refunding would understate money actually spent
  (and need negative quota deltas); not charging would let a user burn unbounded
  provider spend for free. A retry is therefore metered exactly like any
  generation and can be refused with **402** when the owner is over budget —
  re-rolling on an expensive model just depletes quota faster.
- **A retry destroys the prior generation with no history.** Replace-in-place is
  irreversible — once the new generation commits, the old content/tokens/cost/
  model are gone. Accepted for simplicity; revisiting means a schema change, not
  just a code change.
- **A failed retry is non-destructive.** Because the pop lives on a clone and only
  a successful generation is committed, an agent/provider error leaves the
  original record and the whole debate untouched — the owner keeps the answer they
  had.
- **One retry in flight per user, same as a turn.** Retry goes through the same
  per-UID in-flight lock, so it cannot run concurrently with another generation
  for that user and is refused with **409** if one is already running.
- **A retry can change a step's model.** The model that produced each step is
  recorded on it (`StepRecord.Model`, surfaced via `turns[].model`), so a turn can
  be labelled with its model and the retry picker can default to the step's
  current one; a debater's turns may therefore run on different models, with
  per-role consistency a frontend convention rather than something enforced.
