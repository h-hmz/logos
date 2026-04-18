interface TokenPillProps {
  n: number | string
  label?: string
  live?: boolean
}

export function TokenPill({ n, label, live }: TokenPillProps) {
  return (
    <span className={`token-pill${live ? ' live' : ''}`}>
      <span className="dot" />
      {label ? <span>{label}</span> : null}
      <span>{typeof n === 'number' ? n.toLocaleString() : n} tok</span>
    </span>
  )
}
