import { useCallback, useEffect, useId, useRef, useState, type ChangeEvent } from 'react'
import { streamTTS } from '../api'

// Single-active-audio coordinator: only one PlayButton may play at a time.
// Scoped to a LivePage instance via useRef so state doesn't bleed between
// navigations or Strict Mode double-mounts.
export interface AudioCoordinator {
  take: (owner: string, pause: () => void) => void
  release: (owner: string) => void
}

export function makeAudioCoordinator(): AudioCoordinator {
  let activeOwner: string | null = null
  let activePause: (() => void) | null = null
  return {
    take(owner, pause) {
      if (activeOwner && activeOwner !== owner && activePause) activePause()
      activeOwner = owner
      activePause = pause
    },
    release(owner) {
      if (activeOwner === owner) {
        activeOwner = null
        activePause = null
      }
    },
  }
}

// PlayButton streams TTS for a step via Web Audio and plays chunks as they
// arrive (first audio in ~1-2s). On completion, caches the full PCM so
// replay and seek are instant.
interface PlayButtonProps {
  debateId: string
  stepKey: string
  text: string
  coordinator: AudioCoordinator
  disabled?: boolean
  onTime?: (pos: number) => void
  onDuration?: (dur: number) => void
  // Fires once when streaming ends with the true total duration. Consumers
  // that need an accurate duration (e.g. sentence timing) should prefer this
  // over onDuration, which reports partial values during streaming.
  onFinalDuration?: (dur: number) => void
  onStateChange?: (playing: boolean) => void
}

function fmtTime(s: number): string {
  if (!Number.isFinite(s) || s < 0) return '0:00'
  const m = Math.floor(s / 60)
  const r = Math.floor(s % 60)
  return `${m}:${r.toString().padStart(2, '0')}`
}

export function PlayButton({
  debateId,
  stepKey,
  text,
  coordinator,
  disabled,
  onTime,
  onDuration,
  onFinalDuration,
  onStateChange,
}: PlayButtonProps) {
  const [state, setState] = useState<
    'idle' | 'loading' | 'streaming' | 'playing' | 'paused' | 'error'
  >('idle')
  const [errorMsg, setErrorMsg] = useState<string | null>(null)
  const [pos, setPos] = useState(0)
  const [dur, setDur] = useState(0)
  const ownerId = useId()
  const stateRef = useRef(state)
  useEffect(() => {
    stateRef.current = state
  }, [state])
  const onStateChangeRef = useRef(onStateChange)
  useEffect(() => {
    onStateChangeRef.current = onStateChange
  }, [onStateChange])

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
      const AC =
        window.AudioContext ??
        (window as unknown as { webkitAudioContext: typeof AudioContext })
          .webkitAudioContext
      ctxRef.current = new AC({ sampleRate })
    }
    return ctxRef.current
  }, [])

  const clearTicker = useCallback(() => {
    if (tickerRef.current !== null) {
      window.clearInterval(tickerRef.current)
      tickerRef.current = null
    }
  }, [])
  const clearEndTimer = useCallback(() => {
    if (endTimerRef.current !== null) {
      window.clearTimeout(endTimerRef.current)
      endTimerRef.current = null
    }
  }, [])

  const stopAll = useCallback(() => {
    for (const s of sourcesRef.current) {
      try {
        s.onended = null
        s.stop()
      } catch {
        /* noop */
      }
    }
    sourcesRef.current = []
    clearTicker()
    clearEndTimer()
  }, [clearTicker, clearEndTimer])

  // pauseSelf is invoked by the single-active-audio coordinator when another
  // PlayButton starts playing. It mirrors the user-initiated pause branch in
  // onToggle, but reads state through refs so its identity can stay stable
  // across renders.
  const pauseSelf = useCallback(() => {
    const s = stateRef.current
    if (s !== 'playing' && s !== 'streaming') return
    const ctx = ctxRef.current
    if (!ctx) return
    pauseOffsetRef.current = ctx.currentTime - playStartRef.current
    ctx.suspend().catch(() => {
      /* noop */
    })
    clearTicker()
    setState('paused')
    onStateChangeRef.current?.(false)
  }, [clearTicker])

  useEffect(() => {
    return () => {
      coordinator.release(ownerId)
      stopAll()
      if (ctxRef.current) {
        try {
          ctxRef.current.close()
        } catch {
          /* noop */
        }
        ctxRef.current = null
      }
    }
  }, [stopAll, ownerId])

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
          coordinator.release(ownerId)
        }
      }
    }, 80)
  }, [clearTicker, coordinator, onStateChange, onTime, stopAll, ownerId])

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
      try {
        await ctx.suspend()
      } catch {
        /* noop */
      }
      clearTicker()
      setState('paused')
      onStateChange?.(false)
      coordinator.release(ownerId)
      return
    }

    // Resume from pause.
    if (state === 'paused') {
      const ctx = ctxRef.current
      if (!ctx) return
      try {
        await ctx.resume()
      } catch {
        /* noop */
      }
      startTicker()
      setState(doneRef.current ? 'playing' : 'streaming')
      onStateChange?.(true)
      coordinator.take(ownerId, pauseSelf)
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
      coordinator.take(ownerId, pauseSelf)
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
      coordinator.take(ownerId, pauseSelf)
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
      for (const c of pcmChunksRef.current) {
        unified.set(c, off)
        off += c.length
      }
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
      coordinator.release(ownerId)
    }
  }, [
    state,
    debateId,
    stepKey,
    text,
    coordinator,
    ensureCtx,
    scheduleChunk,
    startTicker,
    stopAll,
    clearTicker,
    onDuration,
    onFinalDuration,
    onStateChange,
    onTime,
    ownerId,
    pauseSelf,
  ])

  const onSeek = useCallback(
    (e: ChangeEvent<HTMLInputElement>) => {
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
      if (ctx.state === 'suspended') {
        ctx.resume().catch(() => {})
      }
      setPos(t)
      onTime?.(t)
      startTicker()
      setState('playing')
      onStateChange?.(true)
    },
    [onStateChange, onTime, startTicker, stopAll],
  )

  const icon =
    state === 'loading' ? (
      <svg
        className="spin"
        width="12"
        height="12"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="3"
        strokeLinecap="round"
        style={{ verticalAlign: '-1px' }}
      >
        <path d="M21 12a9 9 0 1 1-6.219-8.56" />
      </svg>
    ) : state === 'playing' || state === 'streaming' ? (
      '⏸'
    ) : (
      '▶'
    )
  const title =
    state === 'error'
      ? (errorMsg ?? 'error')
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
          <span
            className="t-mono"
            style={{ fontSize: 11, color: 'var(--ink-soft)', minWidth: 64 }}
          >
            {fmtTime(pos)} / {fmtTime(maxSeek)}
          </span>
        </>
      )}
    </span>
  )
}
