import { useEffect, useRef, useState } from 'react'

const INTERJECTION_MAX_LEN = 800
const HUMAN_TURN_MAX_WORDS = 500

export function HumanTurnComposer({ onSubmit }: { onSubmit: (text: string) => Promise<void> }) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const ref = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    containerRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
    ref.current?.focus()
  }, [])

  const trimmed = text.trim()
  const wordCount = trimmed ? trimmed.split(/\s+/).length : 0
  const canSubmit = !busy && wordCount > 0 && wordCount <= HUMAN_TURN_MAX_WORDS

  const handleSubmit = async () => {
    if (!canSubmit) return
    setBusy(true)
    setError(null)
    try {
      await onSubmit(trimmed)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Submission failed')
      setBusy(false)
    }
  }

  return (
    <div ref={containerRef} className="interjection-composer">
      <div className="interjection-composer-inner">
        <div className="t-sc" style={{ marginBottom: 6 }}>
          Your argument
        </div>
        <textarea
          ref={ref}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
              e.preventDefault()
              handleSubmit()
            }
          }}
          placeholder="Make your case. The opponent will respond directly."
          rows={5}
          disabled={busy}
        />
        <div className="interjection-footer">
          <span className={`counter ${wordCount > HUMAN_TURN_MAX_WORDS ? 'over' : ''}`}>
            {wordCount} / {HUMAN_TURN_MAX_WORDS} words
          </span>
          <div style={{ flex: 1 }} />
          <button
            type="button"
            className="btn accent small"
            onClick={handleSubmit}
            disabled={!canSubmit}
          >
            {busy ? 'Submitting…' : 'Submit argument'}
          </button>
        </div>
        {error && (
          <div className="live-error" style={{ marginTop: 8 }}>
            {error}
          </div>
        )}
      </div>
    </div>
  )
}

interface InterjectionComposerProps {
  onSubmit: (text: string) => Promise<void>
  onCancel: () => void
}

export function InterjectionComposer({ onSubmit, onCancel }: InterjectionComposerProps) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    textareaRef.current?.focus()
  }, [])

  const trimmed = text.trim()
  const canSubmit = !busy && trimmed.length > 0 && trimmed.length <= INTERJECTION_MAX_LEN

  const handleSubmit = async () => {
    if (!canSubmit) return
    setBusy(true)
    setError(null)
    try {
      await onSubmit(trimmed)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to submit')
      setBusy(false)
    }
  }

  return (
    <div className="interjection-composer">
      <div className="interjection-composer-inner">
        <div className="t-sc" style={{ marginBottom: 6 }}>
          Your interjection
        </div>
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
              e.preventDefault()
              handleSubmit()
            } else if (e.key === 'Escape') {
              e.preventDefault()
              onCancel()
            }
          }}
          placeholder="Raise a question or a counter-argument. The next philosopher will address it directly."
          rows={4}
          disabled={busy}
          maxLength={INTERJECTION_MAX_LEN + 50}
        />
        <div className="interjection-footer">
          <span className={`counter ${trimmed.length > INTERJECTION_MAX_LEN ? 'over' : ''}`}>
            {trimmed.length} / {INTERJECTION_MAX_LEN}
          </span>
          <div style={{ flex: 1 }} />
          <button
            type="button"
            className="btn ghost small"
            onClick={onCancel}
            disabled={busy}
          >
            Cancel
          </button>
          <button
            type="button"
            className="btn accent small"
            onClick={handleSubmit}
            disabled={!canSubmit}
          >
            {busy ? 'Submitting…' : 'Interject'}
          </button>
        </div>
        {error && (
          <div className="live-error" style={{ marginTop: 8 }}>
            {error}
          </div>
        )}
      </div>
    </div>
  )
}
