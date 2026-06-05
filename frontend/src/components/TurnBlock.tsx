import { useMemo, useState } from 'react'
import { formatCostUSD } from '../api'
import { type EnrichedPhilosopher } from '../philosopher-meta'
import { stripTTSTags, stripTTSTagsStreaming } from '../ttsTags'
import { Bust } from './Bust'
import { InlineMd } from './InlineMd'
import { PlayButton, type AudioCoordinator } from './PlayButton'
import {
  SpokenText,
  activeSentenceIndex,
  sentenceStarts,
  splitSentences,
} from './SpokenText'

interface TurnBlockProps {
  who: EnrichedPhilosopher
  side: 'a' | 'b'
  label: string
  text: string
  // fullText, when present, is the complete content even if the paced
  // reveal hasn't finished animating yet. PlayButton uses this to start
  // TTS as soon as the data is ready.
  fullText?: string
  streaming: boolean
  // This turn's output token count, prompt (input) token count, and
  // provider-billed cost in USD — shown together in the side panel.
  tokens: number
  inputTokens: number
  costUSD: number
  active: boolean
  error?: string
  debateId?: string
  stepKey?: string
  coordinator?: AudioCoordinator
  // The owner's interjection that steered this turn, rendered as an aside.
  interjection?: string
}

export function TurnBlock({
  who,
  side,
  label,
  text,
  fullText,
  streaming,
  tokens,
  inputTokens,
  costUSD,
  active,
  error,
  debateId,
  stepKey,
  coordinator,
  interjection,
}: TurnBlockProps) {
  const selectable = !streaming && !!text && !!stepKey
  // Raw `text` carries Gemini TTS delivery cues like [excited]; hide them
  // from the reader while keeping the raw string for the TTS POST body.
  const displayText = streaming ? stripTTSTagsStreaming(text) : stripTTSTags(text)
  // audioText is whatever the TTS endpoint should receive: the full
  // content as soon as the stream resolves, falling back to the revealed
  // text once the reveal has caught up. Empty until either is ready.
  const audioText = fullText ?? (streaming ? '' : text)
  const paragraphs = displayText.split(/\n\n+/).filter((p) => p.length > 0)
  if (paragraphs.length === 0) paragraphs.push('')
  const [pos, setPos] = useState(0)
  const [finalDur, setFinalDur] = useState<number | undefined>(undefined)
  const [playing, setPlaying] = useState(false)
  // Flatten paragraphs to a linear sentence list to keep indices consistent
  // with what SpokenText renders internally.
  const allSentences = useMemo(() => paragraphs.flatMap(splitSentences), [displayText])
  const starts = useMemo(() => sentenceStarts(allSentences, finalDur), [allSentences, finalDur])
  const activeIdx = activeSentenceIndex(starts, pos, playing)
  return (
    <div className={`turn ${side}${active ? ' now' : ''}`}>
      <div className="who">
        <Bust who={who} size="sm" showSchool={false} />
        <div className="name">{who.name}</div>
        <div className="school">{who.school}</div>
        {(tokens > 0 || inputTokens > 0) && (
          <div className="usage">
            <div className="toks" title="input · output tokens">
              {inputTokens.toLocaleString()} in · {tokens.toLocaleString()} out
            </div>
            {costUSD > 0 && (
              <div className="cost" title="provider-billed cost for this turn">
                {formatCostUSD(costUSD)}
              </div>
            )}
          </div>
        )}
      </div>
      <div className="say">
        <div className="label-row">
          <span className="round">◆ {label}</span>
          <span
            className="stat"
            style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}
          >
            {streaming
              ? `streaming · ${displayText.length} ch`
              : tokens || inputTokens
                ? `${(inputTokens + tokens).toLocaleString()} tok`
                : ''}
            {audioText &&
              debateId &&
              stepKey &&
              coordinator && (
                <PlayButton
                  debateId={debateId}
                  stepKey={stepKey}
                  text={audioText}
                  coordinator={coordinator}
                  onTime={setPos}
                  onFinalDuration={setFinalDur}
                  onStateChange={setPlaying}
                />
              )}
          </span>
        </div>
        {interjection && (
          <div className="interjection-aside">
            <span className="interjection-tag">⟢ A voice from the agora</span>
            <span className="interjection-text">{interjection}</span>
          </div>
        )}
        <div className="body" data-turn-key={selectable ? stepKey : undefined}>
          {streaming ? (
            paragraphs.map((p, i) => (
              <p key={i} className={i === 0 ? 'drop' : ''}>
                <InlineMd text={p} />
                {i === paragraphs.length - 1 && <span className="cursor">·</span>}
              </p>
            ))
          ) : (
            <SpokenText text={displayText} activeIndex={activeIdx} asParagraphs />
          )}
        </div>
        {error && <div className="live-error">{error}</div>}
      </div>
    </div>
  )
}
