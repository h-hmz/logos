import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { fetchQuota } from './api'
import { useAuth } from './AuthContext'
import { firebaseEnabled } from './firebase'

type QuotaStatus = 'idle' | 'loading' | 'ready' | 'error'

interface QuotaCtx {
  // remaining/limit in USD.
  remaining: number | null
  limit: number | null
  status: QuotaStatus
  // Re-read /me/quota. Called lazily when the profile menu opens — the balance's
  // only display site — so spend from turns, tutor, and TTS is reflected the
  // next time the menu opens rather than re-fetched after every generation.
  refresh: () => Promise<void>
}

const QuotaContext = createContext<QuotaCtx>({
  remaining: null,
  limit: null,
  status: 'idle',
  refresh: async () => {},
})

export function QuotaProvider({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth()
  const [remaining, setRemaining] = useState<number | null>(null)
  const [limit, setLimit] = useState<number | null>(null)
  const [status, setStatus] = useState<QuotaStatus>('idle')

  const refresh = useCallback(async () => {
    // Don't fetch before we know who the caller is. With Firebase on, that
    // means a signed-in user; with Firebase off (local dev) the API uses a
    // dev token and we can fetch immediately.
    if (firebaseEnabled && !user) return
    setStatus((s) => (s === 'ready' ? s : 'loading'))
    try {
      const q = await fetchQuota()
      setRemaining(q.remaining_usd)
      setLimit(q.limit_usd)
      setStatus('ready')
    } catch (e) {
      console.warn('[logos] Failed to read quota:', e)
      // Keep any prior value; the pill hides itself only when nothing loaded.
      setStatus((s) => (s === 'ready' ? 'ready' : 'error'))
    }
  }, [user])

  useEffect(() => {
    // The balance is read lazily when the profile menu opens, so we never fetch
    // proactively here — we only react to sign-out by dropping any stale value.
    if (!firebaseEnabled) return
    if (loading || user) return
    setRemaining(null)
    setLimit(null)
    setStatus('idle')
  }, [user, loading])

  return (
    <QuotaContext.Provider value={{ remaining, limit, status, refresh }}>
      {children}
    </QuotaContext.Provider>
  )
}

export function useQuota() {
  return useContext(QuotaContext)
}
