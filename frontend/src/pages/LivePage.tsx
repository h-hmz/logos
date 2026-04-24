import { Fragment, useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type JSX } from 'react'
import { Bust } from '../components/Bust'
import { InlineMd } from '../components/InlineMd'
import { TokenPill } from '../components/TokenPill'
import { TutorPanel } from '../components/TutorPanel'
import { NoctuaIcon } from '../components/NoctuaIcon'
import {
  interject,
  streamTTS,
  streamTurn,
  type CriticFinding,
  type DebateSession,
  type Interjection,
  type JudgeVerdict,
  type TurnResult,
} from '../api'
import { enrich, type EnrichedPhilosopher } from '../philosopher-meta'
import { roleForLabel, trimmedLabel, type Role } from '../step-role'
import type { Route } from '../components/Masthead'

interface Props {
  debate: DebateSession
  navigate: (r: Route) => void
  initialTurns: Record<number, TurnResult>
  onTurnCompleted: (stepIndex: number, turn: TurnResult) => void
}

interface TurnBuffer {
  text: string
  done: boolean
  tokens: number
  streaming: boolean
  error?: string
  structured?: CriticFinding | JudgeVerdict | null
}

const STORAGE_KEY = (id: string) => `logos_step_${id}`
const INTERJECTIONS_KEY = (id: string) => `logos_interjections_${id}`
const INTERJECTION_MAX_LEN = 800

