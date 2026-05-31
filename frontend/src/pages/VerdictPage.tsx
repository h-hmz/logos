import { useEffect, useMemo, useState } from 'react'
import { Bust } from '../components/Bust'
import { InlineMd } from '../components/InlineMd'
import {
  parseStructured,
  scorecardTotals,
  verdictWinner,
  type DebateSession,
  type JudgeVerdict,
  type TurnResult,
} from '../api'
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
    if (verdict.turning_point?.quote) out.push({ id: 'turning', label: 'I · Turning point' })
    if (verdict.scorecard?.length) out.push({ id: 'scorecard', label: 'II · Scorecard' })
    if (verdict.synthesis) out.push({ id: 'synthesis', label: 'III · Synthesis' })
    if (verdict.open?.length) out.push({ id: 'open', label: 'IV · Open' })
    return out
  }, [verdict])

  const totals = useMemo(() => scorecardTotals(verdict), [verdict])

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

  const winner = verdictWinner(verdict)
  const winnerName =
    winner === 'A' ? a.name : winner === 'B' ? b.name : winner === 'draw' ? 'A draw' : null

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

          {verdict?.turning_point?.quote && (
            <div id="turning" className="section">
              <div className="hair">I</div>
              <h3>Turning point</h3>
              <blockquote className="turning-quote">
                <InlineMd text={verdict.turning_point.quote} />
                <cite className="turning-who">
                  — {verdict.turning_point.who === 'B' ? b.name : a.name}
                </cite>
              </blockquote>
              {verdict.turning_point.why && (
                <p className="verdict-article" style={{ marginTop: 12 }}>
                  <InlineMd text={verdict.turning_point.why} />
                </p>
              )}
            </div>
          )}

          {verdict?.scorecard && verdict.scorecard.length > 0 && (
            <div id="scorecard" className="section">
              <div className="hair">II</div>
              <h3>Scorecard</h3>
              <div className="scorecard">
                <div className="sc-legend">
                  <span className="sc-tag a"><i />{a.name}</span>
                  <span className="sc-tag b"><i />{b.name}</span>
                </div>
                {verdict.scorecard.map((row, i) => {
                  const sa = row.score_a ?? 0
                  const sb = row.score_b ?? 0
                  return (
                    <div className="sc-row" key={i}>
                      <div className="sc-main">
                        <div className="sc-crit">{row.criterion}</div>
                        <div className="sc-bars">
                          <div className={`sc-side a${sa >= sb ? ' lead' : ''}`}>
                            <span className="sc-ini">{a.initials}</span>
                            <span className="sc-track"><i style={{ width: `${sa * 20}%` }} /></span>
                            <span className="sc-val">{sa}</span>
                          </div>
                          <div className={`sc-side b${sb >= sa ? ' lead' : ''}`}>
                            <span className="sc-ini">{b.initials}</span>
                            <span className="sc-track"><i style={{ width: `${sb * 20}%` }} /></span>
                            <span className="sc-val">{sb}</span>
                          </div>
                        </div>
                      </div>
                      {row.note && <p className="sc-note"><InlineMd text={row.note} /></p>}
                    </div>
                  )
                })}
                <div className="sc-total">
                  <div className={`sc-total-side a${totals.a > totals.b ? ' win' : ''}`}>
                    <span className="sc-total-val">{totals.a}</span>
                    <span className="sc-ini">{a.initials}</span>
                  </div>
                  <div className="sc-total-rule">
                    {winnerName && (
                      <span className="sc-ruling">
                        {winner === 'draw' ? 'A draw' : `${winnerName} takes it`}
                      </span>
                    )}
                  </div>
                  <div className={`sc-total-side b${totals.b > totals.a ? ' win' : ''}`}>
                    <span className="sc-ini">{b.initials}</span>
                    <span className="sc-total-val">{totals.b}</span>
                  </div>
                </div>
              </div>
              {verdict.ranking?.note && (
                <p className="verdict-article" style={{ marginTop: 18 }}>
                  <InlineMd text={verdict.ranking.note} />
                </p>
              )}
            </div>
          )}

          {verdict?.synthesis && (
            <div id="synthesis" className="section">
              <div className="hair">III</div>
              <h3>Synthesis</h3>
              <p className="verdict-article drop"><InlineMd text={verdict.synthesis} /></p>
            </div>
          )}

          {verdict?.open && verdict.open.length > 0 && (
            <div id="open" className="section">
              <div className="hair">IV</div>
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
