import { useEffect, useMemo, useRef, useState } from 'react'
import { Bust } from '../components/Bust'
import { RuleOrnament } from '../components/RuleOrnament'
import { createDebate, fetchModels, fetchPhilosophers, modelLabel, type DebateSession, type ModelOption, type Philosopher } from '../api'
import { enrich, ERAS, type EnrichedPhilosopher } from '../philosopher-meta'

interface Props {
  onDebateCreated: (d: DebateSession) => void
}

const SUGGESTED_TOPICS = [
  'Can artificial intelligence be conscious?',
  'Do we have free will, or is everything determined?',
  'What is the nature of time?',
  'Is consciousness purely physical?',
  'Is reality independent of perception?',
  'Can morality exist without religion?',
  'Is meaning something we find or create?',
  'Is existence itself meaningful?',
  'Do ends ever justify means?',
  'Should we fear, or welcome, our own obsolescence?',
  'Is truth discovered or constructed?',
  'Can we ever be objective?',
  'Are values universal or relative?',
  'Is justice universal or culturally defined?',
  'What makes a person the same over time?',
  'Should we prioritize individuals or collectives?',
  'Is freedom the absence of coercion, or the presence of options?',
  'Can ethics be scientific?',
  'Is identity self-defined or socially imposed?',
  'Should we pursue truth regardless of consequences?',
  'Is authenticity possible in a social world?',
  'Should we value stability over change?',
  'Is progress inherently good?',
  'Can knowledge be dangerous?',
  'Is skepticism a virtue or a limitation?',
  'Is power inherently corrupting?',
  'Is punishment or rehabilitation more just?',
  'Do we owe anything to future generations?',
  'Is loyalty a virtue or a bias?',
  'Can one live a good life without purpose?',
  'Are humans naturally good or evil?',
  'Can suffering be meaningful?',
  'Does everything happen for a reason?',
  'Is hope rational?',
  'Should we always tell the truth?',
  'Can love be rational?',
  'Is autonomy more important than belonging?',
  'Is security worth sacrificing freedom?',
  'Can society exist without hierarchy?',
  'Is democracy the best system?',
  'Should morality evolve over time?',
  'Should we trust intuition or reason?',
  'Is curiosity inherently good?',
  'Can one be moral without self-sacrifice?',
  'Is self-interest compatible with morality?',
  'Should we forgive everything?',
  'Is revenge ever justified?',
  'Can we truly know another person?',
  'Is empathy always good?',
  'Should we prioritize truth or kindness?',
  'Is wisdom more valuable than knowledge?',
  'Is certainty achievable?',
  'Should we seek certainty or embrace ambiguity?',
  'Can paradoxes be true?',
  'Is beauty subjective or objective?',
  'Does language shape thought?',
  'Is boredom necessary for creativity?',
  'Should we seek comfort or growth?',
  'Is failure necessary for growth?',
  'Is resilience learned or innate?',
  'Is ambition inherently good?',
  'Should success be measured individually or collectively?',
  'Are emotions rational?',
  'Is modern life alienating us?',
  'Does technology distance us from ourselves?',
  'Is innovation always beneficial?',
  'Should we preserve the past or prioritize the future?',
  'Is civilization progress or constraint?',
  'Are traditions valuable or restrictive?',
  'Is control an illusion?',
  'Is simplicity superior to complexity?',
  'Is nostalgia a distortion of truth?',
  'Can happiness be objectively measured?',
  'Do we control our desires?',
  'Is mediocrity acceptable?',
  'Should we aim for perfection?',
  'Can we truly know ourselves?',
  'Is privacy a fundamental right?',
  'Are laws expressions of morality or control?',
  'Is rebellion a moral duty?',
  'Can one be free in a society?',
  'Is discomfort necessary for growth?',
  'Can altruism truly exist?',
  'Are humans inherently selfish?',
  'Does modern life improve human flourishing?',
  'Should we fear uncertainty?',
  'Is despair ever justified?',
  'Can we build a just society?',
  'Is loyalty to people stronger than loyalty to principles?',
  'Is authenticity more important than acceptance?',
  'Can one live ethically in an unjust system?',
  'Is language enough to express reality?',
  'Are emotions a guide to truth or a distraction from it?',
  'Can humanity outgrow its need for myths?',
  'Is the search for meaning itself meaningful?',
  'Should we care more about being right or being good?',
  'Is existence without struggle still valuable?',
  'Can a person reinvent themselves completely?',
  'Should we enhance humans with technology?',
  'Does everything that matters resist measurement?',
];