// splitSentences breaks text into sentence-like chunks, keeping terminators.
// Good enough for prose debate turns; doesn't try to handle "Dr.", "e.g.", etc.
// It also ensures closing quotes, brackets, and markdown asterisks/underscores
// are kept glued to the end of the sentence they terminate.
function splitSentences(text: string): string[] {
  const parts = text.match(/[^.!?\n]+[.!?]+["”')\]*_]*[\s]*|[^.!?\n]+$/g)
  if (!parts) return text.trim() ? [text] : []
  return parts.map((p) => p.trim()).filter(Boolean)
}

// Conservative speaking rate (chars/sec) used while the stream is still
// arriving and the true total duration isn't known. Deliberately on the slow
// side so the highlighter lags audio rather than racing ahead of it — catching
// up feels natural, overshooting looks broken.
const TTS_FALLBACK_CPS = 11

// sentenceStarts returns the cumulative start time (seconds) of each sentence,
// weighted proportionally by character count. Pass `finalDuration` only once
// the full audio length is known (stream complete); while streaming, omit it
// and we'll estimate from a conservative chars/sec fallback instead of
// crushing the whole text into the partial duration received so far.
function sentenceStarts(sentences: string[], finalDuration?: number): number[] {
  const weights = sentences.map((s) => Math.max(s.length, 1))
  const total = weights.reduce((a, b) => a + b, 0)
  const effectiveDur = finalDuration && finalDuration > 0
    ? finalDuration
    : total / TTS_FALLBACK_CPS
  const starts: number[] = []
  let acc = 0
  for (const w of weights) {
    starts.push((acc / total) * effectiveDur)
    acc += w
  }
  return starts
}

// activeSentenceIndex returns the index of the sentence whose start is the
// latest one <= pos, or -1 if pos is before the first sentence or duration=0.
function activeSentenceIndex(starts: number[], pos: number, playing: boolean): number {
  if (!playing || starts.length === 0) return -1
  let idx = -1
  for (let i = 0; i < starts.length; i++) {
    if (starts[i] <= pos) idx = i
    else break
  }
  return idx
}

// SpokenText renders text with optional per-sentence highlighting. When
// activeIndex is -1 no sentence is marked.
interface SpokenTextProps {
  text: string
  activeIndex: number
  asParagraphs?: boolean
}

function SpokenText({ text, activeIndex, asParagraphs }: SpokenTextProps) {
  const paragraphs = asParagraphs ? text.split(/\n\n+/).filter(Boolean) : [text]
  let runningIdx = 0
  return (
    <>
      {paragraphs.map((p, pi) => {
        const sentences = splitSentences(p)
        const startIdx = runningIdx
        runningIdx += sentences.length
        const rendered = sentences.map((s, si) => {
          const globalIdx = startIdx + si
          const active = globalIdx === activeIndex
          return (
            <span
              key={si}
              className={active ? 'reading-active' : undefined}
              style={
                active
                  ? {
                    background: 'color-mix(in srgb, var(--accent) 22%, transparent)',
                    boxShadow: '0 0 0 2px color-mix(in srgb, var(--accent) 22%, transparent)',
                    borderRadius: 2,
                    transition: 'background 120ms linear',
                  }
                  : undefined
              }
            >
              <InlineMd text={s} />
              {si < sentences.length - 1 ? ' ' : ''}
            </span>
          )
        })
        return asParagraphs ? (
          <p key={pi} className={pi === 0 ? 'drop' : ''}>
            {rendered}
          </p>
        ) : (
          <span key={pi}>{rendered}</span>
        )
      })}
    </>
  )
}

// PlayButton streams TTS for a step via Web Audio and plays chunks as they
// arrive (first audio in ~1-2s). On completion, caches the full PCM so
// replay and seek are instant.
interface PlayButtonProps {
  debateId: string
  stepKey: string
  text: string
  disabled?: boolean
  onTime?: (pos: number) => void
  onDuration?: (dur: number) => void
  // Fires once when streaming ends with the true total duration. Consumers
  // that need an accurate duration (e.g. sentence timing) should prefer this
  // over onDuration, which reports partial values during streaming.
  onFinalDuration?: (dur: number) => void
  onStateChange?: (playing: boolean) => void
}

function PlayButton({ debateId, stepKey, text, disabled, onTime, onDuration, onFinalDuration, onStateChange }: PlayButtonProps) {
  const [state, setState] = useState<'idle' | 'loading' | 'streaming' | 'playing' | 'paused' | 'error'>('idle')
  const [errorMsg, setErrorMsg] = useState<string | null>(null)
  const [pos, setPos] = useState(0)
  const [dur, setDur] = useState(0)

  const ctxRef = useRef<AudioContext | null>(null)
  const sampleRateRef = useRef(24000)
  const pcmChunksRef = useRef<Float32Array[]>([])
  const totalSamplesRef = useRef(0)
  const sourcesRef = useRef<AudioBufferSourceNode[]>([])
  const fullBufferRef = useRef<AudioBuffer | null>(null)
  // AudioContext-time anchors: playback started at playStart, clock at pause stashed in pauseOffset.
  const playStartRef = useRef(0)
  const nextScheduleRef = useRef(0)
  const pauseOffsetRef = useRef(0)
  const doneRef = useRef(false)
  const tickerRef = useRef<number | null>(null)
  const endTimerRef = useRef<number | null>(null)

  const ensureCtx = useCallback((sampleRate: number): AudioContext => {
    if (!ctxRef.current) {
      const AC = (window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext)
      ctxRef.current = new AC({ sampleRate })
    }
    return ctxRef.current
  }, [])

  const clearTicker = useCallback(() => {
    if (tickerRef.current !== null) { window.clearInterval(tickerRef.current); tickerRef.current = null }
  }, [])
  const clearEndTimer = useCallback(() => {
    if (endTimerRef.current !== null) { window.clearTimeout(endTimerRef.current); endTimerRef.current = null }
  }, [])

  const stopAll = useCallback(() => {
    for (const s of sourcesRef.current) { try { s.onended = null; s.stop() } catch { /* noop */ } }
    sourcesRef.current = []
    clearTicker()
    clearEndTimer()
  }, [clearTicker, clearEndTimer])

  useEffect(() => {
    return () => {
      stopAll()
      if (ctxRef.current) { try { ctxRef.current.close() } catch { /* noop */ } ctxRef.current = null }
    }
  }, [stopAll])

  const startTicker = useCallback(() => {
    clearTicker()
    tickerRef.current = window.setInterval(() => {
      const ctx = ctxRef.current
      if (!ctx) return
      const t = Math.max(0, ctx.currentTime - playStartRef.current)
      const cap = doneRef.current ? totalSamplesRef.current / sampleRateRef.current : t
      const clamped = Math.min(t, cap)
      setPos(clamped)
      onTime?.(clamped)
      if (doneRef.current) {
        const totalDur = totalSamplesRef.current / sampleRateRef.current
        if (t >= totalDur) {
          stopAll()
          setPos(totalDur)
          onTime?.(totalDur)
          setState('idle')
          onStateChange?.(false)
          pauseOffsetRef.current = 0
        }
      }
    }, 80)
  }, [clearTicker, onStateChange, onTime, stopAll])

  const scheduleChunk = useCallback((f32: Float32Array) => {
    const ctx = ctxRef.current
    if (!ctx) return
    const buf = ctx.createBuffer(1, f32.length, sampleRateRef.current)
    // Channel view uses a plain ArrayBuffer; the f32 we receive may be
    // backed by ArrayBufferLike, so copy via the buffer's own channel data.
    buf.getChannelData(0).set(f32)
    const src = ctx.createBufferSource()
    src.buffer = buf
    src.connect(ctx.destination)
    const when = Math.max(ctx.currentTime, nextScheduleRef.current)
    if (nextScheduleRef.current === 0) {
      playStartRef.current = when
    }
    src.start(when)
    nextScheduleRef.current = when + buf.duration
    sourcesRef.current.push(src)
  }, [])

  const onToggle = useCallback(async () => {
    // Pause while streaming or playing.
    if (state === 'playing' || state === 'streaming') {
      const ctx = ctxRef.current
      if (!ctx) return
      pauseOffsetRef.current = ctx.currentTime - playStartRef.current
      try { await ctx.suspend() } catch { /* noop */ }
      clearTicker()
      setState('paused')
      onStateChange?.(false)
      return
    }

    // Resume from pause.
    if (state === 'paused') {
      const ctx = ctxRef.current
      if (!ctx) return
      try { await ctx.resume() } catch { /* noop */ }
      startTicker()
      setState(doneRef.current ? 'playing' : 'streaming')
      onStateChange?.(true)
      return
    }

    // Replay from a completed stream: rebuild a single source from the cached
    // full buffer so subsequent plays are instant and seekable.
    if (state === 'idle' && doneRef.current && fullBufferRef.current) {
      const ctx = ctxRef.current!
      stopAll()
      nextScheduleRef.current = 0
      const src = ctx.createBufferSource()
      src.buffer = fullBufferRef.current
      src.connect(ctx.destination)
      const when = ctx.currentTime
      playStartRef.current = when
      nextScheduleRef.current = when + fullBufferRef.current.duration
      src.start(when)
      sourcesRef.current = [src]
      setPos(0)
      onTime?.(0)
      startTicker()
      setState('playing')
      onStateChange?.(true)
      return
    }

    // First click: stream from the server.
    try {
      setState('loading')
      setErrorMsg(null)
      doneRef.current = false
      pcmChunksRef.current = []
      totalSamplesRef.current = 0
      nextScheduleRef.current = 0
      playStartRef.current = 0
      pauseOffsetRef.current = 0
      fullBufferRef.current = null

      const { sampleRate, chunks } = await streamTTS(debateId, stepKey, text)
      sampleRateRef.current = sampleRate
      const ctx = ensureCtx(sampleRate)
      if (ctx.state === 'suspended') await ctx.resume()

      setState('streaming')
      onStateChange?.(true)
      startTicker()

      for await (const chunk of chunks) {
        pcmChunksRef.current.push(chunk)
        totalSamplesRef.current += chunk.length
        setDur(totalSamplesRef.current / sampleRate)
        onDuration?.(totalSamplesRef.current / sampleRate)
        scheduleChunk(chunk)
      }

      // Stream complete: build the unified buffer for replay/seek.
      doneRef.current = true
      onFinalDuration?.(totalSamplesRef.current / sampleRate)
      const totalLen = totalSamplesRef.current
      const unified = new Float32Array(totalLen)
      let off = 0
      for (const c of pcmChunksRef.current) { unified.set(c, off); off += c.length }
      const full = ctx.createBuffer(1, totalLen, sampleRate)
      full.getChannelData(0).set(unified)
      fullBufferRef.current = full
      // If we're still playing when stream ends, we'll flip to idle via the
      // ticker once audio actually finishes.
      setState((s) => (s === 'streaming' ? 'playing' : s))
    } catch (e) {
      stopAll()
      setState('error')
      setErrorMsg(e instanceof Error ? e.message : 'TTS failed')
      onStateChange?.(false)
    }
  }, [state, debateId, stepKey, text, ensureCtx, scheduleChunk, startTicker, stopAll, clearTicker, onDuration, onFinalDuration, onStateChange, onTime])

  const onSeek = useCallback((e: ChangeEvent<HTMLInputElement>) => {
    // Seek is only supported after streaming completes.
    if (!doneRef.current || !fullBufferRef.current) return
    const ctx = ctxRef.current
    if (!ctx) return
    const t = parseFloat(e.target.value)
    stopAll()
    nextScheduleRef.current = 0
    const src = ctx.createBufferSource()
    src.buffer = fullBufferRef.current
    src.connect(ctx.destination)
    const when = ctx.currentTime
    playStartRef.current = when - t
    nextScheduleRef.current = when + (fullBufferRef.current.duration - t)
    src.start(when, t)
    sourcesRef.current = [src]
    if (ctx.state === 'suspended') { ctx.resume().catch(() => { }) }
    setPos(t)
    onTime?.(t)
    startTicker()
    setState('playing')
    onStateChange?.(true)
  }, [onStateChange, onTime, startTicker, stopAll])

  const icon = state === 'loading' ? (
    <svg className="spin" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" style={{ verticalAlign: '-1px' }}>
      <path d="M21 12a9 9 0 1 1-6.219-8.56" />
    </svg>
  ) : (state === 'playing' || state === 'streaming') ? '⏸' : '▶'
  const title = state === 'error'
    ? errorMsg ?? 'error'
    : state === 'playing' || state === 'streaming'
      ? 'Pause'
      : state === 'paused'
        ? 'Resume'
        : 'Read aloud'
  const hasAudio = totalSamplesRef.current > 0
  const maxSeek = dur > 0 ? dur : 0
  const seekDisabled = !doneRef.current

  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <button
        type="button"
        className="btn ghost small"
        onClick={onToggle}
        disabled={disabled || state === 'loading' || !text}
        title={title}
        aria-label={title}
        style={{ padding: '2px 8px', fontSize: 12, lineHeight: 1.4 }}
      >
        {icon} {state === 'loading' ? '' : '🔊'}
      </button>
      {hasAudio && maxSeek > 0 && (
        <>
          <input
            type="range"
            min={0}
            max={maxSeek}
            step={0.1}
            value={Math.min(pos, maxSeek)}
            onChange={onSeek}
            disabled={seekDisabled}
            aria-label="Seek"
            style={{ width: 120, verticalAlign: 'middle' }}
          />
          <span className="t-mono" style={{ fontSize: 11, color: 'var(--ink-soft)', minWidth: 64 }}>
            {fmtTime(pos)} / {fmtTime(maxSeek)}
          </span>
        </>
      )}
    </span>
  )
}

function fmtTime(s: number): string {
  if (!Number.isFinite(s) || s < 0) return '0:00'
  const m = Math.floor(s / 60)
  const r = Math.floor(s % 60)
  return `${m}:${r.toString().padStart(2, '0')}`
}

// An upcoming step accepts an interjection iff it's a philosopher round turn.
// Round step keys look like "{name}_r{n}"; critic responses end in "_critic_response".
const roundKeyRe = /_r\d+$/
function stepAcceptsInterjection(stepKey: string): boolean {
  return roundKeyRe.test(stepKey) && !stepKey.endsWith('_critic_response')
}

export function LivePage({ debate, navigate, initialTurns, onTurnCompleted }: Props) {
  const a = useMemo(() => enrich(debate.debater_a), [debate.debater_a])
  const b = useMemo(() => enrich(debate.debater_b), [debate.debater_b])
  const nameA = a.name
  const nameB = b.name

  // idx is the number of completed steps; the "now-streaming" step is at idx.
  const [idx, setIdx] = useState<number>(() => {
    try {
      const saved = parseInt(localStorage.getItem(STORAGE_KEY(debate.id)) || '', 10)
      return Number.isFinite(saved) ? Math.max(0, Math.min(saved, debate.total_steps)) : 0
    } catch { return 0 }
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
  const [interjections, setInterjections] = useState<Record<number, Interjection>>(() => {
    try {
      const raw = localStorage.getItem(INTERJECTIONS_KEY(debate.id))
      if (!raw) return {}
      const parsed = JSON.parse(raw) as Interjection[]
      const map: Record<number, Interjection> = {}
      for (const it of parsed) map[it.before_step_index] = it
      return map
    } catch { return {} }
  })
  const [composerOpen, setComposerOpen] = useState(false)
  const [selPopover, setSelPopover] = useState<{
    x: number
    y: number
    text: string
    turnKey: string
  } | null>(null)
  const [tutorTarget, setTutorTarget] = useState<{ selection: string; turnKey: string } | null>(null)
  const [tutorPanelOpen, setTutorPanelOpen] = useState(false)
  const [auto, setAuto] = useState(true)
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
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    try { localStorage.setItem(STORAGE_KEY(debate.id), String(idx)) } catch { /* noop */ }
  }, [idx, debate.id])

  useEffect(() => {
    try {
      localStorage.setItem(
        INTERJECTIONS_KEY(debate.id),
        JSON.stringify(Object.values(interjections)),
      )
    } catch { /* noop */ }
  }, [interjections, debate.id])

  const submitInterjection = useCallback(async (text: string) => {
    const stepIndex = idx
    const result = await interject(debate.id, text)
    setInterjections((prev) => ({ ...prev, [stepIndex]: result }))
    setAuto(false)
    setComposerOpen(false)
  }, [idx, debate.id])

  const activeStep = debate.steps[idx]
  const activeRole: Role | null = activeStep
    ? roleForLabel(activeStep.label, nameA, nameB)
    : null

  const tokensA = useMemo(
    () => Object.entries(turnResults)
      .filter(([i]) => roleForLabel(debate.steps[+i].label, nameA, nameB) === 'a')
      .reduce((n, [, t]) => n + (t.turn_output_tokens ?? 0), 0),
    [turnResults, debate.steps, nameA, nameB]
  )
  const tokensB = useMemo(
    () => Object.entries(turnResults)
      .filter(([i]) => roleForLabel(debate.steps[+i].label, nameA, nameB) === 'b')
      .reduce((n, [, t]) => n + (t.turn_output_tokens ?? 0), 0),
    [turnResults, debate.steps, nameA, nameB]
  )

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

  // Auto-advance: when a turn completes, wait 1.4s then kick off the next.
  const lastCompletedRef = useRef(-1)
  useEffect(() => {
    if (!auto) return
    if (idx === 0) return
    const prevStep = idx - 1
    const buf = buffers[prevStep]
    if (!buf?.done) return
    if (lastCompletedRef.current === prevStep) return
    lastCompletedRef.current = prevStep
    if (idx >= debate.total_steps) return
    const timer = setTimeout(() => { advance() }, 1400)
    return () => clearTimeout(timer)
  }, [idx, auto, buffers, advance, debate.total_steps])

  // Keyboard: → / Space advance, P auto toggle.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
      if (e.code === 'ArrowRight' || e.code === 'Space') {
        e.preventDefault(); advance()
      } else if (e.code === 'KeyP') {
        setAuto((v) => !v)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [advance])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [idx])

  // Detect text selection inside a completed philosopher turn body (marked with
  // data-turn-key). Show a floating "Explore" button anchored to the selection
  // that opens the tutor panel when clicked.
  useEffect(() => {
    const onMouseUp = (e: MouseEvent) => {
      // Ignore clicks that originated inside the popover button itself.
      const target = e.target as HTMLElement | null
      if (target?.closest('[data-tutor-popover]')) return
      const sel = window.getSelection()
      if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
        setSelPopover(null)
        return
      }
      const text = sel.toString().trim()
      if (!text) { setSelPopover(null); return }
      const anchor = sel.anchorNode
      const el = (anchor instanceof HTMLElement ? anchor : anchor?.parentElement) ?? null
      const container = el?.closest('[data-turn-key]') as HTMLElement | null
      if (!container) { setSelPopover(null); return }
      const turnKey = container.dataset.turnKey
      if (!turnKey) { setSelPopover(null); return }
      const rect = sel.getRangeAt(0).getBoundingClientRect()
      setSelPopover({
        x: rect.left + rect.width / 2,
        y: rect.top,
        text,
        turnKey,
      })
    }
    const onSelectionChange = () => {
      const sel = window.getSelection()
      if (!sel || sel.isCollapsed) setSelPopover(null)
    }
    document.addEventListener('mouseup', onMouseUp)
    document.addEventListener('selectionchange', onSelectionChange)
    return () => {
      document.removeEventListener('mouseup', onMouseUp)
      document.removeEventListener('selectionchange', onSelectionChange)
    }
  }, [])

  // Which steps should be visible? All steps with a buffer (started or finished),
  // rendered in order.
  const visibleIndexes = useMemo(() => {
    const indexes: number[] = []
    for (let i = 0; i < debate.total_steps; i++) {
      if (buffers[i]) indexes.push(i)
    }
    return indexes
  }, [buffers, debate.total_steps])

  return (
    <main className={tutorTarget && tutorPanelOpen ? 'noctua-open' : undefined}>
      <LiveChrome
        debate={debate}
        a={a}
        b={b}
        idx={idx}
        tokensA={tokensA}
        tokensB={tokensB}
        totalOut={totalOut}
        totalIn={totalIn}
        activeRole={activeRole}
      />

      <section className="container-narrow transcript">
        {visibleIndexes.length === 0 && (
          <div
            style={{
              textAlign: 'center',
              padding: '80px 0 40px',
              color: 'var(--ink-soft)',
            }}
          >
            <div
              style={{
                fontFamily: 'var(--serif-d)',
                fontSize: 28,
                fontStyle: 'italic',
                marginBottom: 10,
              }}
            >
              The agora is quiet.
            </div>
            <div className="t-small">
              Press <b>Next →</b> or the spacebar to summon the moderator.
            </div>
          </div>
        )}

        {visibleIndexes.map((i) => {
          const step = debate.steps[i]
          const buf = buffers[i]
          const role = roleForLabel(step.label, nameA, nameB)
          const key = `step-${i}`
          const interjection = interjections[i]
          const audience = interjection ? (
            <AudienceCurtain key={`interj-${i}`} text={interjection.text} />
          ) : null
          let body: JSX.Element
          if (role === 'moderator') {
            body = (
              <ModeratorCurtain
                text={buf.text}
                streaming={buf.streaming && !buf.done}
                error={buf.error}
                meta={`◆ ${step.label}${buf.tokens ? ` · ${buf.tokens} tok` : ''}`}
                debateId={debate.id}
                stepKey={step.key}
              />
            )
          } else if (role === 'critic') {
            body = (
              <CriticCurtain
                streaming={buf.streaming && !buf.done}
                error={buf.error}
                meta={`◆ ${step.label}${buf.tokens ? ` · ${buf.tokens} tok` : ''}`}
                finding={(buf.structured as CriticFinding | undefined) ?? null}
                a={a}
                b={b}
              />
            )
          } else if (role === 'judge') {
            body = (
              <JudgeCurtain
                streaming={buf.streaming && !buf.done}
                done={buf.done}
                error={buf.error}
                meta={`◆ ${step.label}${buf.tokens ? ` · ${buf.tokens} tok` : ''}`}
                verdict={(buf.structured as JudgeVerdict | undefined) ?? null}
                a={a}
                b={b}
                onSeeVerdict={() => navigate({ page: 'verdict', id: debate.id })}
              />
            )
          } else {
            const who = role === 'a' ? a : b
            body = (
              <TurnBlock
                who={who}
                side={role}
                label={trimmedLabel(step.label, nameA, nameB)}
                text={buf.text}
                streaming={buf.streaming && !buf.done}
                tokens={buf.tokens}
                active={i === idx - 1 || (i === idx && buf.streaming)}
                error={buf.error}
                debateId={debate.id}
                stepKey={step.key}
              />
            )
          }
          return (
            <Fragment key={key}>
              {audience}
              {body}
            </Fragment>
          )
        })}
        {/* Pending interjection not yet followed by a step (upcoming step hasn't started). */}
        {interjections[idx] && !buffers[idx] && (
          <AudienceCurtain text={interjections[idx].text} />
        )}

        {idx >= debate.total_steps && (
          <div
            style={{
              textAlign: 'center',
              marginTop: 40,
              fontFamily: 'var(--serif-d)',
              fontSize: 22,
              fontStyle: 'italic',
              color: 'var(--ink-soft)',
            }}
          >
            — the bout concludes —
          </div>
        )}
        <div ref={bottomRef} />
      </section>

      {composerOpen && (
        <InterjectionComposer
          onSubmit={submitInterjection}
          onCancel={() => setComposerOpen(false)}
        />
      )}

      {selPopover && (
        <button
          type="button"
          data-tutor-popover
          className="tutor-explore-btn"
          style={{
            position: 'fixed',
            left: selPopover.x,
            top: selPopover.y,
            transform: 'translate(-50%, calc(-100% - 8px))',
          }}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => {
            setTutorTarget({ selection: selPopover.text, turnKey: selPopover.turnKey })
            setTutorPanelOpen(true)
            setSelPopover(null)
            window.getSelection()?.removeAllRanges()
          }}
        >
          <NoctuaIcon size={16} />
          <span>Ask Noctua</span>
        </button>
      )}

      {tutorTarget && !tutorPanelOpen && (
        <button
          type="button"
          className="tutor-resume-btn"
          onClick={() => setTutorPanelOpen(true)}
          title="Reopen Noctua with the latest exchange"
        >
          <NoctuaIcon size={18} />
          <span>Resume with Noctua</span>
        </button>
      )}

      {tutorTarget && (
        <TutorPanel
          key={`${tutorTarget.turnKey}|${tutorTarget.selection}`}
          debateId={debate.id}
          turnKey={tutorTarget.turnKey}
          selection={tutorTarget.selection}
          visible={tutorPanelOpen}
          onClose={() => setTutorPanelOpen(false)}
        />
      )}

      <NextBar
        debate={debate}
        a={a}
        b={b}
        idx={idx}
        auto={auto}
        setAuto={setAuto}
        onAdvance={advance}
        streaming={!!buffers[idx]?.streaming && !buffers[idx]?.done}
        canInterject={
          idx > 0 &&
          idx < debate.total_steps &&
          !buffers[idx]?.streaming &&
          !interjections[idx] &&
          !composerOpen &&
          stepAcceptsInterjection(debate.steps[idx]?.key ?? '')
        }
        onInterject={() => setComposerOpen(true)}
      />
    </main>
  )
}

