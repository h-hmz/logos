import { useCallback, useEffect, useRef, useState } from 'react'
import { streamTurn, type CriticFinding, type DebateSession, type JudgeVerdict, type TurnResult } from '../api'

const STORAGE_KEY = (id: string) => `logos_step_${id}`
const AUTO_ADVANCE_DELAY_MS = 1400

export interface TurnBuffer {
  text: string
  // fullText is set the moment streamTurn resolves with the complete content,
  // even while the paced reveal is still ticking text onto the screen. It
  // unblocks audio synthesis ahead of the visual reveal.
  fullText?: string
  done: boolean
  tokens: number
  streaming: boolean
  error?: string
  structured?: CriticFinding | JudgeVerdict | null
}

export interface DebatePlayback {
  idx: number
  buffers: Record<number, TurnBuffer>
  turnResults: Record<number, TurnResult>
  awaitingHuman: boolean
  totalIn: number
  totalOut: number
  advance: () => Promise<void>
  submitHumanTurn: (input: string) => Promise<void>
}

export function useDebatePlayback(
  debate: DebateSession,
  initialTurns: Record<number, TurnResult>,
  onTurnCompleted: (i: number, t: TurnResult) => void,
  auto: boolean,
): DebatePlayback {
  const [idx, setIdx] = useState<number>(() => {
    try {
      const saved = parseInt(localStorage.getItem(STORAGE_KEY(debate.id)) || '', 10)
      return Number.isFinite(saved) ? Math.max(0, Math.min(saved, debate.total_steps)) : 0
    } catch {
      return 0
    }
  })
  const [buffers, setBuffers] = useState<Record<number, TurnBuffer>>(() => {
    const b: Record<number, TurnBuffer> = {}
    for (const [k, t] of Object.entries(initialTurns)) {
      b[+k] = {
        text: t.content,
        done: true,
        tokens: t.turn_output_tokens ?? 0,
        streaming: false,
        structured: t.structured ?? null,
      }
    }
    return b
  })
  const [turnResults, setTurnResults] = useState<Record<number, TurnResult>>(initialTurns)
  const [awaitingHuman, setAwaitingHuman] = useState(false)
  const [autoAdvanceOnce, setAutoAdvanceOnce] = useState(false)
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

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY(debate.id), String(idx))
    } catch (e) {
      console.warn('[logos] Failed to persist step index:', e)
    }
  }, [idx, debate.id])

  const advance = useCallback(async () => {
    if (inFlight.current) return
    if (idx >= debate.total_steps) return
    const stepIndex = idx
    inFlight.current = true
    setBuffers((prev) => ({
      ...prev,
      [stepIndex]: { text: '', done: false, tokens: 0, streaming: true },
    }))
    try {
      const result = await streamTurn(debate.id, (chunk) => {
        setBuffers((prev) => {
          const cur = prev[stepIndex] ?? { text: '', done: false, tokens: 0, streaming: true }
          return { ...prev, [stepIndex]: { ...cur, text: cur.text + chunk } }
        })
      })
      if (result.awaiting_human) {
        setBuffers((prev) => ({
          ...prev,
          [stepIndex]: { text: '', done: false, tokens: 0, streaming: false },
        }))
        setAwaitingHuman(true)
        return
      }
      setBuffers((prev) => ({
        ...prev,
        [stepIndex]: {
          text: result.content,
          done: true,
          tokens: result.turn_output_tokens,
          streaming: false,
          structured: result.structured ?? null,
        },
      }))
      setTurnResults((prev) => ({ ...prev, [stepIndex]: result }))
      onTurnCompleted(stepIndex, result)
      setTotalIn(result.total_input_tokens)
      setTotalOut(result.total_output_tokens)
      setIdx(stepIndex + 1)
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'stream failed'
      setBuffers((prev) => ({
        ...prev,
        [stepIndex]: {
          text: prev[stepIndex]?.text ?? '',
          done: false,
          tokens: 0,
          streaming: false,
          error: msg,
        },
      }))
    } finally {
      inFlight.current = false
    }
  }, [idx, debate.id, debate.total_steps, onTurnCompleted])

  const submitHumanTurn = useCallback(
    async (input: string) => {
      if (inFlight.current) return
      const stepIndex = idx
      inFlight.current = true
      setAwaitingHuman(false)
      setBuffers((prev) => ({
        ...prev,
        [stepIndex]: { text: '', done: false, tokens: 0, streaming: true },
      }))
      try {
        const result = await streamTurn(debate.id, () => {}, input)
        setBuffers((prev) => ({
          ...prev,
          [stepIndex]: { text: result.content, done: true, tokens: 0, streaming: false },
        }))
        setTurnResults((prev) => ({ ...prev, [stepIndex]: result }))
        onTurnCompleted(stepIndex, result)
        setIdx(stepIndex + 1)
        setAutoAdvanceOnce(true)
      } catch (e) {
        const msg = e instanceof Error ? e.message : 'submission failed'
        setBuffers((prev) => ({
          ...prev,
          [stepIndex]: { text: '', done: false, tokens: 0, streaming: false, error: msg },
        }))
        setAwaitingHuman(true)
      } finally {
        inFlight.current = false
      }
    },
    [idx, debate.id, onTurnCompleted],
  )

  useEffect(() => {
    if (!auto) return
    if (awaitingHuman) return
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
  }, [idx, auto, awaitingHuman, buffers, advance, debate.total_steps])

  useEffect(() => {
    if (!autoAdvanceOnce) return
    setAutoAdvanceOnce(false)
    advance()
  }, [autoAdvanceOnce, advance])

  return { idx, buffers, turnResults, awaitingHuman, totalIn, totalOut, advance, submitHumanTurn }
}
