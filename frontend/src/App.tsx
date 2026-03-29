import { useState, useEffect, useRef, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

// ── Types ──────────────────────────────────────────────────────────────────────

interface Model {
  id: string
  name: string
}

interface Philosopher {
  name: string
  school: string
}

interface StepInfo {
  key: string
  label: string
}

interface DebateSession {
  id: string
  topic: string
  debater_a: Philosopher
  debater_b: Philosopher
  steps: StepInfo[]
  total_steps: number
}

interface TurnResult {
  key: string
  label: string
  author: string
  content: string
  step_index: number
  total_steps: number
  done: boolean
  turn_input_tokens: number
  turn_output_tokens: number
  total_input_tokens: number
  total_output_tokens: number
}

// ── Role helpers ───────────────────────────────────────────────────────────────

type Role = 'moderator' | 'a' | 'b' | 'critic' | 'judge'

function getRole(label: string, nameA: string, nameB: string): Role {
  if (label === 'Introduction') return 'moderator'
  if (label === 'Critic Analysis') return 'critic'
  if (label === "Judge's Verdict") return 'judge'
  if (label.startsWith(nameA)) return 'a'
  if (label.startsWith(nameB)) return 'b'
  return 'moderator'
}

function getPhase(label: string): 'intro' | 'round' | 'critic' | 'judge' {
  if (label === 'Introduction') return 'intro'
  if (label === 'Critic Analysis' || label.includes('Response to Critic')) return 'critic'
  if (label === "Judge's Verdict") return 'judge'
  return 'round'
}

function roleIcon(role: Role): string {
  switch (role) {
    case 'moderator': return '\u{1F399}'
    case 'a':         return '\u{1F535}'
    case 'b':         return '\u{1F7E0}'
    case 'critic':    return '\u{1F50D}'
    case 'judge':     return '\u{2696}'
  }
}

function initials(name: string): string {
  return name.slice(0, 2).toUpperCase()
}

// ── API ────────────────────────────────────────────────────────────────────────

async function fetchModels(): Promise<Model[]> {
  const res = await fetch('/models')
  if (!res.ok) throw new Error('Failed to load models')
  return res.json()
}

async function fetchPhilosophers(): Promise<Philosopher[]> {
  const res = await fetch('/philosophers')
  if (!res.ok) throw new Error('Failed to load philosophers')
  return res.json()
}

async function createDebate(params: {
  topic: string
  debater_a: string
  debater_b: string
  num_rounds: number
  model: string
  max_words: number
}): Promise<DebateSession> {
  const res = await fetch('/debates', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(params),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: 'Unknown error' }))
    throw new Error(err.detail ?? 'Failed to create debate')
  }
  return res.json()
}

async function nextTurn(debateId: string): Promise<TurnResult> {
  const res = await fetch(`/debates/${debateId}/next`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: 'Unknown error' }))
    throw new Error(err.detail ?? 'Failed to advance debate')
  }
  return res.json()
}

// ── Suggested topics ───────────────────────────────────────────────────────────

const SUGGESTIONS = [
  'Is free will an illusion?',
  'Does God exist?',
  'What is the purpose of life?',
  'Is capitalism just?',
  'Can morality exist without religion?',
  'Is suffering necessary for growth?',
]

// ── StepSidebar ────────────────────────────────────────────────────────────────

interface StepSidebarProps {
  steps: StepInfo[]
  currentStep: number
  nameA: string
  nameB: string
}

