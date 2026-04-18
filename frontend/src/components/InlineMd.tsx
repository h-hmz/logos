import { Fragment, type ReactNode } from 'react'

const PATTERN = /\*\*([\s\S]+?)\*\*|\*([^*\n]+?)\*|_([^_\n]+?)_/g

export function renderInlineMd(text: string): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  let key = 0
  let m: RegExpExecArray | null
  PATTERN.lastIndex = 0
  while ((m = PATTERN.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index))
    if (m[1] !== undefined) out.push(<strong key={key++}>{m[1]}</strong>)
    else if (m[2] !== undefined) out.push(<em key={key++}>{m[2]}</em>)
    else if (m[3] !== undefined) out.push(<em key={key++}>{m[3]}</em>)
    last = PATTERN.lastIndex
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

export function InlineMd({ text }: { text: string }) {
  return <Fragment>{renderInlineMd(text)}</Fragment>
}
