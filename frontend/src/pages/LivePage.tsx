import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  interject,
  type CriticFinding,
  type DebateSession,
  type Interjection,
  type JudgeVerdict,
  type TurnResult,
} from '../api'
import { enrich } from '../philosopher-meta'
import { roleForLabel, trimmedLabel } from '../step-role'
import { AudienceCurtain, CriticCurtain, JudgeCurtain, ModeratorCurtain } from '../components/Curtains'
import { HumanTurnComposer, InterjectionComposer } from '../components/Composers'
import { LiveChrome } from '../components/LiveChrome'
import { NextBar } from '../components/NextBar'
import { NoctuaIcon } from '../components/NoctuaIcon'
import { TurnBlock } from '../components/TurnBlock'
import { TutorPanel } from '../components/TutorPanel'
import { makeAudioCoordinator } from '../components/PlayButton'
import { useDebatePlayback } from '../hooks/useDebatePlayback'
import type { Route } from '../components/Masthead'

interface Props {
  debate: DebateSession
  navigate: (r: Route) => void
  initialTurns: Record<number, TurnResult>
  onTurnCompleted: (stepIndex: number, turn: TurnResult) => void
}

const INTERJECTIONS_KEY = (id: string) => `logos_interjections_${id}`

// An upcoming step accepts an interjection iff it's a philosopher round turn.
// Round step keys look like "{name}_r{n}"; critic responses end in "_critic_response".
const roundKeyRe = /_r\d+$/
function stepAcceptsInterjection(stepKey: string): boolean {
  return roundKeyRe.test(stepKey) && !stepKey.endsWith('_critic_response')
}

export function LivePage({ debate, navigate, initialTurns, onTurnCompleted }: Props) {
  const coordinatorRef = useRef(makeAudioCoordinator())
  const a = useMemo(() => enrich(debate.debater_a), [debate.debater_a])
  const b = useMemo(() => enrich(debate.debater_b), [debate.debater_b])
  const nameA = a.name
  const nameB = b.name

  const [auto, setAuto] = useState(false)
  const [interjections, setInterjections] = useState<Record<number, Interjection>>(() => {
    try {
      const raw = localStorage.getItem(INTERJECTIONS_KEY(debate.id))
      if (!raw) return {}
      const parsed = JSON.parse(raw) as Interjection[]
      const map: Record<number, Interjection> = {}
      for (const it of parsed) map[it.before_step_index] = it
      return map
    } catch {
      return {}
    }
  })
  const [composerOpen, setComposerOpen] = useState(false)
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

  const { idx, buffers, turnResults, awaitingHuman, totalIn, totalOut, advance, submitHumanTurn } =
    useDebatePlayback(debate, initialTurns, onTurnCompleted, auto)

  useEffect(() => {
    try {
      localStorage.setItem(
        INTERJECTIONS_KEY(debate.id),
        JSON.stringify(Object.values(interjections)),
      )
    } catch (e) {
      console.warn('[logos] Failed to persist interjections:', e)
    }
  }, [interjections, debate.id])

  const submitInterjection = useCallback(
    async (text: string) => {
      const stepIndex = idx
      const result = await interject(debate.id, text)
      setInterjections((prev) => ({ ...prev, [stepIndex]: result }))
      setAuto(false)
      setComposerOpen(false)
    },
    [idx, debate.id],
  )

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
        advance()
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
                fullText={buf.fullText}
                streaming={buf.streaming && !buf.done}
                tokens={buf.tokens}
                active={i === idx - 1 || (i === idx && buf.streaming)}
                error={buf.error}
                debateId={debate.id}
                stepKey={step.key}
                coordinator={coordinatorRef.current}
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

      {awaitingHuman && <HumanTurnComposer onSubmit={submitHumanTurn} />}

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
        awaitingHuman={awaitingHuman}
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
