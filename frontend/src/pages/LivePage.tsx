import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  DEFAULT_MODEL,
  MODELS,
  fetchNarrationsCached,
  formatCostUSD,
  formatDuration,
  parseStructured,
  type CriticFinding,
  type DebateModels,
  type DebateSession,
  type JudgeVerdict,
  type TurnResult,
} from '../api'
import type { TurnBuffer } from '../hooks/useDebatePlayback'
import { enrich } from '../philosopher-meta'
import { roleForLabel, trimmedLabel } from '../step-role'
import { CriticCurtain, JudgeCurtain, ModeratorCurtain } from '../components/Curtains'
import { LiveChrome } from '../components/LiveChrome'
import { NextBar } from '../components/NextBar'
import { NoctuaIcon } from '../components/NoctuaIcon'
import { StepRetry } from '../components/StepRetry'
import { TurnBlock } from '../components/TurnBlock'
import { TutorPanel } from '../components/TutorPanel'
import { makeAudioCoordinator } from '../components/PlayButton'
import { useDebatePlayback } from '../hooks/useDebatePlayback'
import { useZen } from '../ZenContext'
import type { Route } from '../components/Masthead'

interface Props {
  debate: DebateSession
  navigate: (r: Route) => void
  initialTurns: Record<number, TurnResult>
  onTurnCompleted: (stepIndex: number, turn: TurnResult) => void
}

// curtainMeta builds the footer line shown under a moderator/critic/judge
// curtain: the step label, then this turn's output tokens, cost, and latency
// once the generation has completed (all zero/absent while streaming). Latency
// shows "first → total" when the step streamed (moderator), else just the total
// (critic/judge arrive as a single payload). Zen mode drops the whole tail,
// leaving just the label.
function curtainMeta(label: string, buf: TurnBuffer, zen: boolean): string {
  let meta = `◆ ${label}`
  if (zen) return meta
  if (buf.tokens) meta += ` · ${buf.tokens.toLocaleString()} tok`
  if (buf.costUSD > 0) meta += ` · ${formatCostUSD(buf.costUSD)}`
  if (buf.durationMs > 0) {
    meta +=
      buf.ttftMs > 0 && buf.ttftMs < buf.durationMs
        ? ` · ${formatDuration(buf.ttftMs)} → ${formatDuration(buf.durationMs)}`
        : ` · ${formatDuration(buf.durationMs)}`
  }
  return meta
}