function StepSidebar({ steps, currentStep, nameA, nameB }: StepSidebarProps) {
  const activeRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    activeRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
  }, [currentStep])

  // Group steps by phase
  type Group = { phase: string; steps: { info: StepInfo; idx: number }[] }
  const groups: Group[] = []
  for (let i = 0; i < steps.length; i++) {
    const phase = getPhase(steps[i].label)
    const phaseLabel =
      phase === 'intro' ? 'Opening' :
      phase === 'round' ? 'Debate' :
      phase === 'critic' ? 'Critic Review' : 'Verdict'
    const last = groups[groups.length - 1]
    if (last && last.phase === phaseLabel) {
      last.steps.push({ info: steps[i], idx: i })
    } else {
      groups.push({ phase: phaseLabel, steps: [{ info: steps[i], idx: i }] })
    }
  }

  return (
    <aside className="step-sidebar">
      <div className="sidebar-title">Timeline</div>
      {groups.map((g) => (
        <div key={g.phase + g.steps[0].idx} className="step-group">
          <div className="step-phase-label">{g.phase}</div>
          {g.steps.map(({ info, idx }) => {
            const state =
              idx < currentStep ? 'done' : idx === currentStep ? 'active' : 'pending'
            const role = getRole(info.label, nameA, nameB)
            return (
              <div
                key={info.key}
                ref={state === 'active' ? activeRef : undefined}
                className={`step-item step-${state}`}
              >
                <span className={`step-dot role-dot-${role}`} />
                <span className="step-label">{info.label}</span>
                {state === 'done' && <span className="step-check">&#10003;</span>}
              </div>
            )
          })}
        </div>
      ))}
    </aside>
  )
}

// ── TurnCard ───────────────────────────────────────────────────────────────────

interface TurnCardProps {
  turn: TurnResult
  nameA: string
  nameB: string
  isLatest: boolean
}

function TurnCard({ turn, nameA, nameB, isLatest }: TurnCardProps) {
  const role = getRole(turn.label, nameA, nameB)
  const icon = roleIcon(role)
  const avatarName =
    role === 'a' ? nameA :
    role === 'b' ? nameB :
    role === 'moderator' ? 'moderator' :
    role === 'critic' ? 'critic' : 'judge'

  // Mirror Python's philosopher_key(): lowercase, non-alphanumeric runs → "_", strip leading/trailing "_"
  // e.g. "Kanye West" → "kanye_west", "Al-Ghazali" → "al_ghazali"
  const baseName = avatarName.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '')
  const [avatarSrc, setAvatarSrc] = useState(`/avatars/${baseName}.png`)
  const [imgError, setImgError] = useState(false)

  const handleImgError = () => {
    if (avatarSrc.endsWith('.png')) {
      setAvatarSrc(`/avatars/${baseName}.jpg`)
    } else {
      setImgError(true)
    }
  }
  const avatarLabel =
    role === 'a' ? initials(nameA) :
    role === 'b' ? initials(nameB) :
    role === 'moderator' ? 'MC' :
    role === 'critic' ? 'CR' : 'JG'

  return (
    <article className={`turn-card role-${role}${isLatest ? ' turn-enter' : ''}`}>
      <header className="turn-header">
        <div className="turn-header-left">
          {!imgError ? (
            <img
              className="turn-avatar-img"
              src={avatarSrc}
              alt={avatarName}
              onError={handleImgError}
            />
          ) : (
            <span className={`turn-avatar role-bg-${role}`} title={turn.label}>
              {avatarLabel}
            </span>
          )}
          <div className="turn-header-text">
            <span className="turn-label">{turn.label}</span>
            <span className="turn-meta">
              {icon} Turn {turn.step_index + 1} of {turn.total_steps}
              {turn.turn_input_tokens > 0 && (
                <span className="turn-tokens">
                  &middot; {(turn.turn_input_tokens + turn.turn_output_tokens).toLocaleString()} tokens
                </span>
              )}
            </span>
          </div>
        </div>
      </header>
      <div className="turn-body">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{turn.content}</ReactMarkdown>
      </div>
    </article>
  )
}

// ── Progress bar ───────────────────────────────────────────────────────────────

function ProgressBar({ current, total }: { current: number; total: number }) {
  const pct = total > 0 ? (current / total) * 100 : 0
  return (
    <div className="progress-bar-track">
      <div className="progress-bar-fill" style={{ width: `${pct}%` }} />
    </div>
  )
}

