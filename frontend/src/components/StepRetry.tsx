import { useEffect, useRef, useState } from 'react'
import { DEFAULT_MODEL, modelLabel } from '../api'
import { type Role } from '../step-role'

interface StepRetryProps {
  // onRetry re-rolls the step; model is "" to keep the step's current model
  // (sticky) or an allowlisted id to run the re-roll on a different model.
  onRetry: (model: string) => void
  // busy is true while any generation is in flight; the backend permits only one
  // at a time, so the control is disabled (and the menu closed) until it clears.
  busy: boolean
  // retrying is true while this step's own re-roll is streaming, switching the
  // label to a progress state.
  retrying: boolean
  // role positions the control under the step's content: debater turns mirror
  // the turn's two-column grid so it lands at the right edge of the speaker's
  // column (flipped for B), while referee curtains span full width.
  role: Role
  // models is the allowlist (model ids) backing the menu; currentModel is the
  // model the step currently holds, offered first as the "keep current" choice.
  models: readonly string[]
  currentModel?: string
}

// StepRetry is the affordance beneath the debate's last completed step: a quiet
// link that, when clicked, opens a small menu of models. Choosing one re-rolls
// the step on it immediately — the current model (sticky, value "") first, then
// the allowlisted alternatives. Only the last step is retryable (enforced by the
// backend), so the page renders this for that step alone.
export function StepRetry({ onRetry, busy, retrying, role, models, currentModel }: StepRetryProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)
  const debater = role === 'a' || role === 'b'

  // Dismiss the menu on an outside click or Escape.
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  // A generation starting (busy) closes the menu so it never lingers over a
  // re-rolling step.
  useEffect(() => {
    if (busy) setOpen(false)
  }, [busy])

  const choose = (model: string) => {
    setOpen(false)
    onRetry(model)
  }

  const controls = (
    <span className="retry-controls" ref={ref}>
      <button
        type="button"
        className="retry-link"
        onClick={() => setOpen((v) => !v)}
        disabled={busy}
        aria-haspopup="menu"
        aria-expanded={open}
        title="Discard this answer and generate a new one"
      >
        {retrying ? 'retrying…' : '↻ retry'}
      </button>
      {open && (
        <div className="retry-menu" role="menu">
          <div className="retry-menu-head">Re-roll on</div>
          <button
            type="button"
            className="retry-menu-item"
            role="menuitem"
            onClick={() => choose(currentModel ?? DEFAULT_MODEL)}
          >
            {currentModel ? modelLabel(currentModel) : modelLabel(DEFAULT_MODEL)}
            <span className="cur"> · current</span>
          </button>
          {models
            .filter((m) => m !== currentModel)
            .map((m) => (
              <button
                key={m}
                type="button"
                className="retry-menu-item"
                role="menuitem"
                onClick={() => choose(m)}
              >
                {modelLabel(m)}
              </button>
            ))}
        </div>
      )}
    </span>
  )

  if (!debater) {
    return <div className="step-retry full">{controls}</div>
  }
  return (
    <div className={`step-retry turn-aligned ${role}`}>
      <span className="retry-cell">{controls}</span>
    </div>
  )
}
