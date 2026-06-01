import { useQuota } from '../QuotaContext'

// formatRemaining renders the balance the way the masthead pill shows it:
//  - negative  -> unlimited (quota disabled)
//  - 0         -> "$0.00 left" (truly depleted)
//  - (0, 0.01) -> "<$0.01 left" (never floor a real balance to $0.00)
//  - otherwise -> floored to cents, e.g. "$0.63 left" (never overstate)
export function formatRemaining(remaining: number): string {
  if (remaining < 0) return '∞'
  if (remaining === 0) return '$0.00 left'
  if (remaining < 0.01) return '<$0.01 left'
  const floored = Math.floor(remaining * 100) / 100
  return `$${floored.toFixed(2)} left`
}

export function QuotaPill() {
  const { remaining, status } = useQuota()

  if (status === 'loading') {
    return <span className="quota-pill skeleton" aria-hidden="true" />
  }
  // Nothing loaded (error / signed out): stay quiet — the balance is
  // informational and shouldn't nag or block the app.
  if (status !== 'ready' || remaining === null) return null

  const depleted = remaining === 0
  return (
    <span className={`quota-pill${depleted ? ' depleted' : ''}`} title="Generation budget remaining">
      {formatRemaining(remaining)}
    </span>
  )
}
