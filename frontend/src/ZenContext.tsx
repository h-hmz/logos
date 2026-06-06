import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'

// Zen mode is a reader preference: it hides the per-turn token/cost readouts on
// the Live Bout page so the debate reads as prose, not a billing meter. It's a
// cross-cutting UI concern (toggled in the masthead profile menu, consumed deep
// in the live transcript), so it lives in context rather than prop-drilling.
const ZEN_STORAGE_KEY = 'logos_zen'

interface ZenCtx {
  zen: boolean
  toggleZen: () => void
}

const ZenContext = createContext<ZenCtx>({ zen: false, toggleZen: () => {} })

export function ZenProvider({ children }: { children: ReactNode }) {
  const [zen, setZen] = useState<boolean>(() => {
    try {
      return localStorage.getItem(ZEN_STORAGE_KEY) === '1'
    } catch {
      return false
    }
  })

  useEffect(() => {
    try {
      localStorage.setItem(ZEN_STORAGE_KEY, zen ? '1' : '0')
    } catch {
      /* noop */
    }
  }, [zen])

  const toggleZen = useCallback(() => setZen((z) => !z), [])

  return <ZenContext.Provider value={{ zen, toggleZen }}>{children}</ZenContext.Provider>
}

export function useZen() {
  return useContext(ZenContext)
}