// ── Setup screen ───────────────────────────────────────────────────────────────

interface SetupProps {
  onStart: (debate: DebateSession) => void
}

function Setup({ onStart }: SetupProps) {
  const [philosophers, setPhilosophers] = useState<Philosopher[]>([])
  const [models, setModels] = useState<Model[]>([])
  const [topic, setTopic] = useState('')
  const [debaterA, setDebaterA] = useState('')
  const [debaterB, setDebaterB] = useState('')
  const [rounds, setRounds] = useState(2)
  const [maxWords, setMaxWords] = useState(250)
  const [modelId, setModelId] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showAdvanced, setShowAdvanced] = useState(false)

  useEffect(() => {
    Promise.all([fetchPhilosophers(), fetchModels()])
      .then(([philList, modelList]) => {
        setPhilosophers(philList)
        if (philList.length >= 2) {
          setDebaterA(philList[0].name)
          setDebaterB(philList[1].name)
        }
        setModels(modelList)
        if (modelList.length > 0) setModelId(modelList[0].id)
      })
      .catch((e) => setError(e.message))
  }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!topic.trim() || !debaterA || !debaterB) return
    setLoading(true)
    setError(null)
    try {
      const debate = await createDebate({
        topic: topic.trim(),
        debater_a: debaterA,
        debater_b: debaterB,
        num_rounds: rounds,
        model: modelId,
        max_words: maxWords,
      })
      onStart(debate)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }

  const canSubmit = topic.trim() && debaterA && debaterB && debaterA !== debaterB && !loading
  const selectedA = philosophers.find((p) => p.name === debaterA)
  const selectedB = philosophers.find((p) => p.name === debaterB)

  return (
    <div className="setup-screen">
      {/* Ambient background */}
      <div className="setup-bg">
        <div className="orb orb-1" />
        <div className="orb orb-2" />
      </div>

      <div className="setup-card">
        <div className="setup-brand">
          <div className="brand-icon">&#923;</div>
          <h1 className="setup-title">Logos Arena</h1>
          <p className="setup-subtitle">Where ideas clash and truth emerges</p>
        </div>

        <form className="setup-form" onSubmit={handleSubmit}>
          {/* Topic */}
          <div className="field">
            <label>Topic</label>
            <input
              type="text"
              value={topic}
              onChange={(e) => setTopic(e.target.value)}
              placeholder="What question should they debate?"
              required
              autoFocus
            />
            <div className="suggestions">
              {SUGGESTIONS.map((s) => (
                <button
                  key={s}
                  type="button"
                  className={`suggestion-chip${topic === s ? ' active' : ''}`}
                  onClick={() => setTopic(s)}
                >
                  {s}
                </button>
              ))}
            </div>
          </div>

          {/* Debater selection — VS layout */}
          <div className="field">
            <label>Debaters</label>
            <div className="vs-selector">
              <div className="vs-side vs-side-a">
                <select value={debaterA} onChange={(e) => setDebaterA(e.target.value)}>
                  {philosophers.map((p) => (
                    <option key={p.name} value={p.name} disabled={p.name === debaterB}>
                      {p.name}
                    </option>
                  ))}
                </select>
                {selectedA && <span className="vs-school">{selectedA.school}</span>}
              </div>
              <div className="vs-badge">VS</div>
              <div className="vs-side vs-side-b">
                <select value={debaterB} onChange={(e) => setDebaterB(e.target.value)}>
                  {philosophers.map((p) => (
                    <option key={p.name} value={p.name} disabled={p.name === debaterA}>
                      {p.name}
                    </option>
                  ))}
                </select>
                {selectedB && <span className="vs-school">{selectedB.school}</span>}
              </div>
            </div>
          </div>

          {/* Rounds */}
          <div className="field">
            <label>Rounds</label>
            <div className="rounds-picker">
              {[1, 2, 3, 4, 5].map((n) => (
                <button
                  key={n}
                  type="button"
                  className={`round-btn ${rounds === n ? 'active' : ''}`}
                  onClick={() => setRounds(n)}
                >
                  {n}
                </button>
              ))}
            </div>
          </div>

          {/* Advanced toggle */}
          <button
            type="button"
            className="advanced-toggle"
            onClick={() => setShowAdvanced(!showAdvanced)}
          >
            Advanced settings {showAdvanced ? '\u25B4' : '\u25BE'}
          </button>

          {showAdvanced && (
            <div className="advanced-panel">
              <div className="field">
                <label>Max words per turn &mdash; <strong>{maxWords}</strong></label>
                <input
                  type="range"
                  min={100}
                  max={800}
                  step={50}
                  value={maxWords}
                  onChange={(e) => setMaxWords(Number(e.target.value))}
                />
                <div className="range-labels"><span>Concise (100)</span><span>Detailed (800)</span></div>
              </div>
              <div className="field">
                <label>Model</label>
                <select value={modelId} onChange={(e) => setModelId(e.target.value)}>
                  {models.map((m) => (
                    <option key={m.id} value={m.id}>{m.name}</option>
                  ))}
                </select>
              </div>
            </div>
          )}

          {error && <p className="form-error">{error}</p>}

          <button type="submit" className="start-btn" disabled={!canSubmit}>
            {loading ? (
              <><span className="spinner spinner-sm" /> Starting debate...</>
            ) : (
              'Enter the Arena'
            )}
          </button>
        </form>
      </div>
    </div>
  )
}

