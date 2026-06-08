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
  'Confucius':          { era: 'Ancient',      dates: 'c. 551',  hatch: 'h-cross', oneliner: 'To govern well, first rectify names.' },
  'Laozi':              { era: 'Ancient',      dates: 'c. 6c.',  hatch: 'h-dots',  oneliner: 'The Tao that can be named is not the eternal Tao.' },
  'Socrates':           { era: 'Ancient',      dates: 'c. 470',  hatch: 'h-dots',  oneliner: 'The unexamined life is not worth living.' },
  'Aristotle':          { era: 'Ancient',      dates: '384',     hatch: 'h-diag',  oneliner: 'We are what we repeatedly do; excellence is a habit, not an act.' },
  'Epicurus':           { era: 'Ancient',      dates: '341',     hatch: 'h-horz',  oneliner: 'The highest pleasure is tranquility, not indulgence.' },
  'Diogenes of Sinope': { era: 'Ancient',      dates: 'c. 412',  hatch: 'h-cross', oneliner: 'I am a citizen of the world — and convention is the enemy of virtue.' },
  'Marcus Aurelius':    { era: 'Ancient',      dates: 'c. 121',  hatch: 'h-horz',  oneliner: 'Virtue is the sole good; accept what you cannot govern.' },
  'Nagarjuna':          { era: 'Ancient',      dates: 'c. 150',  hatch: 'h-dots',  oneliner: 'Emptiness is not nothing — it is interdependence.' },
  'Thomas Aquinas':     { era: 'Medieval',     dates: '1225',    hatch: 'h-diag',  oneliner: 'Faith and reason, rightly understood, cannot conflict.' },
  'Al-Ghazali':         { era: 'Medieval',     dates: '1058',    hatch: 'h-cross', oneliner: 'The heart has reasons the intellect must then verify.' },
  'Ibn Rushd':          { era: 'Medieval',     dates: '1126',    hatch: 'h-diag',  oneliner: 'If Revelation and Reason disagree, re-read the former.' },
  'Ibn Sina':           { era: 'Medieval',     dates: '980',     hatch: 'h-cross', oneliner: 'The soul knows itself directly — existence precedes every essence we can name.' },
  'Ibn Khaldun':        { era: 'Medieval',     dates: '1332',    hatch: 'h-horz',  oneliner: 'Civilisations rise by solidarity and fall by luxury.' },
  'Adam Smith':          { era: 'Modern',       dates: '1723',    hatch: 'h-dots',  oneliner: 'Not from the benevolence of the butcher do we expect our dinner, but from his self-interest.' },
  'Immanuel Kant':       { era: 'Modern',       dates: '1724',    hatch: 'h-cross', oneliner: 'Act only on maxims you could will to be universal law.' },
  'Friedrich Nietzsche': { era: 'Modern',       dates: '1844',    hatch: 'h-diag',  oneliner: 'God is dead — and we have killed him.' },
  'David Hume':         { era: 'Modern',       dates: '1711',    hatch: 'h-horz',  oneliner: 'Reason is, and ought only to be, the slave of the passions.' },
  'Jean-Jacques Rousseau': { era: 'Modern',    dates: '1712',    hatch: 'h-dots',  oneliner: 'Man is born free, and everywhere he is in chains.' },
  'Karl Marx':          { era: 'Modern',       dates: '1818',    hatch: 'h-horz',  oneliner: 'All history is the history of class struggle.' },
  'John Stuart Mill':   { era: 'Modern',       dates: '1806',    hatch: 'h-dots',  oneliner: 'Over himself, over his own body and mind, the individual is sovereign.' },
  'Simone de Beauvoir': { era: 'Modern',       dates: '1908',    hatch: 'h-diag',  oneliner: 'One is not born, but becomes, a woman.' },
  'Frantz Fanon':       { era: 'Contemporary', dates: '1925',    hatch: 'h-diag',  oneliner: 'The colonised man liberates himself by any means necessary.' },
  'Friedrich Hayek':    { era: 'Contemporary', dates: '1899',    hatch: 'h-dots',  oneliner: 'The curious task of economics is to demonstrate how little we know of what we imagine we can design.' },
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
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '')
  return `/avatars/${slug}.webp`
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
