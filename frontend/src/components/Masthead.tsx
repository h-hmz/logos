export type Route =
  | { page: 'stage' }
  | { page: 'live'; id: string }
  | { page: 'verdict'; id: string }

interface MastheadProps {
  route: Route
  debateId?: string
  navigate: (r: Route) => void
}

export function Masthead({ route, debateId, navigate }: MastheadProps) {
  const page = route.page
  const liveHref = debateId ? { page: 'live' as const, id: debateId } : null
  const verdictHref = debateId ? { page: 'verdict' as const, id: debateId } : null

  return (
    <header className="masthead">
      <button type="button" className="mark" onClick={() => navigate({ page: 'stage' })} aria-label="Home">
        <div className="glyph">λ</div>
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
      </nav>
    </header>
  )
}