// ── Debate screen ──────────────────────────────────────────────────────────────

interface DebateProps {
  debate: DebateSession
  onReset: () => void
}

const FONT_SIZES = [13, 15, 17, 20, 24]
const FONT_SIZE_KEY = 'logos-font-size'

function Debate({ debate, onReset }: DebateProps) {
  const [turns, setTurns] = useState<TurnResult[]>([])
  const [fontSizeIdx, setFontSizeIdx] = useState<number>(() => {
    const saved = localStorage.getItem(FONT_SIZE_KEY)
    const idx = saved ? FONT_SIZES.indexOf(Number(saved)) : -1
    return idx >= 0 ? idx : 1 // default: index 1 = 15px
  })
  const [currentStep, setCurrentStep] = useState(0)
  const [loading, setLoading] = useState(false)
  const [done, setDone] = useState(false)
  const [totalInputTokens, setTotalInputTokens] = useState(0)
  const [totalOutputTokens, setTotalOutputTokens] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  // Apply font size as a CSS variable on the root element
  useEffect(() => {
    const px = FONT_SIZES[fontSizeIdx]
    document.documentElement.style.setProperty('--font-size-content', `${px}px`)
    localStorage.setItem(FONT_SIZE_KEY, String(px))
  }, [fontSizeIdx])

  const nameA = debate.debater_a.name
  const nameB = debate.debater_b.name

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [turns, loading])

  const handleNext = useCallback(async () => {
    if (loading || done) return
    setLoading(true)
    setError(null)
    try {
      const turn = await nextTurn(debate.id)
      setTurns((prev) => [...prev, turn])
      setCurrentStep(turn.step_index + 1)
      setTotalInputTokens(turn.total_input_tokens)
      setTotalOutputTokens(turn.total_output_tokens)
      if (turn.done) setDone(true)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }, [loading, done, debate.id])

  // Keyboard shortcut: Space or Enter to advance
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.code === 'Space' || e.code === 'Enter') {
        const tag = (e.target as HTMLElement).tagName
        if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || tag === 'BUTTON') return
        e.preventDefault()
        handleNext()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [handleNext])

  const nextLabel =
    currentStep < debate.steps.length ? debate.steps[currentStep].label : null

  return (
    <div className="debate-screen">
      {/* Header */}
      <header className="debate-header">
        <div className="debate-meta">
          <span className="debate-topic">&ldquo;{debate.topic}&rdquo;</span>
          <span className="debate-participants">
            <span className="name-a">{nameA}</span>
            <span className="vs-small"> vs </span>
            <span className="name-b">{nameB}</span>
          </span>
        </div>
        <ProgressBar current={currentStep} total={debate.total_steps} />
        <div className="header-right">
          <div className="font-size-controls">
            <button
              className="font-size-btn font-size-btn-small"
              onClick={() => setFontSizeIdx(i => Math.max(0, i - 1))}
              disabled={fontSizeIdx === 0}
              title="Decrease font size"
            >A</button>
            <button
              className="font-size-btn font-size-btn-large"
              onClick={() => setFontSizeIdx(i => Math.min(FONT_SIZES.length - 1, i + 1))}
              disabled={fontSizeIdx === FONT_SIZES.length - 1}
              title="Increase font size"
            >A</button>
          </div>
          {(totalInputTokens > 0 || totalOutputTokens > 0) && (
            <div className="token-counter">
              <div className="token-row" title="Input tokens (prompt)">
                <span className="token-arrow token-in-arrow">&uarr;</span>
                <span className="token-value">{totalInputTokens.toLocaleString()}</span>
              </div>
              <div className="token-row" title="Output tokens (completion)">
                <span className="token-arrow token-out-arrow">&darr;</span>
                <span className="token-value">{totalOutputTokens.toLocaleString()}</span>
              </div>
            </div>
          )}
          <button className="reset-btn" onClick={onReset}>
            <span className="reset-icon">+</span> New
          </button>
        </div>
      </header>

      {/* Body: sidebar + transcript */}
      <div className="debate-body">
        <StepSidebar steps={debate.steps} currentStep={currentStep} nameA={nameA} nameB={nameB} />

        <main className="transcript">
          {turns.length === 0 && !loading && (
            <div className="transcript-empty">
              <div className="empty-icon">&#923;</div>
              <p>The arena is set.</p>
              <p className="empty-hint">Press the button below or hit <kbd>Space</kbd> to begin.</p>
            </div>
          )}
          {turns.map((turn, i) => (
            <TurnCard
              key={turn.key}
              turn={turn}
              nameA={nameA}
              nameB={nameB}
              isLatest={i === turns.length - 1}
            />
          ))}
          {loading && (
            <div className="generating">
              <div className="generating-pulse" />
              <div className="generating-text">
                <span className="generating-label">Generating</span>
                <span className="generating-who">{nextLabel}</span>
              </div>
            </div>
          )}
          {error && <p className="transcript-error">{error}</p>}
          <div ref={bottomRef} />
        </main>
      </div>

      {/* Footer */}
      <footer className="debate-footer">
        <span className="footer-status">
          {done
            ? 'Debate complete'
            : loading
            ? `Step ${currentStep + 1} of ${debate.total_steps}`
            : nextLabel
            ? `Next: ${nextLabel}`
            : ''}
        </span>
        <div className="footer-actions">
          <span className="footer-hint">
            {!done && !loading && <kbd>Space</kbd>}
          </span>
          {done ? (
            <button className="next-btn done" onClick={onReset}>
              New debate
            </button>
          ) : (
            <button className="next-btn" onClick={handleNext} disabled={loading}>
              {loading ? (
                <><span className="spinner spinner-sm" /> Generating...</>
              ) : (
                <>Next turn <span className="btn-arrow">&rarr;</span></>
              )}
            </button>
          )}
        </div>
      </footer>
    </div>
  )
}

// ── App root ───────────────────────────────────────────────────────────────────

export default function App() {
  const [debate, setDebate] = useState<DebateSession | null>(null)

  if (debate) {
    return <Debate debate={debate} onReset={() => setDebate(null)} />
  }
  return <Setup onStart={setDebate} />
}
