# Logos — Debate Context

Logos runs structured philosophical debates: two philosophers argue a topic
through a fixed pipeline of model-generated steps, refereed by a moderator,
critic, and judge. This glossary fixes the language used across the backend
debate engine and its HTTP surface.

## Language

**Debate**:
A topic argued by two philosophers through a fixed, ordered pipeline of steps,
created and owned by one caller.
_Avoid_: session, conversation, match

**Step**:
One node of the debate pipeline (moderator intro, a debater round, the critic
analysis, a critic response, the judge verdict). Steps run in a fixed order and
each produces exactly one generation.
_Avoid_: stage, phase, node

**Generation**:
A single model production — the content, token counts, and provider cost from
one model call. The unit that quota meters and that a retry re-rolls. A step has
one generation; a tutor reply is also a generation.
_Avoid_: completion, call, inference

**Turn**:
The act (and recorded outcome) of advancing a debate by running its next
not-yet-run step.
_Avoid_: move, round (a round is a debater-pair of steps, not a single turn)

**Retry**:
Re-running the generation for a step that already completed, discarding its
record and replacing it with the fresh result. Only the **last step** is
retryable, and the replaced generation leaves no history. A retry may run the
step on a different [[model]], but only for that one re-roll.
_Avoid_: redo, regenerate, re-roll, undo

**Last step**:
The most recently completed step of a debate — the only step a retry may target.
For a completed debate this is the judge's verdict.

**Interjection**:
An owner's one-time steering note attached to a single debater turn. Referee
steps (moderator, critic, judge) ignore interjections. A retried debater step
keeps the interjection its original generation ran under.
_Avoid_: prompt, note, comment, instruction

**Owner**:
The caller who created a debate; the only one permitted to advance or retry it.
Reads stay open to anyone holding the debate's (unguessable) ID.
_Avoid_: user, author, creator

**Role**:
Which participant a step speaks as — moderator, debater (A or B), critic, or
judge. A role is not bound to a model; the [[model]] is chosen per [[generation]].
_Avoid_: persona, actor

**Model**:
The LLM a single [[generation]] runs on. The client names it on every generation
request (advance, retry, tutor); it must be in the [[allowlist]] and is recorded
on the step. There is no debate-level or operator default — the client supplies a
model each time, and the same role's steps need not share one.
_Avoid_: engine, default model

**Debater Model**:
The [[model]] the client runs a given debater's turns on — a per-debater choice
the frontend holds and sends on each of that debater's [[generation]]s. The two
debaters may differ; like any model it must be in the [[allowlist]].
_Avoid_: player model

**Allowlist**:
The authorized set of models, and the **sole** authority over them: every
[[generation]]'s model must be in it. There is no trusted model outside it — to
change which models are available, change the allowlist.
_Avoid_: whitelist

**Tutor**:
An on-demand plain-language explanation of existing debate content. The tutor is
a generation but **not** a pipeline step, so it is never part of a debate's
steps and is not retryable.
_Avoid_: helper, assistant, guide

**Narration**:
The synthesized audio rendering of a [[step]]'s text — the spoken form of what a
debater said. A narration is a model call that costs money, but it is **not** a
[[generation]] or a [[step]]: it renders content that already exists rather than
producing new content, so it sits outside the debate pipeline and a [[retry]]
never re-rolls it. Only debater [[role]]s are narratable today. Two narrations
are the same exactly when their text, voice, style, and model match; a [[retry]]
that changes a step's text therefore yields a different narration.
_Avoid_: generation (a narration is not the unit quota meters), clip, recording, audio

## Example dialogue

> **Dev:** When the user hits retry, are we re-running the whole round?
>
> **Domain expert:** No — a retry re-runs one *step*'s *generation*, and only
> the *last* one. If the *judge's verdict* just came in, retry re-rolls that
> verdict and throws the old one away. Nothing earlier moves.
>
> **Dev:** What if that last step was a *debater* turn the owner had steered?
>
> **Domain expert:** The retry keeps the same *interjection*. Same steering,
> fresh dice. A retry never changes the inputs — it just asks the model again.
>
> **Dev:** And the cost of the thrown-away generation?
>
> **Domain expert:** It was really spent, so it stays counted against the
> owner's quota. The debate's own displayed total only shows the generation it
> kept. Two ledgers, on purpose.
>
> **Dev:** Where does each step's *model* come from?
>
> **Domain expert:** The client names it on every *generation* — there's no
> server default. It has to be in the *allowlist*, and we stamp it onto the step.
> The frontend keeps a *debater model* per corner and resends it each turn, so a
> debater stays on one model by convention, not because the backend pins it.
>
> **Dev:** So a *retry* on a different model?
>
> **Domain expert:** Same mechanism — the re-roll just names a different model.
> It only changes that one step.