function pickRandomTopics(n: number): string[] {
  const pool = [...SUGGESTED_TOPICS]
  for (let i = pool.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[pool[i], pool[j]] = [pool[j], pool[i]]
  }
  return pool.slice(0, n).sort((a, b) => a.length - b.length)
}

const ARCHIVE = [
  { topic: 'Is morality based on cultivating virtue, or on universal moral duties?',  a: 'Aristotle',  b: 'Immanuel Kant', verdict: 'Draw', tokens: '5,212' },
  { topic: 'Are there limits to reason in understanding ultimate truth?',             a: 'Al-Ghazali', b: 'Ibn Rushd',     verdict: 'Al-Ghazali',    tokens: '4,890' },
  { topic: 'Does capitalism promote human flourishing or exploitation?',              a: 'Karl Marx',  b: 'Adam Smith',   verdict: 'Draw',          tokens: '6,104' },
  { topic: 'Should society be shaped by moral order or natural harmony?',             a: 'Confucius',  b: 'Laozi', verdict: 'Confucius',     tokens: '4,620' },
]

export function StagePage({ onDebateCreated }: Props) {
  const [philosophers, setPhilosophers] = useState<EnrichedPhilosopher[]>([])
  const [topic, setTopic] = useState('')
  const [pickA, setPickA] = useState<EnrichedPhilosopher | null>(null)
  const [pickB, setPickB] = useState<EnrichedPhilosopher | null>(null)
  // Authorized Debater Models and the per-corner choice. Empty string means the
  // "Default" option (the server falls back to its default model).
  const [models, setModels] = useState<ModelOption[]>([])
  const [modelA, setModelA] = useState('')
  const [modelB, setModelB] = useState('')
  const [rounds, setRounds] = useState(3)
  const [words, setWords] = useState(250)
  const [era, setEra] = useState<'All' | string>('All')
  const [slot, setSlot] = useState<'a' | 'b'>('a')
  const [loading, setLoading] = useState(false)
  const [loadingRoster, setLoadingRoster] = useState(true)
  const [suggestedTopics, setSuggestedTopics] = useState<string[]>(() => pickRandomTopics(5))
  const [showSuggestions, setShowSuggestions] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const rosterRef = useRef<HTMLElement>(null)

  useEffect(() => {
    fetchPhilosophers()
      .then((phils: Philosopher[]) => setPhilosophers(phils.map(enrich)))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoadingRoster(false))
  }, [])

  // Load the model allowlist for the per-debater dropdowns. A failure here is
  // non-fatal: the dropdowns still offer "Default", so debates can proceed.
  useEffect(() => {
    fetchModels()
      .then(setModels)
      .catch(() => setModels([]))
  }, [])

  const filtered = useMemo(
    () => philosophers.filter((p) => era === 'All' || p.era === era),
    [philosophers, era]
  )

  const canBegin = !!(pickA && pickB && pickA.name !== pickB.name && topic.trim().length > 4 && !loading && !loadingRoster)

  const handlePick = (p: EnrichedPhilosopher) => {
    if (pickA && pickA.name === p.name) { setPickA(null); setSlot('a'); return }
    if (pickB && pickB.name === p.name) { setPickB(null); setSlot('b'); return }
    if (slot === 'a') { setPickA(p); setSlot('b') } else { setPickB(p); setSlot('a') }
  }

  const handleShuffleTopics = () => {
    setSuggestedTopics(pickRandomTopics(5))
  }

  const handleBegin = async () => {
    if (!canBegin || !pickA || !pickB) return
    setLoading(true)
    setError(null)
    try {
      const debate = await createDebate({
        topic: topic.trim(),
        debater_a: pickA.name,
        debater_b: pickB.name,
        num_rounds: rounds,
        max_words: words,
        model_a: modelA,
        model_b: modelB,
      })
      onDebateCreated(debate)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unknown error')
      setLoading(false)
    }
  }

  return (
    <main>
      {/* Hero */}
      <section className="container stage">
        <div>
          <div className="t-eyebrow overline">An arena for arguments</div>
          <h1 className="t-display title">
            Pick two.<br />
            <em className="accent">Give them something to argue about.</em>
          </h1>
          <p className="t-lead lede">
            Logos is a multi-agent debate stage. Two philosophers face each other across
            several rounds; a critic intervenes; a judge delivers a verdict. You are
            the impresario.
          </p>
          <div className="stage-stats">
            <div className="s">
              <div className="n">{philosophers.length || 12}</div>
              <div className="l">Thinkers in residence</div>
            </div>
            <div className="s">
              <div className="n">2N<span className="soft">+5</span></div>
              <div className="l">Turns per bout</div>
            </div>
            <div className="s">
              <div className="n">∞</div>
              <div className="l">Topics to propose</div>
            </div>
          </div>
        </div>

        {/* Topic + corners */}
        <div>
          <div className="panel" style={{ padding: '24px 26px' }}>
            <div className="t-eyebrow" style={{ marginBottom: 10 }}>
              The question on the table
            </div>
            <input
              className="topic-input"
              value={topic}
              onChange={(e) => setTopic(e.target.value)}
              placeholder="e.g. Is capitalism inevitable?"
              autoFocus
            />
            {showSuggestions ? (
              <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'stretch', gap: 6, marginTop: 10 }}>
                {suggestedTopics.map((t) => (
                  <button
                    key={t}
                    type="button"
                    className="chip"
                    onClick={() => setTopic(t)}
                    style={{ textAlign: 'center', padding: '8px 12px' }}
                  >
                    {t}
                  </button>
                ))}
                <button
                  type="button"
                  className="chip"
                  onClick={handleShuffleTopics}
                  style={{
                    fontStyle: 'italic',
                    textTransform: 'none',
                    letterSpacing: '0.02em',
                    color: 'var(--accent-ink)',
                    borderStyle: 'dashed',
                    textAlign: 'center',
                    padding: '8px 12px',
                    marginTop: 4,
                  }}
                  title="Show different topics"
                >
                  shuffle →
                </button>
              </div>
            ) : (
              <button
                type="button"
                className="chip"
                onClick={() => setShowSuggestions(true)}
                style={{
                  marginTop: 10,
                  fontStyle: 'italic',
                  textTransform: 'none',
                  letterSpacing: '0.02em',
                  color: 'var(--accent-ink)',
                  borderStyle: 'dashed',
                  padding: '8px 12px',
                }}
              >
                need inspiration? →
              </button>
            )}

            <div className="corners" style={{ marginTop: 22 }}>
              <CornerSlot
                picked={pickA}
                label="Corner A"
                onClear={() => { setPickA(null); setSlot('a') }}
                active={slot === 'a' && !pickA}
                onScrollToRoster={() => rosterRef.current?.scrollIntoView({ behavior: 'smooth' })}
                models={models}
                model={modelA}
                onModelChange={setModelA}
              />
              <div className="versus">vs.</div>
              <CornerSlot
                picked={pickB}
                label="Corner B"
                onClear={() => { setPickB(null); setSlot('b') }}
                active={slot === 'b' && !pickB}
                onScrollToRoster={() => rosterRef.current?.scrollIntoView({ behavior: 'smooth' })}
                models={models}
                model={modelB}
                onModelChange={setModelB}
              />
            </div>

            <div className="setup-row">
              <div className="field">
                <label>Rounds</label>
                <div className="segmented">
                  {[1, 2, 3, 4, 5].map((n) => (
                    <button
                      key={n}
                      type="button"
                      className={rounds === n ? 'on' : ''}
                      onClick={() => setRounds(n)}
                    >
                      {n}
                    </button>
                  ))}
                </div>
              </div>
              <div className="field">
                <label>Word limit · per turn</label>
                <div className="val">
                  {words}{' '}
                  <span className="soft" style={{ fontSize: 12, fontFamily: 'var(--mono)' }}>
                    words
                  </span>
                </div>
                <input
                  type="range"
                  min={120}
                  max={500}
                  step={10}
                  value={words}
                  onChange={(e) => setWords(+e.target.value)}
                />
              </div>
            </div>

            <div className="setup-footer">
              <div className="t-small t-italic">
                {canBegin ? (
                  <>
                    Total turns: <b>{2 * rounds + 5}</b> — one moderator, {2 * rounds} rounds,
                    one critic, two responses, one judge.
                  </>
                ) : (
                  <span>Pick two speakers and pose a question to begin.</span>
                )}
              </div>
              <button type="button" className="btn accent" disabled={!canBegin} onClick={handleBegin}>
                {loading ? 'Ringing…' : 'Ring the bell →'}
              </button>
            </div>

            {error && (
              <div className="live-error" style={{ marginTop: 16 }}>{error}</div>
            )}
          </div>
        </div>
      </section>

      <div className="container">
        <RuleOrnament text="The roster" />
      </div>

      {/* Roster */}
      <section ref={rosterRef} className="container roster">
        <div className="roster-head">
          <div>
            <div className="t-eyebrow">Choose your speakers</div>
            <div className="t-body" style={{ marginTop: 4 }}>
              Pick one for each corner. Click again to unseat.
            </div>
          </div>
          <div className="roster-filters">
            {(['All', ...ERAS] as const).map((e) => (
              <button
                key={e}
                type="button"
                className={`chip ${era === e ? 'on' : ''}`}
                onClick={() => setEra(e)}
              >
                {e}
              </button>
            ))}
          </div>
        </div>
        <div className="roster-grid">
          {loadingRoster && (
            <div style={{ gridColumn: '1 / -1', padding: '48px 0', textAlign: 'center', color: 'var(--ink-soft)', fontStyle: 'italic' }}>
              Awaiting the thinkers…
            </div>
          )}
          {filtered.map((p) => {
            const picked = (pickA && pickA.name === p.name) || (pickB && pickB.name === p.name)
            return (
              <button
                type="button"
                key={p.name}
                className={`roster-card${picked ? ' picked' : ''}`}
                onClick={() => handlePick(p)}
              >
                <Bust who={p} size="sm" showSchool={false} />
                <div className="name">{p.name}</div>
                <div className="school">{p.school}</div>
                <div className="era">{p.dates && p.dates !== '—' ? p.dates : p.era}</div>
              </button>
            )
          })}
        </div>
      </section>

      <div className="container">
        <RuleOrnament text="Past bouts" />
      </div>

      <section className="container" style={{ paddingBottom: 80 }}>
        <div className="archive-strip">
          {ARCHIVE.map((a, i) => (
            <div key={i} className="archive-item">
              <div className="m">{a.a} vs. {a.b}</div>
              <div className="t">&ldquo;{a.topic}&rdquo;</div>
              <div className="tok">Verdict: {a.verdict} · {a.tokens} tok</div>
            </div>
          ))}
        </div>
      </section>
    </main>
  )
}