// ── Chrome ────────────────────────────────────────────────────────────────────

interface ChromeProps {
  debate: DebateSession
  a: EnrichedPhilosopher
  b: EnrichedPhilosopher
  idx: number
  tokensA: number
  tokensB: number
  totalOut: number
  totalIn: number
  activeRole: Role | null
}

function LiveChrome({ debate, a, b, idx, tokensA, tokensB, totalOut, activeRole }: ChromeProps) {
  const total = debate.total_steps
  return (
    <div className="live-chrome">
      <div className="container-wide" style={{ padding: 0 }}>
        <div className="row1">
          <div>
            <div className="crumbs">
              Live bout ·{' '}
              {new Date().toLocaleDateString('en-GB', { day: 'numeric', month: 'long' })}
            </div>
            <div className="topic">&ldquo;{debate.topic}&rdquo;</div>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 6 }}>
            <div style={{ display: 'flex', gap: 8 }}>
              <TokenPill n={tokensA} label={a.initials} live={activeRole === 'a'} />
              <TokenPill n={tokensB} label={b.initials} live={activeRole === 'b'} />
              <TokenPill n={totalOut} label="Σ" />
            </div>
          </div>
        </div>

        <div className="fighters">
          <div className="fighter">
            <Bust who={a} size="xs" showSchool={false} />
            <div className="meta">
              <div className="name">{a.name}</div>
              <div className="line">
                <span className="school">{a.school}</span>
              </div>
            </div>
          </div>
          <div className="vs-small">vs.</div>
          <div className="fighter">
            <Bust who={b} size="xs" showSchool={false} />
            <div className="meta">
              <div className="name">{b.name}</div>
              <div className="line">
                <span className="school">{b.school}</span>
              </div>
            </div>
          </div>
          <div className="spacer" />
          <div className="steps">
            {debate.steps.map((s, i) => {
              const role = roleForLabel(s.label, a.name, b.name)
              const done = i < idx
              const now = i === idx
              const extra =
                role === 'critic' ? 'critic' : role === 'judge' ? 'judge' : ''
              const cls = ['node', extra, done ? 'done' : '', now ? 'now' : '']
                .filter(Boolean)
                .join(' ')
              return (
                <span key={i} className="step">
                  <span className={cls} />
                  {i < debate.steps.length - 1 && (
                    <span className={`seg${i < idx ? ' done' : ''}`} />
                  )}
                </span>
              )
            })}
            <span className="label">
              step {Math.min(idx + 1, total)} / {total}
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}

