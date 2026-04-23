import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { streamTutor, type TutorMessage } from '../api'
import { InlineMd } from './InlineMd'
import { NoctuaIcon } from './NoctuaIcon'

interface Props {
  debateId: string
  turnKey: string
  selection: string
  visible: boolean
  onClose: () => void
}

interface ChatEntry {
  role: 'user' | 'tutor'
  content: string
  streaming?: boolean
  error?: string
}

const MAX_SELECTION_PREVIEW = 320

export function TutorPanel({ debateId, turnKey, selection, visible, onClose }: Props) {
  const [entries, setEntries] = useState<ChatEntry[]>([])
  const [draft, setDraft] = useState('Explain this passage.')
  const [busy, setBusy] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)
  const composerRef = useRef<HTMLTextAreaElement>(null)
  // Ref mirror of `entries` so async callbacks see the latest chat log without
  // closing over stale state.
  const entriesRef = useRef<ChatEntry[]>([])
  useEffect(() => { entriesRef.current = entries }, [entries])

  const toHistory = (list: ChatEntry[]): TutorMessage[] =>
    list
      .filter((e) => !e.error && e.content.trim().length > 0)
      .map((e) => ({ role: e.role, content: e.content }))

  const ask = useCallback(
    async (userText: string) => {
      if (busy) return
      const userEntry: ChatEntry = { role: 'user', content: userText }
      const tutorEntry: ChatEntry = { role: 'tutor', content: '', streaming: true }

      // Build history from the current ref BEFORE dispatching the state update.
      // Includes prior completed entries plus the new user question; excludes
      // the empty streaming tutor placeholder we're about to append.
      const history = toHistory([...entriesRef.current, userEntry])

      entriesRef.current = [...entriesRef.current, userEntry, tutorEntry]
      setEntries(entriesRef.current)
      setBusy(true)

      try {
        const final = await streamTutor(
          debateId,
          { selection, turn_key: turnKey, history },
          (chunk) => {
            setEntries((prev) => {
              const copy = prev.slice()
              const last = copy[copy.length - 1]
              if (last && last.role === 'tutor' && last.streaming) {
                copy[copy.length - 1] = { ...last, content: last.content + chunk }
              }
              return copy
            })
          },
        )
        setEntries((prev) => {
          const copy = prev.slice()
          const last = copy[copy.length - 1]
          if (last && last.role === 'tutor' && last.streaming) {
            copy[copy.length - 1] = { role: 'tutor', content: final || last.content }
          }
          return copy
        })
      } catch (e) {
        const msg = e instanceof Error ? e.message : 'Tutor request failed'
        setEntries((prev) => {
          const copy = prev.slice()
          const last = copy[copy.length - 1]
          if (last && last.role === 'tutor' && last.streaming) {
            copy[copy.length - 1] = { role: 'tutor', content: last.content, error: msg }
          }
          return copy
        })
      } finally {
        setBusy(false)
      }
    },
    [busy, debateId, selection, turnKey],
  )

  // Focus the composer on open (mount or reopen) and preselect the default so
  // the user can hit Enter to send as-is, or start typing to replace it.
  useEffect(() => {
    if (!visible) return
    const el = composerRef.current
    if (!el) return
    el.focus()
    if (entries.length === 0) el.select()
  }, [visible, entries.length])

  // Auto-scroll to bottom on new content.
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
  }, [entries])

  // Close on Escape.
  useEffect(() => {
    if (!visible) return
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose, visible])

  const handleSubmit = () => {
    const trimmed = draft.trim()
    if (!trimmed || busy) return
    setDraft('')
    ask(trimmed)
  }

  const onComposerKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSubmit()
    }
  }

  const preview =
    selection.length > MAX_SELECTION_PREVIEW
      ? selection.slice(0, MAX_SELECTION_PREVIEW).trimEnd() + '…'
      : selection

  return (
    <aside
      className="tutor-panel"
      role="complementary"
      aria-label="Noctua"
      style={{ display: visible ? 'flex' : 'none' }}
      aria-hidden={!visible}
    >
      <header className="tutor-panel-head">
        <div className="tutor-panel-mark" aria-hidden="true">
          <NoctuaIcon size={40} />
        </div>
        <div className="tutor-panel-title">
          <span className="t-sc">Noctua</span>
          <h3>Explore this passage</h3>
        </div>
        <button
          type="button"
          className="btn ghost small"
          onClick={onClose}
          aria-label="Close Noctua"
        >
          ✕
        </button>
      </header>

      <div className="tutor-selection">
        <div className="t-sc" style={{ marginBottom: 4 }}>Highlighted</div>
        <blockquote>{preview}</blockquote>
      </div>

      <div className="tutor-chat" ref={scrollRef}>
        {entries.map((e, i) => (
          <div key={i} className={`tutor-msg ${e.role}`}>
            <div className="tutor-msg-role t-sc">{e.role === 'user' ? 'You' : 'Noctua'}</div>
            <div className="tutor-msg-body">
              {e.content ? <InlineMd text={e.content} /> : e.streaming ? <em>considering…</em> : null}
              {e.streaming && e.content && <span className="cursor">·</span>}
              {e.error && <div className="live-error" style={{ marginTop: 6 }}>{e.error}</div>}
            </div>
          </div>
        ))}
      </div>

      <div className="tutor-composer">
        <textarea
          ref={composerRef}
          value={draft}
          onChange={(ev) => setDraft(ev.target.value)}
          onKeyDown={onComposerKey}
          placeholder={busy ? 'Noctua is replying…' : 'Ask Noctua (Enter to send, Shift+Enter for newline)'}
          rows={2}
          disabled={busy}
          maxLength={2000}
        />
        <button
          type="button"
          className="btn accent small"
          onClick={handleSubmit}
          disabled={busy || draft.trim().length === 0}
        >
          Ask
        </button>
      </div>
    </aside>
  )
}
