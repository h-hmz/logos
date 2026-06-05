import { type DebateSession } from '../api'
import { type EnrichedPhilosopher } from '../philosopher-meta'
import { roleForLabel, trimmedLabel } from '../step-role'

// Mirrors the backend's InterjectionMaxLen so the input caps locally.
const INTERJECTION_MAX_LEN = 1000

interface NextBarProps {
  debate: DebateSession
  a: EnrichedPhilosopher
  b: EnrichedPhilosopher
  idx: number
  auto: boolean
  setAuto: (v: boolean) => void
  onAdvance: () => void
  streaming: boolean
  interjection: string
  onInterjectionChange: (v: string) => void
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
  interjection,
  onInterjectionChange,
}: NextBarProps) {
  const total = debate.total_steps
  const atEnd = idx >= total
  const step = debate.steps[Math.min(idx, total - 1)]
  const role = step ? roleForLabel(step.label, a.name, b.name) : null
  // Interjections steer debater turns only, and only when advancing by hand.
  const nextSpeaker = role === 'a' ? a.name : role === 'b' ? b.name : null
  const canInterject = !!nextSpeaker && !auto && !atEnd
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
      {canInterject && (
        <div className="interject-row">
          <textarea
            className="interject-input"
            rows={1}
            maxLength={INTERJECTION_MAX_LEN}
            value={interjection}
            onChange={(e) => onInterjectionChange(e.target.value)}
            placeholder={`Interject — a note for ${nextSpeaker} to weigh next (optional)`}
            disabled={streaming}
          />
          {interjection.trim() && (
            <span className="interject-count t-mono">
              {interjection.trim().length}/{INTERJECTION_MAX_LEN}
            </span>
          )}
        </div>
      )}
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