// ── Moderator curtain ─────────────────────────────────────────────────────────

interface CurtainProps {
  text: string
  streaming: boolean
  meta: string
  error?: string
  debateId?: string
  stepKey?: string
}

function ModeratorCurtain({ text, streaming, meta, error }: CurtainProps) {
  return (
    <div className="curtain moderator">
      <div className="gavel">§</div>
      <div className="title-row">
        <div className="role">The Moderator</div>
        <h2>Welcome to the agora.</h2>
      </div>
      <div className="body-text">
        <InlineMd text={text} />
        {streaming && <span className="cursor">·</span>}
      </div>
      {error && <div className="live-error" style={{ marginTop: 12 }}>{error}</div>}
      <div className="curtain-meta">{meta}</div>
    </div>
  )
}

interface CriticCurtainProps {
  streaming: boolean
  meta: string
  error?: string
  finding: CriticFinding | null
  a: EnrichedPhilosopher
  b: EnrichedPhilosopher
}

function CriticCurtain({ streaming, meta, error, finding, a, b }: CriticCurtainProps) {
  const hasFinding = !!finding && !streaming
  return (
    <div className="curtain">
      <div className="title-row">
        <div className="role">The Critic</div>
        <h2>I beg a word.</h2>
      </div>
      {hasFinding ? (
        <CriticBody finding={finding!} a={a} b={b} />
      ) : (
        <div className="body-text" style={{ fontStyle: 'italic' }}>
          {streaming
            ? 'The critic reviews the exchange, weighing each argument for soundness and reach'
            : 'Awaiting the critic.'}
          {streaming && <span className="cursor">·</span>}
        </div>
      )}
      {error && <div className="live-error" style={{ marginTop: 12 }}>{error}</div>}
      <div className="curtain-meta">{meta}</div>
    </div>
  )
}

