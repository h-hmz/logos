import { InlineMd } from './InlineMd'

const SENTENCE_HIGHLIGHT_TRANSITION_MS = 120
// Nominal speech rate (spoken chars/sec) used whenever the real audio length
// can't anchor the mapping: while streaming, and when the clip is detected as
// truncated. Deliberately slow — a highlight that lags the voice reads far
// better than one that runs ahead into unspoken text.
const TTS_FALLBACK_CPS = 11
// Fastest plausible speech rate. If spreading the whole text over the actual
// audio duration would imply a rate above this, the clip cannot contain the
// whole text — Gemini truncates output at its ~2-min per-call cap (ADR 0002),
// so proportional stretching would sweep the highlight through the unspoken
// tail. In that case fall back to the nominal-rate layout, whose starts past
// the audio end simply never activate.
const MAX_PLAUSIBLE_CPS = 20
// TTS puts a beat of silence at sentence boundaries, so short sentences get
// more air time than their raw length suggests. Weighted as extra chars.
const SENTENCE_PAUSE_PAD = 8

// splitSentences breaks text into sentence-like chunks, keeping terminators.
// Good enough for prose debate turns; doesn't try to handle "Dr.", "e.g.", etc.
// It also ensures closing quotes, brackets, and markdown asterisks/underscores
// are kept glued to the end of the sentence they terminate.
export function splitSentences(text: string): string[] {
  const parts = text.match(/[^.!?\n]+[.!?]+[""')\]*_]*[\s]*|[^.!?\n]+$/g)
  if (!parts) return text.trim() ? [text] : []
  return parts.map((p) => p.trim()).filter(Boolean)
}

// speakableWeight estimates how much air time a sentence gets, in
// char-equivalents. It counts only what the voice actually says: the server
// strips parentheticals before synthesis, and markdown syntax (emphasis
// marks, link URLs) is never spoken, so weighting them would drift the
// highlight ahead of or behind the voice.
export function speakableWeight(s: string): number {
  const spoken = s
    .replace(/\s*\([^()]*\)/g, '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[*_`#]/g, '')
  return Math.max(spoken.length, 1) + SENTENCE_PAUSE_PAD
}

// sentenceStarts returns the cumulative start time (seconds) of each sentence,
// weighted by estimated spoken length. Pass `finalDuration` only once the full
// audio length is known (stream complete); while streaming, omit it and we'll
// estimate from a conservative chars/sec fallback instead of crushing the
// whole text into the partial duration received so far.
//
// A finalDuration too short to plausibly contain the whole text means the clip
// was truncated at the model's per-call output cap (ADR 0002). Stretching the
// text into it would highlight sentences that were never spoken, so the
// nominal-rate layout is kept: playback position never reaches the starts past
// the real audio end, and the unspoken tail never lights up.
export function sentenceStarts(sentences: string[], finalDuration?: number): number[] {
  const weights = sentences.map(speakableWeight)
  const total = weights.reduce((a, b) => a + b, 0)
  let effectiveDur = total / TTS_FALLBACK_CPS
  if (finalDuration && finalDuration > 0 && total / finalDuration <= MAX_PLAUSIBLE_CPS) {
    effectiveDur = finalDuration
  }
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