interface CornerSlotProps {
  picked: EnrichedPhilosopher | null
  label: string
  onClear: () => void
  active: boolean
  onScrollToRoster: () => void
  models: ModelOption[]
  model: string
  onModelChange: (id: string) => void
}

function CornerSlot({ picked, label, onClear, active, onScrollToRoster, models, model, onModelChange }: CornerSlotProps) {
  if (!picked) {
    return (
      <div
        className="corner empty"
        style={{ ...(active ? { borderColor: 'var(--accent)' } : undefined), cursor: 'pointer' }}
        onClick={onScrollToRoster}
      >
        <div className="hdr">
          <div className="k" style={active ? { color: 'var(--accent)' } : undefined}>
            {label} · choose
          </div>
        </div>
        <div className="empty-msg">
          {active ? 'tap a thinker below' : 'awaiting a speaker'}
        </div>
      </div>
    )
  }
  return (
    <div className="corner">
      <div className="hdr">
        <div className="k">{label}</div>
        <button type="button" className="swap" onClick={onClear}>× remove</button>
      </div>
      <div className="body">
        <div className="bust-container">
          <Bust who={picked} size="sm" showSchool={false} />
        </div>
        <div className="meta">
          <div className="name">{picked.name}</div>
          <div className="school">{picked.school}</div>
        </div>
        {picked.oneliner && <div className="quote">&ldquo;{picked.oneliner}&rdquo;</div>}
        <div className="model-pick">
          <label>Model</label>
          <select value={model} onChange={(e) => onModelChange(e.target.value)}>
            <option value="">Default</option>
            {models.map((m) => (
              <option key={m.id} value={m.id}>{modelLabel(m.id)}</option>
            ))}
          </select>
        </div>
      </div>
    </div>
  )
}
