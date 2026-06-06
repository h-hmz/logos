import { useAuth } from '../AuthContext'
import { firebaseEnabled } from '../firebase'
import { ProfileMenu } from './ProfileMenu'

export type Route =
  | { page: 'stage' }
  | { page: 'live'; id: string }
  | { page: 'verdict'; id: string }

interface MastheadProps {
  route: Route
  debateId?: string
  navigate: (r: Route) => void
  theme: 'light' | 'dark'
  onToggleTheme: () => void
}

export function Masthead({ route, debateId, navigate, theme, onToggleTheme }: MastheadProps) {
  const page = route.page
  const { user } = useAuth()
  // The profile dropdown hosts the theme toggle (and budget/sign-out). It shows
  // whenever there's an identity to attach it to: a signed-in user, or — with
  // Firebase off in local dev — a placeholder "dev user". The only state without
  // a profile menu is the signed-out gate, which falls back to a standalone
  // toggle so the control is never lost.
  const showProfile = !firebaseEnabled || Boolean(user)
  const liveHref = debateId ? { page: 'live' as const, id: debateId } : null
  const verdictHref = debateId ? { page: 'verdict' as const, id: debateId } : null

  return (
    <header className="masthead">
      <button type="button" className="mark" onClick={() => navigate({ page: 'stage' })} aria-label="Home">
        <img src={theme === 'dark' ? '/dark_owl.png' : '/warm_owl.png'} alt="Logos Owl" style={{ height: '40px', objectFit: 'contain' }} />
        <div className="name">LOGOS</div>
      </button>
      <nav>
        <a
          href="#/"
          className={page === 'stage' ? 'active' : ''}
          onClick={(e) => { e.preventDefault(); navigate({ page: 'stage' }) }}
        >
          Agora
        </a>
        <a
          href={liveHref ? `#/d/${liveHref.id}` : '#'}
          className={page === 'live' ? 'active' : ''}
          aria-disabled={!liveHref}
          onClick={(e) => {
            e.preventDefault()
            if (liveHref) navigate(liveHref)
          }}
          style={!liveHref ? { opacity: 0.4, pointerEvents: 'none' } : undefined}
        >
          Live Bout
        </a>
        <a
          href={verdictHref ? `#/d/${verdictHref.id}/verdict` : '#'}
          className={page === 'verdict' ? 'active' : ''}
          aria-disabled={!verdictHref}
          onClick={(e) => {
            e.preventDefault()
            if (verdictHref) navigate(verdictHref)
          }}
          style={!verdictHref ? { opacity: 0.4, pointerEvents: 'none' } : undefined}
        >
          Verdict
        </a>
        {showProfile ? (
          <ProfileMenu theme={theme} onToggleTheme={onToggleTheme} />
        ) : (
          <button
            type="button"
            className="theme-toggle"
            onClick={onToggleTheme}
            aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
            title={theme === 'dark' ? 'Light' : 'Dark'}
          >
            {theme === 'dark' ? '☉' : '☾'}
          </button>
        )}
      </nav>
    </header>
  )
}
