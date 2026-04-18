import type { EnrichedPhilosopher } from '../philosopher-meta'
import { avatarFor } from '../philosopher-meta'

interface BustProps {
  who: EnrichedPhilosopher
  size?: 'md' | 'sm' | 'xs'
  showSchool?: boolean
}

export function Bust({ who, size = 'md', showSchool = true }: BustProps) {
  const sizeClass = size === 'sm' ? 'small' : size === 'xs' ? 'tiny' : ''
  return (
    <div className={`bust ${who.hatch} ${sizeClass}`.trim()}>
      <div className="initials">{who.initials}</div>
      <img
        src={avatarFor(who.name)}
        alt={who.name}
        className="avatar-img"
        onError={(e) => { (e.target as HTMLImageElement).style.display = 'none' }}
      />
      {showSchool && size === 'md' && (
        <div className="school-tag">{who.school.split(' / ')[0]}</div>
      )}
    </div>
  )
}