function CriticBody({ finding, a, b }: { finding: CriticFinding; a: EnrichedPhilosopher; b: EnrichedPhilosopher }) {
  return (
    <div className="critic-body">
      {finding.intro && <p className="t-lead"><InlineMd text={finding.intro} /></p>}
      <div className="critic-grid">
        <CriticSideCard who={a.name} initials={a.initials} side={finding.debater_a} />
        <CriticSideCard who={b.name} initials={b.initials} side={finding.debater_b} />
      </div>
      {finding.overall && (
        <p className="critic-overall" style={{ marginTop: 16, fontStyle: 'italic' }}>
          <InlineMd text={finding.overall} />
        </p>
      )}
    </div>
  )
}

function CriticSideCard({
  who,
  initials,
  side,
}: {
  who: string
  initials: string
  side: CriticFinding['debater_a']
}) {
  if (!side) return null
  return (
    <div className="critic-card">
      <div className="critic-card-head">
        <span className="t-sc">{initials}</span>
        <span className="name">{side.name ?? who}</span>
      </div>
      {side.strongest && (
        <div className="critic-row">
          <div className="k">Strongest</div>
          <div className="v"><InlineMd text={side.strongest} /></div>
        </div>
      )}
      {side.weakness && (
        <div className="critic-row">
          <div className="k">Weakness</div>
          <div className="v"><InlineMd text={side.weakness} /></div>
        </div>
      )}
      {side.fallacies && side.fallacies.length > 0 && (
        <div className="critic-row">
          <div className="k">Fallacies</div>
          <ul className="critic-fallacies">
            {side.fallacies.map((f, i) => (
              <li key={i}>
                <b>{f.name}</b>
                {f.instance ? ` — ${f.instance}` : ''}
              </li>
            ))}
          </ul>
        </div>
      )}
      {side.challenge && (
        <div className="critic-row">
          <div className="k">Challenge</div>
          <div className="v" style={{ fontStyle: 'italic' }}>
            &ldquo;<InlineMd text={side.challenge} />&rdquo;
          </div>
        </div>
      )}
    </div>
  )
}

