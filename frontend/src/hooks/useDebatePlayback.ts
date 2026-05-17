import { useCallback, useEffect, useRef, useState } from 'react'
import { streamTurn, type DebateSession, type TurnResult } from '../api'

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
}

export interface DebatePlayback {
  idx: number
  buffers: Record<number, TurnBuffer>
  turnResults: Record<number, TurnResult>
  totalIn: number
  totalOut: number
  advance: () => Promise<void>
}

export function useDebatePlayback(
  debate: DebateSession,
  initialTurns: Record<number, TurnResult>,
  onTurnCompleted: (i: number, t: TurnResult) => void,
  auto: boolean,
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
        streaming: false,
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
      setBuffers((prev) => ({
        ...prev,
        [stepIndex]: {
          text: result.content,
          done: true,
          tokens: result.turn_output_tokens,
          streaming: false,
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

  return { idx, buffers, turnResults, totalIn, totalOut, advance }
}
