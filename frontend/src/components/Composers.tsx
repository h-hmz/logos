import { useEffect, useRef, useState } from 'react'

const INTERJECTION_MAX_LEN = 800

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
