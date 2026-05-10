import { type DebateSession } from '../api'
import { type EnrichedPhilosopher } from '../philosopher-meta'
import { roleForLabel, trimmedLabel } from '../step-role'

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

export function NextBar({
  debate,
  a,
  b,
  idx,
  auto,
  setAuto,
  onAdvance,
  streaming,
  canInterject,
  onInterject,
}: NextBarProps) {
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

  const nextLabel = streaming ? 'Streaming…' : 'Next →'

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
            <input
              type="checkbox"
              checked={auto}
              onChange={(e) => setAuto(e.target.checked)}
            />
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
            {nextLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
