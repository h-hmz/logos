interface RuleOrnamentProps {
  text?: string
}

export function RuleOrnament({ text }: RuleOrnamentProps) {
  return (
    <div className="rule-ornament">
      <span className="gem">◆</span>
      {text && (
        <span className="t-sc" style={{ fontSize: 12, color: 'var(--ink-soft)' }}>
          {text}
        </span>
      )}
      <span className="gem">◆</span>
    </div>
  )
}
