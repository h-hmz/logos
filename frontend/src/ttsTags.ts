// TTS delivery cues emitted by the debater LLM and consumed by Gemini TTS.
// Gemini supports 200+ audio tags, so the strip is shape-based rather than
// keyword-based: anything that looks like a short `[cue]` token gets
// removed for display, while markdown links `[label](url)` are preserved
// by the negative-lookahead on `(`.

// Closed bracket expression up to 40 chars, no newlines or nested brackets,
// not immediately followed by `(` (so markdown links survive).
const closedTagRE = /\[[^\][\n]{1,40}\](?!\()/g

// Unclosed trailing bracket like "… text [exci" or "… text [pause 2". Used
// only on the streaming buffer so partial cues don't briefly flash.
const trailingOpenTagRE = /\[[^\][\n]{0,40}$/

// stripTTSTags removes Gemini delivery cues from text and tidies the
// whitespace they leave behind. Markdown links and plain prose are left
// untouched.
export function stripTTSTags(text: string): string {
  return collapseWhitespace(text.replace(closedTagRE, ''))
}

// stripTTSTagsStreaming additionally hides an unclosed trailing bracket
// token at the end of the buffer, so a half-streamed `[excit` doesn't
// briefly flash before the closing `]` arrives.
export function stripTTSTagsStreaming(buf: string): string {
  const closed = buf.replace(closedTagRE, '')
  const trimmed = closed.replace(trailingOpenTagRE, '')
  return collapseWhitespace(trimmed)
}

function collapseWhitespace(s: string): string {
  // Collapse runs of spaces/tabs (but not newlines) created by tag removal,
  // and tidy stray " ," / " ." patterns that can appear when a tag sat next
  // to punctuation.
  return s
    .replace(/[ \t]{2,}/g, ' ')
    .replace(/[ \t]+([,.;:!?])/g, '$1')
}
