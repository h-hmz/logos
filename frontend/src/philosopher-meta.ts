// Frontend-side augmentation of the philosopher records returned by the backend.
// The backend provides only {name, school}; the design requires {era, dates,
// initials, hatch pattern, oneliner}. Known thinkers get curated metadata;
// unknown ones fall back to deterministic defaults derived from the name.

import type { Philosopher } from './api'

export type Hatch = 'h-cross' | 'h-diag' | 'h-dots' | 'h-horz'
export type Era = 'Ancient' | 'Medieval' | 'Modern' | 'Contemporary'
export const ERAS: readonly Era[] = ['Ancient', 'Medieval', 'Modern', 'Contemporary']

export interface PhilosopherMeta {
  era: Era
  dates: string
  hatch: Hatch
  oneliner: string
}

const HATCHES: Hatch[] = ['h-cross', 'h-diag', 'h-dots', 'h-horz']

const META: Record<string, PhilosopherMeta> = {
  'Marcus Aurelius':    { era: 'Ancient',      dates: 'c. 121',  hatch: 'h-horz',  oneliner: 'Virtue is the sole good; accept what you cannot govern.' },
  'Nagarjuna':          { era: 'Ancient',      dates: 'c. 150',  hatch: 'h-dots',  oneliner: 'Emptiness is not nothing — it is interdependence.' },
  'Adi Shankara':       { era: 'Ancient',      dates: 'c. 700',  hatch: 'h-cross', oneliner: 'The self and the absolute are not two.' },
  'Thomas Aquinas':     { era: 'Medieval',     dates: '1225',    hatch: 'h-diag',  oneliner: 'Faith and reason, rightly understood, cannot conflict.' },
  'Al-Ghazali':         { era: 'Medieval',     dates: '1058',    hatch: 'h-cross', oneliner: 'The heart has reasons the intellect must then verify.' },
  'Ibn Rushd':          { era: 'Medieval',     dates: '1126',    hatch: 'h-diag',  oneliner: 'If Revelation and Reason disagree, re-read the former.' },
  'Karl Marx':          { era: 'Modern',       dates: '1818',    hatch: 'h-horz',  oneliner: 'All history is the history of class struggle.' },
  'John Stuart Mill':   { era: 'Modern',       dates: '1806',    hatch: 'h-dots',  oneliner: 'Over himself, over his own body and mind, the individual is sovereign.' },
  'Simone de Beauvoir': { era: 'Modern',       dates: '1908',    hatch: 'h-diag',  oneliner: 'One is not born, but becomes, a woman.' },
}

function hashCode(s: string): number {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h << 5) - h + s.charCodeAt(i) | 0
  return Math.abs(h)
}

export function metaFor(name: string): PhilosopherMeta {
  if (META[name]) return META[name]
  return {
    era: 'Modern',
    dates: '—',
    hatch: HATCHES[hashCode(name) % HATCHES.length],
    oneliner: '',
  }
}

export function initialsFor(name: string): string {
  const parts = name.split(/\s+/).filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase()
}

export interface EnrichedPhilosopher extends PhilosopherMeta {
  name: string
  school: string
  one_liner?: string
  initials: string
}

export function avatarFor(name: string): string {
  const slug = name.toLowerCase().replace(/[\s-]+/g, '_')
  return `/avatars/${slug}.jpg`
}

export function enrich(p: Philosopher): EnrichedPhilosopher {
  const meta = metaFor(p.name)
  // Backend-supplied era/dates/one_liner take precedence when present.
  const era = (p.era && (ERAS as readonly string[]).includes(p.era) ? (p.era as Era) : meta.era)
  const dates = p.dates && p.dates.trim() ? p.dates : meta.dates
  const oneliner = p.one_liner && p.one_liner.trim() ? p.one_liner : meta.oneliner
  return {
    ...p,
    era,
    dates,
    hatch: meta.hatch,
    oneliner,
    initials: initialsFor(p.name),
  }
}