interface JudgeCurtainProps {
  streaming: boolean
  done: boolean
  meta: string
  error?: string
  verdict: JudgeVerdict | null
  a: EnrichedPhilosopher
  b: EnrichedPhilosopher
  onSeeVerdict: () => void
}

function JudgeCurtain({ streaming, done, meta, error, verdict, a, b, onSeeVerdict }: JudgeCurtainProps) {
  const winner =
    verdict?.ranking?.winner === 'A'
      ? a.name
      : verdict?.ranking?.winner === 'B'
        ? b.name
        : verdict?.ranking?.winner === 'draw'
          ? 'Draw'
          : null
  return (
    <div className="curtain judge">
      <div className="title-row">
        <div className="role">The Judge</div>
        <h2>The matter is decided.</h2>
      </div>
      {streaming ? (
        <div className="body-text" style={{ fontStyle: 'italic' }}>
          The judge deliberates
          <span className="cursor">·</span>
        </div>
      ) : verdict ? (
        <div className="judge-preview">
          {winner && (
            <div className="judge-winner">
              <span className="t-sc">Verdict</span>{' '}
              <b>{winner}</b>
              {typeof verdict.confidence === 'number' && (
                <span className="judge-confidence"> · confidence {verdict.confidence}/10</span>
              )}
            </div>
          )}
          {verdict.synthesis && (
            <p className="t-lead" style={{ marginTop: 8 }}>
              <InlineMd text={verdict.synthesis} />
            </p>
          )}
        </div>
      ) : (
        <div className="body-text" style={{ fontStyle: 'italic' }}>
          Awaiting the judge.
        </div>
      )}
      {error && <div className="live-error" style={{ marginTop: 12 }}>{error}</div>}
      <div className="curtain-meta">{meta}</div>
      {done && (
        <div style={{ marginTop: 20 }}>
          <button
            type="button"
            className="btn"
            style={{
              background: 'var(--accent-2)',
              borderColor: 'var(--accent-2)',
              color: '#1a1612',
            }}
            onClick={onSeeVerdict}
          >
            Read the verdict →
          </button>
        </div>
      )}
    </div>
  )
}

