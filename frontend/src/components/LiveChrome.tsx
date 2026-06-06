import { modelLabel, type DebateSession } from '../api'
import { type EnrichedPhilosopher } from '../philosopher-meta'
import { roleForLabel, type Role } from '../step-role'
import { useZen } from '../ZenContext'
import { Bust } from './Bust'
import { TokenPill } from './TokenPill'

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

export function LiveChrome({ debate, a, b, idx, tokensA, tokensB, totalOut, activeRole }: ChromeProps) {
  const { zen } = useZen()
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
          {!zen && (
            <div
              style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 6 }}
            >
              <div style={{ display: 'flex', gap: 8 }}>
                <TokenPill n={tokensA} label={a.initials} live={activeRole === 'a'} />
                <TokenPill n={tokensB} label={b.initials} live={activeRole === 'b'} />
                <TokenPill n={totalOut} label="Σ" />
              </div>
            </div>
          )}
        </div>

        <div className="fighters">
          <div className="fighter">
            <Bust who={a} size="xs" showSchool={false} />
            <div className="meta">
              <div className="name">{a.name}</div>
              <div className="line">
                <span className="school">{a.school}</span>
              </div>
              {a.model && <div className="model">{modelLabel(a.model)}</div>}
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
              {b.model && <div className="model">{modelLabel(b.model)}</div>}
            </div>
          </div>
          <div className="spacer" />
          <div className="steps">
            {debate.steps.map((s, i) => {
              const role = roleForLabel(s.label, a.name, b.name)
              const done = i < idx
              const now = i === idx
              const extra = role === 'critic' ? 'critic' : role === 'judge' ? 'judge' : ''
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
