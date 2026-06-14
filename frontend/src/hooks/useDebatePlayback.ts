import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, streamTurn, streamRetry, type DebateModels, type DebateSession, type TurnResult } from '../api'
import { roleForLabel } from '../step-role'
import { useQuota } from '../QuotaContext'

const STORAGE_KEY = (id: string) => `logos_step_${id}`
const AUTO_ADVANCE_DELAY_MS = 1400

export interface TurnBuffer {
  text: string
  // fullText is set the moment streamTurn resolves with the complete content,
  // even while the paced reveal is still ticking text onto the screen. It
  // unblocks audio synthesis ahead of the visual reveal.
  fullText?: string
  done: boolean
  // tokens is this turn's output token count; inputTokens its prompt count and
  // costUSD its provider-billed cost. All zero until the turn completes.
  tokens: number
  inputTokens: number
  costUSD: number
  // Generation latency in milliseconds from the server: time to first token and
  // total duration. Zero until the turn completes.
  ttftMs: number
  durationMs: number
  streaming: boolean
  error?: string
  // The owner's interjection that steered this turn, if any — stashed at turn
  // start so it renders immediately, and seeded from prior turns on rehydrate.
  interjection?: string
  // The model that produced this turn (a retry may run a step on a different
  // model). Seeded from prior turns on rehydrate; used to default the retry
  // picker to the step's current model.
  model?: string
}

export interface DebatePlayback {
  idx: number
  buffers: Record<number, TurnBuffer>
  turnResults: Record<number, TurnResult>
  totalIn: number
  totalOut: number
  advance: (interjection?: string) => Promise<void>
  // retry re-rolls the last completed step (idx - 1) in place, leaving the
  // playhead put. model, when non-empty, is the Retry Model the re-roll runs on;
  // empty keeps the step's current model. No-op when there is no completed step.
  retry: (model: string) => Promise<void>
}

