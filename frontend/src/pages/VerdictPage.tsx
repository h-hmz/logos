import { useEffect, useMemo, useState } from 'react'
import { Bust } from '../components/Bust'
import { InlineMd } from '../components/InlineMd'
import { parseStructured, type DebateSession, type JudgeVerdict, type TurnResult } from '../api'
import { enrich } from '../philosopher-meta'
import { roleForLabel } from '../step-role'
import type { Route } from '../components/Masthead'

interface Props {
  debate: DebateSession
  turns: Record<number, TurnResult>
  navigate: (r: Route) => void
}

interface SectionDef {
  id: string
  label: string
}

export function VerdictPage({ debate, turns, navigate }: Props) {
  const a = useMemo(() => enrich(debate.debater_a), [debate.debater_a])
  const b = useMemo(() => enrich(debate.debater_b), [debate.debater_b])

  const judgeIdx = debate.steps.findIndex(
    (s) => roleForLabel(s.label, a.name, b.name) === 'judge'
  )
  const judgeTurn = judgeIdx >= 0 ? turns[judgeIdx] : undefined
  const verdict = judgeTurn ? parseStructured<JudgeVerdict>(judgeTurn.content) : null

  const tally = useMemo(() => {
    let tokA = 0, tokB = 0, tokCritic = 0, tokJudge = 0, totalIn = 0, totalOut = 0
    let rounds = 0
    for (const [i, t] of Object.entries(turns)) {
      const role = roleForLabel(debate.steps[+i].label, a.name, b.name)
      const out = t.turn_output_tokens ?? 0
      if (role === 'a') { tokA += out; rounds++ }
      else if (role === 'b') tokB += out
      else if (role === 'critic') tokCritic += out
      else if (role === 'judge') tokJudge += out
      totalIn = Math.max(totalIn, t.total_input_tokens ?? 0)
      totalOut = Math.max(totalOut, t.total_output_tokens ?? 0)
    }
    return { tokA, tokB, tokCritic, tokJudge, totalIn, totalOut, rounds: Math.ceil(rounds) }
  }, [turns, debate.steps, a.name, b.name])

  const sum = tally.totalIn + tally.totalOut

  const sections: SectionDef[] = useMemo(() => {
    if (!verdict) return []
    const out: SectionDef[] = []
    if (verdict.summary_a || verdict.summary_b) out.push({ id: 'positions', label: 'I · Positions' })
    if (verdict.clashes?.length) out.push({ id: 'clashes', label: 'II · Clashes' })
    if (verdict.shifts?.length) out.push({ id: 'shifts', label: 'III · Shifts' })
    if (verdict.ranking) out.push({ id: 'ranking', label: 'IV · Ranking' })
    if (verdict.synthesis) out.push({ id: 'synthesis', label: 'V · Synthesis' })
    if (typeof verdict.confidence === 'number') out.push({ id: 'confidence', label: 'VI · Confidence' })
    if (verdict.open?.length) out.push({ id: 'open', label: 'VII · Open' })
    return out
  }, [verdict])

  const [active, setActive] = useState<string>('')
  useEffect(() => {
    if (sections.length === 0) return
    const handler = () => {
      let cur = sections[0].id
      for (const s of sections) {
        const el = document.getElementById(s.id)
        if (!el) continue
        if (el.getBoundingClientRect().top - 120 <= 0) cur = s.id
      }
      setActive(cur)
    }
    handler()
    window.addEventListener('scroll', handler, { passive: true })
    return () => window.removeEventListener('scroll', handler)
  }, [sections])

  const winnerName =
    verdict?.ranking?.winner === 'A'
      ? a.name
      : verdict?.ranking?.winner === 'B'
      ? b.name
      : verdict?.ranking?.winner === 'draw'
      ? 'A draw'
      : null

  return (
    <main>
      <section className="verdict-hero">
        <div className="container">
          <div className="t-eyebrow">The judge&apos;s verdict</div>
          <div className="seal">§</div>
          <div className="topic">&ldquo;{debate.topic}&rdquo;</div>
          <div className="fighters-big">
            <div style={{ textAlign: 'center' }}>
              <div style={{ margin: '0 auto', width: 80 }}>
                <Bust who={a} size="sm" showSchool={false} />
              </div>
              <div
                className="t-sc"
                style={{ marginTop: 8, fontSize: 13, letterSpacing: '0.2em' }}
              >
                {a.name.toUpperCase()}
              </div>
            </div>
            <div className="v">vs.</div>
            <div style={{ textAlign: 'center' }}>
              <div style={{ margin: '0 auto', width: 80 }}>
                <Bust who={b} size="sm" showSchool={false} />
              </div>
              <div
                className="t-sc"
                style={{ marginTop: 8, fontSize: 13, letterSpacing: '0.2em' }}
              >
                {b.name.toUpperCase()}
              </div>
            </div>
          </div>
          {winnerName && (
            <div className="verdict-ruling" style={{ marginTop: 18 }}>
              <span className="t-sc" style={{ letterSpacing: '0.24em', fontSize: 12 }}>
                Ruling
              </span>{' '}
              <span style={{ fontFamily: 'var(--serif-d)', fontStyle: 'italic', fontSize: 28 }}>
                {winnerName}
              </span>
            </div>
          )}
          <div className="t-mono" style={{ marginTop: 18 }}>
            {debate.steps.length} steps · {sum.toLocaleString()} total tokens
          </div>
        </div>
      </section>

      <section
        className="container verdict-grid"
        style={{ gridTemplateColumns: '1fr 260px' }}
      >
        <article>
          {!verdict && judgeTurn && (
            <div className="section">
              <div className="hair">I</div>
              <h3>The verdict</h3>
              <div className="verdict-article"><InlineMd text={judgeTurn.content} /></div>
            </div>
          )}

          {!judgeTurn && (
            <div className="section">
              <div className="hair">—</div>
              <h3>The verdict</h3>
              <p style={{ fontStyle: 'italic', color: 'var(--ink-soft)' }}>
                The judge has not yet rendered a verdict.{' '}
                <a
                  href={`#/d/${debate.id}`}
                  onClick={(e) => {
                    e.preventDefault()
                    navigate({ page: 'live', id: debate.id })
                  }}
                  style={{ color: 'var(--accent-ink)' }}
                >
                  Return to the live bout →
                </a>
              </p>
            </div>
          )}

          {verdict && (verdict.summary_a || verdict.summary_b) && (
            <div id="positions" className="section">
              <div className="hair">I</div>
              <h3>Positions</h3>
              <div className="positions-grid">
                {verdict.summary_a && (
                  <PositionCard
                    who={a.name}
                    initials={a.initials}
                    name={verdict.summary_a.name}
                    thesis={verdict.summary_a.thesis}
                  />
                )}
                {verdict.summary_b && (
                  <PositionCard
                    who={b.name}
                    initials={b.initials}
                    name={verdict.summary_b.name}
                    thesis={verdict.summary_b.thesis}
                  />
                )}
              </div>
            </div>
          )}

          {verdict?.clashes && verdict.clashes.length > 0 && (
            <div id="clashes" className="section">
              <div className="hair">II</div>
              <h3>Clashes</h3>
              <ol className="clash-list">
                {verdict.clashes.map((c, i) => (
                  <li key={i}>
                    {c.title && <div className="clash-title"><InlineMd text={c.title} /></div>}
                    {c.description && <div className="clash-desc"><InlineMd text={c.description} /></div>}
                  </li>
                ))}
              </ol>
            </div>
          )}

          {verdict?.shifts && verdict.shifts.length > 0 && (
            <div id="shifts" className="section">
              <div className="hair">III</div>
              <h3>Shifts</h3>
              <ul className="shift-list">
                {verdict.shifts.map((s, i) => (
                  <li key={i}>
                    <span className="shift-who">
                      {s.who === 'A'
                        ? a.name
                        : s.who === 'B'
                        ? b.name
                        : s.who === 'both'
                        ? 'Both'
                        : 'Neither'}
                    </span>
                    {s.description && <span className="shift-desc"> — <InlineMd text={s.description} /></span>}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {verdict?.ranking && (
            <div id="ranking" className="section">
              <div className="hair">IV</div>
              <h3>Ranking</h3>
              <div className="ranking-line">
                <span className="t-sc">Winner</span>
                <span className="ranking-winner">
                  {verdict.ranking.winner === 'A'
                    ? a.name
                    : verdict.ranking.winner === 'B'
                    ? b.name
                    : 'Draw'}
                </span>
              </div>
              {verdict.ranking.note && (
                <p className="verdict-article" style={{ marginTop: 10 }}>
                  <InlineMd text={verdict.ranking.note} />
                </p>
              )}
            </div>
          )}

          {verdict?.synthesis && (
            <div id="synthesis" className="section">
              <div className="hair">V</div>
              <h3>Synthesis</h3>
              <p className="verdict-article drop"><InlineMd text={verdict.synthesis} /></p>
            </div>
          )}

          {verdict && typeof verdict.confidence === 'number' && (
            <div id="confidence" className="section">
              <div className="hair">VI</div>
              <h3>Confidence</h3>
              <div className="confidence-meter">
                <div className="confidence-bar">
                  <div
                    className="confidence-fill"
                    style={{ width: `${Math.max(0, Math.min(10, verdict.confidence)) * 10}%` }}
                  />
                </div>
                <div className="confidence-num">
                  {verdict.confidence}
                  <span className="confidence-den">/10</span>
                </div>
              </div>
              {verdict.confidence_note && (
                <p style={{ marginTop: 10, fontStyle: 'italic', color: 'var(--ink-2)' }}>
                  <InlineMd text={verdict.confidence_note} />
                </p>
              )}
            </div>
          )}

          {verdict?.open && verdict.open.length > 0 && (
            <div id="open" className="section">
              <div className="hair">VII</div>
              <h3>Open questions</h3>
              <ul className="open-list">
                {verdict.open.map((q, i) => (
                  <li key={i}><InlineMd text={q} /></li>
                ))}
              </ul>
            </div>
          )}
        </article>

        <aside>
          {sections.length > 0 && (
            <div className="aside-card toc">
              <h4>Contents</h4>
              <ul>
                {sections.map((s) => (
                  <li key={s.id} className={active === s.id ? 'on' : ''}>
                    <a
                      href={`#${s.id}`}
                      onClick={(e) => {
                        e.preventDefault()
                        document.getElementById(s.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
                      }}
                    >
                      {s.label}
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          )}

          <div className="aside-card">
            <h4>Tale of the tape</h4>
            <div className="stat-list">
              <div className="row"><span>Steps</span><span>{debate.steps.length}</span></div>
              <div className="row">
                <span>{a.initials} · tokens</span>
                <span>{tally.tokA.toLocaleString()}</span>
              </div>
              <div className="row">
                <span>{b.initials} · tokens</span>
                <span>{tally.tokB.toLocaleString()}</span>
              </div>
              <div className="row">
                <span>Critic · tokens</span>
                <span>{tally.tokCritic.toLocaleString()}</span>
              </div>
              <div className="row">
                <span>Judge · tokens</span>
                <span>{tally.tokJudge.toLocaleString()}</span>
              </div>
              <div className="row">
                <span>Input Σ</span>
                <span>{tally.totalIn.toLocaleString()}</span>
              </div>
              <div className="row">
                <span>Output Σ</span>
                <span>{tally.totalOut.toLocaleString()}</span>
              </div>
              <div className="row">
                <span>Σ total</span>
                <span style={{ color: 'var(--accent)' }}>{sum.toLocaleString()}</span>
              </div>
            </div>
          </div>

          <div className="aside-card">
            <h4>Share the verdict</h4>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <button
                type="button"
                className="btn ghost small"
                onClick={() => {
                  navigator.clipboard?.writeText(window.location.href).catch(() => {})
                }}
              >
                Copy permalink
              </button>
              <button
                type="button"
                className="btn accent small"
                onClick={() => navigate({ page: 'stage' })}
              >
                Stage a rematch →
              </button>
            </div>
          </div>
        </aside>
      </section>
    </main>
  )
}

function PositionCard({
  who,
  initials,
  name,
  thesis,
}: {
  who: string
  initials: string
  name?: string
  thesis?: string
}) {
  return (
    <div className="position-card">
      <div className="position-head">
        <span className="t-sc">{initials}</span>
        <span className="name">{name ?? who}</span>
      </div>
      {thesis && <p className="position-thesis"><InlineMd text={thesis} /></p>}
    </div>
  )
}
