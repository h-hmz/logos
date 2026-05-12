import { type CriticFinding, type JudgeVerdict } from '../api'
import { type EnrichedPhilosopher } from '../philosopher-meta'
import { InlineMd } from './InlineMd'

interface CurtainProps {
  text: string
  streaming: boolean
  meta: string
  error?: string
  debateId?: string
  stepKey?: string
}

export function ModeratorCurtain({ text, streaming, meta, error }: CurtainProps) {
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
      {error && (
        <div className="live-error" style={{ marginTop: 12 }}>
          {error}
        </div>
      )}
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

export function CriticCurtain({ streaming, meta, error, finding, a, b }: CriticCurtainProps) {
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
      {error && (
        <div className="live-error" style={{ marginTop: 12 }}>
          {error}
        </div>
      )}
      <div className="curtain-meta">{meta}</div>
    </div>
  )
}

function CriticBody({
  finding,
  a,
  b,
}: {
  finding: CriticFinding
  a: EnrichedPhilosopher
  b: EnrichedPhilosopher
}) {
  return (
    <div className="critic-body">
      {finding.intro && (
        <p className="t-lead">
          <InlineMd text={finding.intro} />
        </p>
      )}
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
          <div className="v">
            <InlineMd text={side.strongest} />
          </div>
        </div>
      )}
      {side.weakness && (
        <div className="critic-row">
          <div className="k">Weakness</div>
          <div className="v">
            <InlineMd text={side.weakness} />
          </div>
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

export function JudgeCurtain({
  streaming,
  done,
  meta,
  error,
  verdict,
  a,
  b,
  onSeeVerdict,
}: JudgeCurtainProps) {
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
              <span className="t-sc">Verdict</span> <b>{winner}</b>
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
      {error && (
        <div className="live-error" style={{ marginTop: 12 }}>
          {error}
        </div>
      )}
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

