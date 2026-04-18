import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Bust } from '../components/Bust'
import { TokenPill } from '../components/TokenPill'
import {
  streamTurn,
  type CriticFinding,
  type DebateSession,
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

  const back = useCallback(() => {
    // Rewind view; buffers preserved.
    setIdx((i) => Math.max(0, i - 1))
  }, [])

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

  // Keyboard: → / Space advance, ← back, P auto toggle.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
      if (e.code === 'ArrowRight' || e.code === 'Space') {
        e.preventDefault(); advance()
      } else if (e.code === 'ArrowLeft') {
        e.preventDefault(); back()
      } else if (e.code === 'KeyP') {
        setAuto((v) => !v)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [advance, back])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [idx])

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
    <main>
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
          if (role === 'moderator') {
            return (
              <ModeratorCurtain
                key={key}
                text={buf.text}
                streaming={buf.streaming && !buf.done}
                error={buf.error}
                meta={`◆ ${step.label}${buf.tokens ? ` · ${buf.tokens} tok` : ''}`}
              />
            )
          }
          if (role === 'critic') {
            return (
              <CriticCurtain
                key={key}
                streaming={buf.streaming && !buf.done}
                error={buf.error}
                meta={`◆ ${step.label}${buf.tokens ? ` · ${buf.tokens} tok` : ''}`}
                finding={(buf.structured as CriticFinding | undefined) ?? null}
                a={a}
                b={b}
              />
            )
          }
          if (role === 'judge') {
            return (
              <JudgeCurtain
                key={key}
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
          }
          const who = role === 'a' ? a : b
          return (
            <TurnBlock
              key={key}
              who={who}
              side={role}
              label={trimmedLabel(step.label, nameA, nameB)}
              text={buf.text}
              streaming={buf.streaming && !buf.done}
              tokens={buf.tokens}
              active={i === idx - 1 || (i === idx && buf.streaming)}
              error={buf.error}
            />
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

      <NextBar
        debate={debate}
        a={a}
        b={b}
        idx={idx}
        auto={auto}
        setAuto={setAuto}
        onBack={back}
        onAdvance={advance}
        streaming={!!buffers[idx]?.streaming && !buffers[idx]?.done}
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
        {text}
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
        <div className="body-text" style={{ fontStyle: 'italic', color: 'var(--ink-soft)' }}>
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
      {finding.intro && <p className="t-lead">{finding.intro}</p>}
      <div className="critic-grid">
        <CriticSideCard who={a.name} initials={a.initials} side={finding.debater_a} />
        <CriticSideCard who={b.name} initials={b.initials} side={finding.debater_b} />
      </div>
      {finding.overall && (
        <p className="critic-overall" style={{ marginTop: 16, fontStyle: 'italic' }}>
          {finding.overall}
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
          <div className="v">{side.strongest}</div>
        </div>
      )}
      {side.weakness && (
        <div className="critic-row">
          <div className="k">Weakness</div>
          <div className="v">{side.weakness}</div>
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
            &ldquo;{side.challenge}&rdquo;
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
        <div className="body-text" style={{ fontStyle: 'italic', color: 'var(--ink-soft)' }}>
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
              {verdict.synthesis}
            </p>
          )}
        </div>
      ) : (
        <div className="body-text" style={{ fontStyle: 'italic', color: 'var(--ink-soft)' }}>
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
              color: 'var(--ink)',
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
}

function TurnBlock({ who, side, label, text, streaming, tokens, active, error }: TurnBlockProps) {
  const paragraphs = text.split(/\n\n+/).filter((p) => p.length > 0)
  if (paragraphs.length === 0) paragraphs.push('')
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
          <span className="stat">
            {streaming ? `streaming · ${text.length} ch` : tokens ? `${tokens} tok` : ''}
          </span>
        </div>
        <div className="body">
          {paragraphs.map((p, i) => (
            <p key={i} className={i === 0 ? 'drop' : ''}>
              {p}
              {streaming && i === paragraphs.length - 1 && <span className="cursor">·</span>}
            </p>
          ))}
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
  onBack: () => void
  onAdvance: () => void
  streaming: boolean
}

function NextBar({ debate, a, b, idx, auto, setAuto, onBack, onAdvance, streaming }: NextBarProps) {
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
            onClick={onBack}
            disabled={idx === 0 || streaming}
          >
            ← Back
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
