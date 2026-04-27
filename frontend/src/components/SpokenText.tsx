import { InlineMd } from './InlineMd'

const SENTENCE_HIGHLIGHT_TRANSITION_MS = 120
const TTS_FALLBACK_CPS = 11

// splitSentences breaks text into sentence-like chunks, keeping terminators.
// Good enough for prose debate turns; doesn't try to handle "Dr.", "e.g.", etc.
// It also ensures closing quotes, brackets, and markdown asterisks/underscores
// are kept glued to the end of the sentence they terminate.
export function splitSentences(text: string): string[] {
  const parts = text.match(/[^.!?\n]+[.!?]+[""')\]*_]*[\s]*|[^.!?\n]+$/g)
  if (!parts) return text.trim() ? [text] : []
  return parts.map((p) => p.trim()).filter(Boolean)
}

// sentenceStarts returns the cumulative start time (seconds) of each sentence,
// weighted proportionally by character count. Pass `finalDuration` only once
// the full audio length is known (stream complete); while streaming, omit it
// and we'll estimate from a conservative chars/sec fallback instead of
// crushing the whole text into the partial duration received so far.
export function sentenceStarts(sentences: string[], finalDuration?: number): number[] {
  const weights = sentences.map((s) => Math.max(s.length, 1))
  const total = weights.reduce((a, b) => a + b, 0)
  const effectiveDur =
    finalDuration && finalDuration > 0 ? finalDuration : total / TTS_FALLBACK_CPS
  const starts: number[] = []
  let acc = 0
  for (const w of weights) {
    starts.push((acc / total) * effectiveDur)
    acc += w
  }
  return starts
}

// activeSentenceIndex returns the index of the sentence whose start is the
// latest one <= pos, or -1 if pos is before the first sentence or duration=0.
export function activeSentenceIndex(
  starts: number[],
  pos: number,
  playing: boolean,
): number {
  if (!playing || starts.length === 0) return -1
  let idx = -1
  for (let i = 0; i < starts.length; i++) {
    if (starts[i] <= pos) idx = i
    else break
  }
  return idx
}

interface SpokenTextProps {
  text: string
  activeIndex: number
  asParagraphs?: boolean
}

// SpokenText renders text with optional per-sentence highlighting. When
// activeIndex is -1 no sentence is marked.
export function SpokenText({ text, activeIndex, asParagraphs }: SpokenTextProps) {
  const paragraphs = asParagraphs ? text.split(/\n\n+/).filter(Boolean) : [text]
  let runningIdx = 0
  return (
    <>
      {paragraphs.map((p, pi) => {
        const sentences = splitSentences(p)
        const startIdx = runningIdx
        runningIdx += sentences.length
        const rendered = sentences.map((s, si) => {
          const globalIdx = startIdx + si
          const active = globalIdx === activeIndex
          return (
            <span
              key={si}
              className={active ? 'reading-active' : undefined}
              style={
                active
                  ? {
                      background: 'color-mix(in srgb, var(--accent) 22%, transparent)',
                      boxShadow:
                        '0 0 0 2px color-mix(in srgb, var(--accent) 22%, transparent)',
                      borderRadius: 2,
                      transition: `background ${SENTENCE_HIGHLIGHT_TRANSITION_MS}ms linear`,
                    }
                  : undefined
              }
            >
              <InlineMd text={s} />
              {si < sentences.length - 1 ? ' ' : ''}
            </span>
          )
        })
        return asParagraphs ? (
          <p key={pi} className={pi === 0 ? 'drop' : ''}>
            {rendered}
          </p>
        ) : (
          <span key={pi}>{rendered}</span>
        )
      })}
    </>
  )
}
