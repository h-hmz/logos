import { useEffect, useMemo, useState } from 'react'
import { Bust } from '../components/Bust'
import { RuleOrnament } from '../components/RuleOrnament'
import { createDebate, fetchModels, fetchPhilosophers, type DebateSession, type Model, type Philosopher } from '../api'
import { enrich, ERAS, type EnrichedPhilosopher } from '../philosopher-meta'

interface Props {
  onDebateCreated: (d: DebateSession) => void
}

const SUGGESTED_TOPICS = [
  'Is capitalism inevitable?',
  'Does a meaningful life require suffering?',
  'Should we fear, or welcome, our own obsolescence?',
  'Is freedom the absence of coercion, or the presence of options?',
]

const ARCHIVE = [
  { topic: 'Does a meaningful life require suffering?', a: 'Nagarjuna',     b: 'Marcus Aurelius', verdict: 'Split decision', tokens: '5,212' },
  { topic: 'Can reason prove faith?',                   a: 'Al-Ghazali',    b: 'Ibn Rushd',       verdict: 'Al-Ghazali',    tokens: '4,890' },
  { topic: 'Is the self real?',                         a: 'Nagarjuna',     b: 'Adi Shankara',    verdict: 'Draw',          tokens: '6,104' },
  { topic: 'Liberty or solidarity?',                    a: 'John Stuart Mill', b: 'Karl Marx',      verdict: 'Marx',        tokens: '4,620' },
]

export function StagePage({ onDebateCreated }: Props) {
  const [philosophers, setPhilosophers] = useState<EnrichedPhilosopher[]>([])
  const [models, setModels] = useState<Model[]>([])
  const [topic, setTopic] = useState('')
  const [pickA, setPickA] = useState<EnrichedPhilosopher | null>(null)
  const [pickB, setPickB] = useState<EnrichedPhilosopher | null>(null)
  const [rounds, setRounds] = useState(3)
  const [words, setWords] = useState(250)
  const [model, setModel] = useState('')
  const [era, setEra] = useState<'All' | string>('All')
  const [slot, setSlot] = useState<'a' | 'b'>('a')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    Promise.all([fetchPhilosophers(), fetchModels()])
      .then(([phils, mods]: [Philosopher[], Model[]]) => {
        setPhilosophers(phils.map(enrich))
        setModels(mods)
        if (mods.length > 0) setModel(mods[0].id)
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  const filtered = useMemo(
    () => philosophers.filter((p) => era === 'All' || p.era === era),
    [philosophers, era]
  )

  const canBegin = !!(pickA && pickB && pickA.name !== pickB.name && topic.trim().length > 4 && !loading)

  const handlePick = (p: EnrichedPhilosopher) => {
    if (pickA && pickA.name === p.name) { setPickA(null); setSlot('a'); return }
    if (pickB && pickB.name === p.name) { setPickB(null); setSlot('b'); return }
    if (slot === 'a') { setPickA(p); setSlot('b') } else { setPickB(p); setSlot('a') }
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
        model,
        max_words: words,
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
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 10 }}>
              {SUGGESTED_TOPICS.map((t) => (
                <button
                  key={t}
                  type="button"
                  className="chip"
                  onClick={() => { if (!topic.trim()) setTopic(t) }}
                >
                  {t}
                </button>
              ))}
            </div>

            <div className="corners" style={{ marginTop: 22 }}>
              <CornerSlot
                picked={pickA}
                label="Corner A"
                onClear={() => { setPickA(null); setSlot('a') }}
                active={slot === 'a' && !pickA}
              />
              <div className="versus">vs.</div>
              <CornerSlot
                picked={pickB}
                label="Corner B"
                onClear={() => { setPickB(null); setSlot('b') }}
                active={slot === 'b' && !pickB}
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
              <div className="field">
                <label>Model</label>
                <select value={model} onChange={(e) => setModel(e.target.value)}>
                  {models.map((m) => (
                    <option key={m.id} value={m.id}>{m.name}</option>
                  ))}
                </select>
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
      <section className="container roster">
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
}

function CornerSlot({ picked, label, onClear, active }: CornerSlotProps) {
  if (!picked) {
    return (
      <div className="corner empty" style={active ? { borderColor: 'var(--accent)' } : undefined}>
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
        <div style={{ width: 80 }}>
          <Bust who={picked} size="sm" showSchool={false} />
        </div>
        <div className="meta">
          <div className="name">{picked.name}</div>
          <div className="school">{picked.school}</div>
          {picked.oneliner && <div className="quote">&ldquo;{picked.oneliner}&rdquo;</div>}
        </div>
      </div>
    </div>
  )
}
