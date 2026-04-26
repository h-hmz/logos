import { useCallback, useEffect, useState } from 'react'
import { Masthead, type Route } from './components/Masthead'
import { StagePage } from './pages/StagePage'
import { LivePage } from './pages/LivePage'
import { VerdictPage } from './pages/VerdictPage'
import type { DebateSession, TurnResult } from './api'
import { AuthProvider, useAuth } from './AuthContext'
import { firebaseEnabled } from './firebase'

const DEBATE_STORAGE_KEY = (id: string) => `logos_debate_${id}`
const TURNS_STORAGE_KEY = (id: string) => `logos_turns_${id}`
const THEME_STORAGE_KEY = 'logos_theme'
type Theme = 'light' | 'dark'

function initialTheme(): Theme {
  const attr = document.documentElement.getAttribute('data-theme')
  if (attr === 'dark' || attr === 'light') return attr
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY)
    if (stored === 'dark' || stored === 'light') return stored
  } catch { /* noop */ }
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function parseHash(): Route {
  const h = window.location.hash.replace(/^#/, '')
  const verdict = h.match(/^\/d\/([^/]+)\/verdict\/?$/)
  if (verdict) return { page: 'verdict', id: verdict[1] }
  const live = h.match(/^\/d\/([^/]+)\/?$/)
  if (live) return { page: 'live', id: live[1] }
  return { page: 'stage' }
}

function hashFor(r: Route): string {
  if (r.page === 'stage') return '#/'
  if (r.page === 'live') return `#/d/${r.id}`
  return `#/d/${r.id}/verdict`
}

export default function App() {
  return (
    <AuthProvider>
      <AppInner />
    </AuthProvider>
  )
}

function AppInner() {
  const { user, loading, signIn } = useAuth()
  const [route, setRoute] = useState<Route>(() => parseHash())

  const [debate, setDebate] = useState<DebateSession | null>(null)
  const [turns, setTurns] = useState<Record<number, TurnResult>>({})
  const [theme, setTheme] = useState<Theme>(() => initialTheme())

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    try { localStorage.setItem(THEME_STORAGE_KEY, theme) } catch { /* noop */ }
  }, [theme])

  const toggleTheme = useCallback(() => {
    setTheme((t) => (t === 'light' ? 'dark' : 'light'))
  }, [])

  const navigate = useCallback((r: Route) => {
    window.location.hash = hashFor(r)
    setRoute(r)
    window.scrollTo(0, 0)
  }, [])

  useEffect(() => {
    const onHash = () => setRoute(parseHash())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  // Load cached debate session when routing to /d/:id
  useEffect(() => {
    if (route.page === 'stage') return
    const id = route.id
    if (debate && debate.id === id) return
    try {
      const raw = localStorage.getItem(DEBATE_STORAGE_KEY(id))
      if (raw) setDebate(JSON.parse(raw) as DebateSession)
      const rawTurns = localStorage.getItem(TURNS_STORAGE_KEY(id))
      if (rawTurns) setTurns(JSON.parse(rawTurns) as Record<number, TurnResult>)
    } catch { /* noop */ }
  }, [route, debate])

  // Persist debate & turns for refresh.
  useEffect(() => {
    if (!debate) return
    try { localStorage.setItem(DEBATE_STORAGE_KEY(debate.id), JSON.stringify(debate)) } catch { /* noop */ }
  }, [debate])

  useEffect(() => {
    if (!debate) return
    try { localStorage.setItem(TURNS_STORAGE_KEY(debate.id), JSON.stringify(turns)) } catch { /* noop */ }
  }, [turns, debate])

  const onDebateCreated = useCallback((d: DebateSession) => {
    setDebate(d)
    setTurns({})
    navigate({ page: 'live', id: d.id })
  }, [navigate])

  const onTurnCompleted = useCallback((i: number, t: TurnResult) => {
    setTurns((prev) => ({ ...prev, [i]: t }))
  }, [])

  let content: React.ReactNode
  if (route.page === 'stage') {
    content = <StagePage onDebateCreated={onDebateCreated} />
  } else if (route.page === 'live') {
    content = debate && debate.id === route.id ? (
      <LivePage
        debate={debate}
        navigate={navigate}
        initialTurns={turns}
        onTurnCompleted={onTurnCompleted}
      />
    ) : (
      <MissingDebate navigate={navigate} />
    )
  } else {
    content = debate && debate.id === route.id ? (
      <VerdictPage debate={debate} turns={turns} navigate={navigate} />
    ) : (
      <MissingDebate navigate={navigate} />
    )
  }

  const debateId = debate?.id

  if (firebaseEnabled && !loading && !user) {
    return (
      <div className="app">
        <Masthead route={route} debateId={debateId} navigate={navigate} theme={theme} onToggleTheme={toggleTheme} />
        <main className="container" style={{ padding: '120px 48px', textAlign: 'center' }}>
          <div className="t-eyebrow" style={{ marginBottom: 12 }}>Enter the Agora</div>
          <div style={{ fontFamily: 'var(--serif-d)', fontSize: 38, fontStyle: 'italic', color: 'var(--ink-2)', marginBottom: 24 }}>
            Sign in to attend the debate.
          </div>
          <button type="button" className="btn accent" onClick={signIn}>
            Sign in with Google →
          </button>
        </main>
      </div>
    )
  }

  return (
    <div className="app">
      <Masthead route={route} debateId={debateId} navigate={navigate} theme={theme} onToggleTheme={toggleTheme} />
      {content}
    </div>
  )
}

function MissingDebate({ navigate }: { navigate: (r: Route) => void }) {
  return (
    <main className="container" style={{ padding: '120px 48px', textAlign: 'center' }}>
      <div className="t-eyebrow" style={{ marginBottom: 12 }}>Lost at the gate</div>
      <div
        style={{
          fontFamily: 'var(--serif-d)',
          fontSize: 38,
          fontStyle: 'italic',
          color: 'var(--ink-2)',
          marginBottom: 16,
        }}
      >
        The bout has wandered out of memory.
      </div>
      <p className="t-lead" style={{ maxWidth: 560, margin: '0 auto 24px' }}>
        We can&apos;t find this debate session. Start a new one from the agora.
      </p>
      <button type="button" className="btn accent" onClick={() => navigate({ page: 'stage' })}>
        Return to the agora →
      </button>
    </main>
  )
}