// ── Audience curtain ──────────────────────────────────────────────────────────

function AudienceCurtain({ text }: { text: string }) {
  return (
    <div className="curtain audience">
      <div className="title-row">
        <div className="role">The Audience</div>
        <h2>A voice from the crowd.</h2>
      </div>
      <div className="body-text" style={{ fontStyle: 'italic' }}>
        <InlineMd text={text} />
      </div>
      <div className="curtain-meta">◆ Interjection</div>
    </div>
  )
}

// ── Interjection composer ─────────────────────────────────────────────────────

interface InterjectionComposerProps {
  onSubmit: (text: string) => Promise<void>
  onCancel: () => void
}

function InterjectionComposer({ onSubmit, onCancel }: InterjectionComposerProps) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => { textareaRef.current?.focus() }, [])

  const trimmed = text.trim()
  const canSubmit = !busy && trimmed.length > 0 && trimmed.length <= INTERJECTION_MAX_LEN

  const handleSubmit = async () => {
    if (!canSubmit) return
    setBusy(true)
    setError(null)
    try {
      await onSubmit(trimmed)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to submit')
      setBusy(false)
    }
  }

  return (
    <div className="interjection-composer">
      <div className="interjection-composer-inner">
        <div className="t-sc" style={{ marginBottom: 6 }}>Your interjection</div>
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
              e.preventDefault()
              handleSubmit()
            } else if (e.key === 'Escape') {
              e.preventDefault()
              onCancel()
            }
          }}
          placeholder="Raise a question or a counter-argument. The next philosopher will address it directly."
          rows={4}
          disabled={busy}
          maxLength={INTERJECTION_MAX_LEN + 50}
        />
        <div className="interjection-footer">
          <span className={`counter ${trimmed.length > INTERJECTION_MAX_LEN ? 'over' : ''}`}>
            {trimmed.length} / {INTERJECTION_MAX_LEN}
          </span>
          <div style={{ flex: 1 }} />
          <button type="button" className="btn ghost small" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="btn accent small"
            onClick={handleSubmit}
            disabled={!canSubmit}
          >
            {busy ? 'Submitting…' : 'Interject'}
          </button>
        </div>
        {error && <div className="live-error" style={{ marginTop: 8 }}>{error}</div>}
      </div>
    </div>
  )
}