export function LivePage({ debate, navigate, initialTurns, onTurnCompleted }: Props) {
  const { zen } = useZen()
  const coordinatorRef = useRef(makeAudioCoordinator())
  const a = useMemo(() => enrich(debate.debater_a), [debate.debater_a])
  const b = useMemo(() => enrich(debate.debater_b), [debate.debater_b])
  const nameA = a.name
  const nameB = b.name

  const [auto, setAuto] = useState(false)
  // The client's per-role model choice for this debate. Read from the stash
  // written at creation; on a cleared/cross-device load, reconstruct each role's
  // model from its latest completed step, defaulting roles not yet seen.
  const [roleModels] = useState<DebateModels>(() => {
    try {
      const raw = localStorage.getItem(`logos_models_${debate.id}`)
      if (raw) {
        const m = JSON.parse(raw)
        if (m && typeof m.a === 'string' && typeof m.b === 'string' && typeof m.referee === 'string') {
          return m as DebateModels
        }
      }
    } catch { /* noop */ }
    const derived: DebateModels = { a: DEFAULT_MODEL, b: DEFAULT_MODEL, referee: DEFAULT_MODEL }
    for (let i = 0; i < debate.total_steps; i++) {
      const m = initialTurns[i]?.model
      if (!m) continue
      const role = roleForLabel(debate.steps[i].label, nameA, nameB)
      if (role === 'a') derived.a = m
      else if (role === 'b') derived.b = m
      else derived.referee = m
    }
    return derived
  })
  const [selPopover, setSelPopover] = useState<{
    x: number
    y: number
    text: string
    turnKey: string
  } | null>(null)
  const [tutorTarget, setTutorTarget] = useState<{ selection: string; turnKey: string } | null>(
    null,
  )
  const [tutorPanelOpen, setTutorPanelOpen] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)

  const { idx, buffers, turnResults, totalIn, totalOut, advance, retry } =
    useDebatePlayback(debate, initialTurns, onTurnCompleted, auto, roleModels)

  // Per-step narration cache status, fetched once on load: true means an instant
  // replay, false/absent means playing will synthesize (a charge applies). It's a
  // snapshot — a freshly generated turn has never been narrated, so it defaults to
  // uncached, and we optimistically flip a step to cached once it synthesizes.
  const [narrationCached, setNarrationCached] = useState<Record<string, boolean>>({})
  useEffect(() => {
    let cancelled = false
    void fetchNarrationsCached(debate.id).then((m) => {
      if (!cancelled) setNarrationCached(m)
    })
    return () => { cancelled = true }
  }, [debate.id])
  const markSynthesized = useCallback((stepKey: string) => {
    setNarrationCached((prev) => (prev[stepKey] ? prev : { ...prev, [stepKey]: true }))
  }, [])

  // A generation is in flight when any buffer is streaming. The retry control is
  // disabled while busy (the backend allows one generation at a time).
  const busy = useMemo(() => Object.values(buffers).some((b) => b.streaming), [buffers])

  // The owner's pending interjection for the upcoming turn, carried into advance
  // and cleared after.
  const [draft, setDraft] = useState('')
  const handleAdvance = useCallback(() => {
    advance(draft)
    setDraft('')
  }, [advance, draft])

  const activeStep = debate.steps[idx]
  const activeRole = activeStep ? roleForLabel(activeStep.label, nameA, nameB) : null

  const tokensA = useMemo(
    () =>
      Object.entries(turnResults)
        .filter(([i]) => roleForLabel(debate.steps[+i].label, nameA, nameB) === 'a')
        .reduce((n, [, t]) => n + (t.turn_output_tokens ?? 0), 0),
    [turnResults, debate.steps, nameA, nameB],
  )
  const tokensB = useMemo(
    () =>
      Object.entries(turnResults)
        .filter(([i]) => roleForLabel(debate.steps[+i].label, nameA, nameB) === 'b')
        .reduce((n, [, t]) => n + (t.turn_output_tokens ?? 0), 0),
    [turnResults, debate.steps, nameA, nameB],
  )

  // Keyboard: → / Space advance, P auto toggle.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
      if (e.code === 'ArrowRight' || e.code === 'Space') {
        e.preventDefault()
        handleAdvance()
      } else if (e.code === 'KeyP') {
        setAuto((v) => !v)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [handleAdvance])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [idx])

  // Detect text selection inside a completed philosopher turn body (marked with
  // data-turn-key). Show a floating "Explore" button anchored to the selection
  // that opens the tutor panel when clicked.
  useEffect(() => {
    const onMouseUp = (e: MouseEvent) => {
      const target = e.target as HTMLElement | null
      if (target?.closest('[data-tutor-popover]')) return
      const sel = window.getSelection()
      if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
        setSelPopover(null)
        return
      }
      const text = sel.toString().trim()
      if (!text) {
        setSelPopover(null)
        return
      }
      const anchor = sel.anchorNode
      const el = (anchor instanceof HTMLElement ? anchor : anchor?.parentElement) ?? null
      const container = el?.closest('[data-turn-key]') as HTMLElement | null
      if (!container) {
        setSelPopover(null)
        return
      }
      const turnKey = container.dataset.turnKey
      if (!turnKey) {
        setSelPopover(null)
        return
      }
      const rect = sel.getRangeAt(0).getBoundingClientRect()
      setSelPopover({ x: rect.left + rect.width / 2, y: rect.top, text, turnKey })
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

  // Which steps should be visible? All steps with a buffer (started or finished).
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
        modelA={roleModels.a}
        modelB={roleModels.b}
      />

      <section className="container-narrow transcript">
        {visibleIndexes.length === 0 && (
          <div
            style={{ textAlign: 'center', padding: '80px 0 40px', color: 'var(--ink-soft)' }}
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
          let body: JSX.Element
          if (role === 'moderator') {
            body = (
              <ModeratorCurtain
                text={buf.text}
                streaming={buf.streaming && !buf.done}
                error={buf.error}
                meta={curtainMeta(step.label, buf, zen)}
                debateId={debate.id}
                stepKey={step.key}
              />
            )
          } else if (role === 'critic') {
            body = (
              <CriticCurtain
                streaming={buf.streaming && !buf.done}
                error={buf.error}
                meta={curtainMeta(step.label, buf, zen)}
                finding={buf.done ? parseStructured<CriticFinding>(buf.text) : null}
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
                meta={curtainMeta(step.label, buf, zen)}
                verdict={buf.done ? parseStructured<JudgeVerdict>(buf.text) : null}
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
                fullText={buf.fullText}
                streaming={buf.streaming && !buf.done}
                tokens={buf.tokens}
                inputTokens={buf.inputTokens}
                costUSD={buf.costUSD}
                ttftMs={buf.ttftMs}
                durationMs={buf.durationMs}
                active={i === idx - 1 || (i === idx && buf.streaming)}
                error={buf.error}
                debateId={debate.id}
                stepKey={step.key}
                coordinator={coordinatorRef.current}
                interjection={buf.interjection}
                narrationCached={narrationCached[step.key]}
                onSynthesized={() => markSynthesized(step.key)}
              />
            )
          }
          return (
            <Fragment key={key}>
              {body}
              {i === idx - 1 && (
                <StepRetry
                  onRetry={retry}
                  busy={busy}
                  retrying={!!buf.streaming}
                  role={role}
                  models={MODELS}
                  currentModel={buf.model}
                />
              )}
            </Fragment>
          )
        })}

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
          model={roleModels.referee}
        />
      )}

      <NextBar
        debate={debate}
        a={a}
        b={b}
        idx={idx}
        auto={auto}
        setAuto={setAuto}
        onAdvance={handleAdvance}
        streaming={!!buffers[idx]?.streaming && !buffers[idx]?.done}
        interjection={draft}
        onInterjectionChange={setDraft}
      />
    </main>
  )
}
