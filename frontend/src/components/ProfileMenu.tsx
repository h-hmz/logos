import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useAuth } from '../AuthContext'
import { useQuota } from '../QuotaContext'
import { useZen } from '../ZenContext'
import { firebaseEnabled } from '../firebase'

// formatRemaining renders the balance shown in the menu's Budget row:
//  - 0         -> "$0.00 left" (truly depleted)
//  - (0, 0.01) -> "<$0.01 left" (never floor a real balance to $0.00)
//  - otherwise -> floored to cents, e.g. "$0.63 left" (never overstate)
function formatRemaining(remaining: number): string {
  if (remaining === 0) return '$0.00 left'
  if (remaining < 0.01) return '<$0.01 left'
  const floored = Math.floor(remaining * 100) / 100
  return `$${floored.toFixed(2)} left`
}

// BudgetSpinner — the same rotating arc the Recite button uses, sized for the
// Budget row while the first /me/quota read is in flight.
function BudgetSpinner() {
  return (
    <svg
      className="spin"
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="3"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <path d="M21 12a9 9 0 1 1-6.219-8.56" />
    </svg>
  )
}

// Line icons drawn with currentColor so they inherit the menu item's text
// color (matches the NoctuaIcon convention). 24×24 viewBox, feather-style.
function MenuIcon({ size = 16, children }: { size?: number; children: ReactNode }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {children}
    </svg>
  )
}

// LogoutIcon — door with an out-arrow.
function LogoutIcon(props: { size?: number }) {
  return (
    <MenuIcon {...props}>
      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
      <polyline points="16 17 21 12 16 7" />
      <line x1="21" y1="12" x2="9" y2="12" />
    </MenuIcon>
  )
}

// SunIcon / MoonIcon — the theme-toggle glyphs.
function SunIcon(props: { size?: number }) {
  return (
    <MenuIcon {...props}>
      <circle cx="12" cy="12" r="5" />
      <line x1="12" y1="1" x2="12" y2="3" />
      <line x1="12" y1="21" x2="12" y2="23" />
      <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
      <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
      <line x1="1" y1="12" x2="3" y2="12" />
      <line x1="21" y1="12" x2="23" y2="12" />
      <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
      <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
    </MenuIcon>
  )
}
function MoonIcon(props: { size?: number }) {
  return (
    <MenuIcon {...props}>
      <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
    </MenuIcon>
  )
}

// LeafIcon — the Zen-mode glyph: a calm leaf for distraction-free reading.
function LeafIcon(props: { size?: number }) {
  return (
    <MenuIcon {...props}>
      <path d="M11 20A7 7 0 0 1 9.8 6.1C15.5 5 17 4.48 19 2c1 2 2 4.18 2 8 0 5.5-4.78 10-10 10Z" />
      <path d="M2 21c0-3 1.85-5.36 5.08-6" />
    </MenuIcon>
  )
}

interface ProfileMenuProps {
  theme: 'light' | 'dark'
  onToggleTheme: () => void
}

// ProfileMenu — the rightmost masthead control. The avatar is a button that
// toggles a dropdown holding account controls: the lifetime budget, the
// light/dark theme toggle, and sign-out.
export function ProfileMenu({ theme, onToggleTheme }: ProfileMenuProps) {
  const { user, signOut } = useAuth()
  const { remaining, status, refresh } = useQuota()
  const { zen, toggleZen } = useZen()
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  // Close on outside click or Escape so the dropdown behaves like a real menu.
  useEffect(() => {
    if (!open) return
    const onPointer = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onPointer)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onPointer)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  // In local dev (Firebase off) there's no real account, but we still want the
  // profile button so every config shares the same control surface. Show a
  // placeholder identity; Sign out is hidden below since there's no session.
  const devMode = !firebaseEnabled
  if (!user && !devMode) return null

  const name = user?.displayName ?? (devMode ? 'Dev User' : 'Account')
  const email = user?.email ?? (devMode ? 'local development' : undefined)
  const photoURL = user?.photoURL ?? undefined
  const initial = (user?.displayName ?? user?.email ?? name).trim().charAt(0).toUpperCase()
  const budget =
    status === 'ready' && remaining !== null ? formatRemaining(remaining) : null

  return (
    <div className="profile-menu" ref={rootRef}>
      <button
        type="button"
        className="profile-trigger"
        onClick={() =>
          setOpen((v) => {
            // Read the balance lazily: fetch /me/quota only as the menu opens.
            if (!v) void refresh()
            return !v
          })
        }
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Account menu"
      >
        {photoURL ? (
          <img
            src={photoURL}
            alt={name}
            className="masthead-avatar"
            referrerPolicy="no-referrer"
          />
        ) : (
          <span className="profile-avatar-fallback">{initial}</span>
        )}
      </button>

      {open && (
        <div className="profile-dropdown" role="menu">
          <div className="profile-dropdown-head">
            <div className="profile-name">{name}</div>
            {email && <div className="profile-email">{email}</div>}
          </div>

          {(budget || status === 'loading') && (
            <div className="profile-row" role="presentation">
              <span className="profile-row-label">Budget</span>
              <span className="profile-row-value">{budget ?? <BudgetSpinner />}</span>
            </div>
          )}

          <button
            type="button"
            className="profile-item"
            role="menuitem"
            onClick={onToggleTheme}
          >
            {theme === 'dark' ? <SunIcon /> : <MoonIcon />}
            <span>{theme === 'dark' ? 'Light mode' : 'Dark mode'}</span>
          </button>

          <button
            type="button"
            className={`profile-item profile-toggle has-desc${zen ? ' on' : ''}`}
            role="menuitemcheckbox"
            aria-checked={zen}
            onClick={toggleZen}
            title="Hide token and cost counts for distraction-free reading"
          >
            <LeafIcon />
            <span className="profile-item-text">
              <span>Zen mode</span>
              <span className="profile-item-desc">Hide token &amp; cost counts</span>
            </span>
            <span className="profile-toggle-state" aria-hidden="true">{zen ? 'On' : 'Off'}</span>
          </button>

          {user && (
            <button type="button" className="profile-item" role="menuitem" onClick={signOut}>
              <LogoutIcon />
              <span>Sign out</span>
            </button>
          )}
        </div>
      )}
    </div>
  )
}