// ── Philosopher turn block ────────────────────────────────────────────────────

interface TurnBlockProps {
  who: EnrichedPhilosopher
  side: 'a' | 'b'
  label: string
  text: string
  streaming: boolean
  tokens: number
  active: boolean
  error?: string
  debateId?: string
  stepKey?: string
}

function TurnBlock({ who, side, label, text, streaming, tokens, active, error, debateId, stepKey }: TurnBlockProps) {
  const selectable = !streaming && !!text && !!stepKey
  const paragraphs = text.split(/\n\n+/).filter((p) => p.length > 0)
  if (paragraphs.length === 0) paragraphs.push('')
  const [pos, setPos] = useState(0)
  const [finalDur, setFinalDur] = useState<number | undefined>(undefined)
  const [playing, setPlaying] = useState(false)
  // Flatten paragraphs to a linear sentence list to keep indices consistent
  // with what SpokenText renders internally.
  const allSentences = useMemo(() => paragraphs.flatMap(splitSentences), [text])
  const starts = useMemo(() => sentenceStarts(allSentences, finalDur), [allSentences, finalDur])
  const activeIdx = activeSentenceIndex(starts, pos, playing)
  return (
    <div className={`turn ${side}${active ? ' now' : ''}`}>
      <div className="who">
        <Bust who={who} size="sm" showSchool={false} />
        <div className="name">{who.name}</div>
        <div className="school">{who.school}</div>
        <div className="meta">{tokens ? `${tokens} tok` : ''}</div>
      </div>
      <div className="say">
        <div className="label-row">
          <span className="round">◆ {label}</span>
          <span className="stat" style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            {streaming ? `streaming · ${text.length} ch` : tokens ? `${tokens} tok` : ''}
            {!streaming && text && debateId && stepKey && (
              <PlayButton
                debateId={debateId}
                stepKey={stepKey}
                text={text}
                onTime={setPos}
                onFinalDuration={setFinalDur}
                onStateChange={setPlaying}
              />
            )}
          </span>
        </div>
        <div
          className="body"
          data-turn-key={selectable ? stepKey : undefined}
        >
          {streaming ? (
            paragraphs.map((p, i) => (
              <p key={i} className={i === 0 ? 'drop' : ''}>
                <InlineMd text={p} />
                {i === paragraphs.length - 1 && <span className="cursor">·</span>}
              </p>
            ))
          ) : (
            <SpokenText text={text} activeIndex={activeIdx} asParagraphs />
          )}
        </div>
        {error && <div className="live-error">{error}</div>}
      </div>
    </div>
  )
}

// ── Next bar ──────────────────────────────────────────────────────────────────

interface NextBarProps {
  debate: DebateSession
  a: EnrichedPhilosopher
  b: EnrichedPhilosopher
  idx: number
  auto: boolean
  setAuto: (v: boolean) => void
  onAdvance: () => void
  streaming: boolean
  canInterject: boolean
  onInterject: () => void
}

function NextBar({ debate, a, b, idx, auto, setAuto, onAdvance, streaming, canInterject, onInterject }: NextBarProps) {
  const total = debate.total_steps
  const atEnd = idx >= total
  const step = debate.steps[Math.min(idx, total - 1)]
  const role = step ? roleForLabel(step.label, a.name, b.name) : null
  const labelText = !step
    ? 'The bout is over'
    : role === 'moderator'
      ? 'The moderator introduces the bout'
      : role === 'critic'
        ? 'The critic speaks next'
        : role === 'judge'
          ? 'The judge renders a verdict'
          : role === 'a'
            ? `${a.name} — ${trimmedLabel(step.label, a.name, b.name)}`
            : `${b.name} — ${trimmedLabel(step.label, a.name, b.name)}`

  return (
    <div className="next-bar">
      <div className="next-bar-inner">
        <div className="desc">
          <span className="t-mono" style={{ marginRight: 10 }}>
            {Math.min(idx + 1, total)}/{total}
          </span>
          <b>{labelText}</b>
        </div>
        <div className="controls">
          <label className="auto-label">
            <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} />
            Auto-advance
          </label>
          <button
            type="button"
            className="btn ghost small"
            onClick={onInterject}
            disabled={!canInterject}
            title="Raise a question or counter-argument"
          >
            ⚡ Interject
          </button>
          <button
            type="button"
            className="btn accent small"
            onClick={onAdvance}
            disabled={atEnd || streaming}
          >
            {streaming ? 'Streaming…' : 'Next →'}
          </button>
        </div>
      </div>
    </div>
  )
}