export function useDebatePlayback(
  debate: DebateSession,
  initialTurns: Record<number, TurnResult>,
  onTurnCompleted: (i: number, t: TurnResult) => void,
  auto: boolean,
  // roleModels is the client's per-role model choice.
  roleModels: DebateModels,
): DebatePlayback {
  const [idx, setIdx] = useState<number>(() => {
    let fromTurns = 0
    for (const k of Object.keys(initialTurns)) {
      const i = parseInt(k, 10)
      if (Number.isFinite(i) && i + 1 > fromTurns) fromTurns = i + 1
    }
    let fromStorage = 0
    try {
      const saved = parseInt(localStorage.getItem(STORAGE_KEY(debate.id)) || '', 10)
      if (Number.isFinite(saved)) fromStorage = saved
    } catch { /* noop */ }
    return Math.max(0, Math.min(Math.max(fromStorage, fromTurns), debate.total_steps))
  })
  const [buffers, setBuffers] = useState<Record<number, TurnBuffer>>(() => {
    const b: Record<number, TurnBuffer> = {}
    for (const [k, t] of Object.entries(initialTurns)) {
      b[+k] = {
        text: t.content,
        done: true,
        tokens: t.turn_output_tokens ?? 0,
        inputTokens: t.turn_input_tokens ?? 0,
        costUSD: t.turn_cost_usd ?? 0,
        ttftMs: t.turn_ttft_ms ?? 0,
        durationMs: t.turn_duration_ms ?? 0,
        streaming: false,
        interjection: t.interjection,
        model: t.model,
      }
    }
    return b
  })
  const [turnResults, setTurnResults] = useState<Record<number, TurnResult>>(initialTurns)
  const [totalIn, setTotalIn] = useState(() => {
    let max = 0
    for (const t of Object.values(initialTurns)) max = Math.max(max, t.total_input_tokens ?? 0)
    return max
  })
  const [totalOut, setTotalOut] = useState(() => {
    let max = 0
    for (const t of Object.values(initialTurns)) max = Math.max(max, t.total_output_tokens ?? 0)
    return max
  })
  const inFlight = useRef(false)
  const lastCompletedRef = useRef(-1)
  // Mirror of buffers for synchronous reads inside callbacks (e.g. snapshotting
  // a step before a retry overwrites it) without making those callbacks depend
  // on buffers and churn on every token.
  const buffersRef = useRef(buffers)
  useEffect(() => { buffersRef.current = buffers }, [buffers])
  const { refresh: refreshQuota } = useQuota()

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY(debate.id), String(idx))
    } catch (e) {
      console.warn('[logos] Failed to persist step index:', e)
    }
  }, [idx, debate.id])

  // runStep drives one generation for stepIndex: it flips the step's buffer to
  // streaming, appends tokens as they arrive, and on success records the final
  // TurnResult, updates totals, and refreshes the budget. advancePlayhead moves
  // the playhead to the next step (a forward turn); a retry leaves it put and
  // replaces the step in place. A failed retry restores the prior buffer, since
  // the server leaves the original generation untouched on failure.
  const runStep = useCallback(
    async (
      stepIndex: number,
      note: string | undefined,
      stream: (onToken: (chunk: string) => void) => Promise<TurnResult>,
      advancePlayhead: boolean,
    ) => {
      if (inFlight.current) return
      inFlight.current = true
      const prior = buffersRef.current[stepIndex]
      setBuffers((prev) => ({
        ...prev,
        [stepIndex]: {
          text: '', done: false, tokens: 0, inputTokens: 0, costUSD: 0,
          ttftMs: 0, durationMs: 0,
          streaming: true, interjection: note ?? prev[stepIndex]?.interjection,
          model: prev[stepIndex]?.model,
        },
      }))
      try {
        const result = await stream((chunk) => {
          setBuffers((prev) => {
            const cur = prev[stepIndex] ?? { text: '', done: false, tokens: 0, inputTokens: 0, costUSD: 0, ttftMs: 0, durationMs: 0, streaming: true }
            return { ...prev, [stepIndex]: { ...cur, text: cur.text + chunk } }
          })
        })
        setBuffers((prev) => ({
          ...prev,
          [stepIndex]: {
            text: result.content,
            done: true,
            tokens: result.turn_output_tokens,
            inputTokens: result.turn_input_tokens,
            costUSD: result.turn_cost_usd,
            ttftMs: result.turn_ttft_ms,
            durationMs: result.turn_duration_ms,
            streaming: false,
            interjection: result.interjection ?? note,
            model: result.model,
          },
        }))
        setTurnResults((prev) => ({ ...prev, [stepIndex]: result }))
        onTurnCompleted(stepIndex, result)
        setTotalIn(result.total_input_tokens)
        setTotalOut(result.total_output_tokens)
        if (advancePlayhead) setIdx(stepIndex + 1)
        // A generation just spent from the budget; pull the new balance.
        void refreshQuota()
      } catch (e) {
        // 402 means the budget is exhausted. Show a plain message naming the
        // limit (the pill already knows it) and refresh so the balance reads $0.
        let msg = e instanceof Error ? e.message : 'stream failed'
        if (e instanceof ApiError && e.status === 402) {
          msg = 'Your purse is empty.'
          void refreshQuota()
        }
        setBuffers((prev) => {
          // A failed retry leaves the stored answer intact server-side, so put
          // the prior generation back on screen and hang the error off it,
          // rather than stranding the step blank.
          if (!advancePlayhead && prior?.done) {
            return { ...prev, [stepIndex]: { ...prior, error: msg } }
          }
          return {
            ...prev,
            [stepIndex]: {
              text: prev[stepIndex]?.text ?? '',
              done: false,
              tokens: 0,
              inputTokens: 0,
              costUSD: 0,
              ttftMs: 0,
              durationMs: 0,
              streaming: false,
              error: msg,
              interjection: prev[stepIndex]?.interjection,
            },
          }
        })
      } finally {
        inFlight.current = false
      }
    },
    [onTurnCompleted, refreshQuota],
  )

  const advance = useCallback(
    async (interjection?: string) => {
      if (idx >= debate.total_steps) return
      const note = interjection?.trim() || undefined
      const role = roleForLabel(debate.steps[idx].label, debate.debater_a.name, debate.debater_b.name)
      const model = role === 'a' ? roleModels.a : role === 'b' ? roleModels.b : roleModels.referee
      await runStep(idx, note, (onToken) => streamTurn(debate.id, onToken, model, note), true)
    },
    [idx, debate, roleModels, runStep],
  )

  // retry re-runs the last completed step (idx - 1) in place. The interjection
  // that steered it is preserved (the server keeps it), and the playhead stays.
  // model, when non-empty, is the Retry Model the re-roll runs on.
  const retry = useCallback(async (model: string) => {
    const stepIndex = idx - 1
    if (stepIndex < 0) return
    const stepKey = debate.steps[stepIndex]?.key
    if (!stepKey) return
    const note = buffersRef.current[stepIndex]?.interjection
    await runStep(stepIndex, note, (onToken) => streamRetry(debate.id, stepKey, onToken, model), false)
  }, [idx, debate.id, debate.steps, runStep])

  useEffect(() => {
    if (!auto) return
    if (idx === 0) return
    const prevStep = idx - 1
    const buf = buffers[prevStep]
    if (!buf?.done) return
    if (lastCompletedRef.current === prevStep) return
    lastCompletedRef.current = prevStep
    if (idx >= debate.total_steps) return
    const timer = setTimeout(() => {
      advance()
    }, AUTO_ADVANCE_DELAY_MS)
    return () => clearTimeout(timer)
  }, [idx, auto, buffers, advance, debate.total_steps])

  return { idx, buffers, turnResults, totalIn, totalOut, advance, retry }
}
