export type Role = 'moderator' | 'a' | 'b' | 'critic' | 'judge'

// Parse a backend step label (e.g. "Introduction", "Karl Marx - Opening Statement",
// "Critic Analysis", "Judge's Verdict") into a role.
export function roleForLabel(label: string, nameA: string, nameB: string): Role {
  if (label === 'Introduction' || /^moderator/i.test(label)) return 'moderator'
  if (label === 'Critic Analysis' || /^critic/i.test(label)) return 'critic'
  if (label === "Judge's Verdict" || /^judge/i.test(label)) return 'judge'
  if (label.startsWith(nameA)) return 'a'
  if (label.startsWith(nameB)) return 'b'
  return 'moderator'
}

// Strip the leading "Name - " prefix to leave just the round/role label.
export function trimmedLabel(label: string, nameA: string, nameB: string): string {
  for (const n of [nameA, nameB]) {
    if (label.startsWith(n)) {
      const rest = label.slice(n.length).replace(/^\s*[-–—]\s*/, '').trim()
      if (rest) return rest
    }
  }
  return label
}
