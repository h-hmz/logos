// ── Types ──────────────────────────────────────────────────────────────────────

export interface Philosopher {
  name: string
  school: string
  era?: string
  dates?: string
  one_liner?: string
}

export type TurnRole = 'moderator' | 'a' | 'b' | 'critic' | 'judge'

// JSON schema emitted by the critic agent.
export interface CriticFinding {
  intro?: string
  debater_a?: CriticSide
  debater_b?: CriticSide
  overall?: string
}

export interface CriticSide {
  name?: string
  strongest?: string
  weakness?: string
  fallacies?: Array<{ name?: string; instance?: string }>
  challenge?: string
}

// JSON schema emitted by the judge agent.
export interface JudgeVerdict {
  summary_a?: { name?: string; thesis?: string }
  summary_b?: { name?: string; thesis?: string }
  clashes?: Array<{ title?: string; description?: string }>
  shifts?: Array<{ who?: 'A' | 'B' | 'both' | 'neither'; description?: string }>
  ranking?: { winner?: 'A' | 'B' | 'draw'; note?: string }
  synthesis?: string
  confidence?: number
  confidence_note?: string
  open?: string[]
}

export interface StepInfo {
  key: string
  label: string
}

export interface DebateSession {
  id: string
  topic: string
  debater_a: Philosopher
  debater_b: Philosopher
  steps: StepInfo[]
  total_steps: number
}

export interface TurnResult {
  key: string
  label: string
  author: string
  role?: TurnRole
  content: string
  structured?: CriticFinding | JudgeVerdict | null
  step_index: number
  total_steps: number
  done: boolean
  turn_input_tokens: number
  turn_output_tokens: number
  total_input_tokens: number
  total_output_tokens: number
}

export interface CreateDebateParams {
  topic: string
  debater_a: string
  debater_b: string
  num_rounds: number
  max_words: number
}

// ── API ────────────────────────────────────────────────────────────────────────

import { auth, firebaseEnabled } from './firebase'

async function getAuthHeader(): Promise<Record<string, string>> {
  if (firebaseEnabled && auth?.currentUser) {
    const token = await auth.currentUser.getIdToken()
    return { Authorization: `Bearer ${token}` }
  }
  // Local dev fallback: noop verifier accepts any non-empty token.
  return { Authorization: 'Bearer local-dev-token' }
}

export async function fetchPhilosophers(): Promise<Philosopher[]> {
  const res = await fetch('/philosophers')
  if (!res.ok) throw new Error('Failed to load philosophers')
  return res.json()
}

export interface DebateSnapshot {
  session: DebateSession
  turns: Record<number, TurnResult>
}

export async function getDebate(debateId: string): Promise<DebateSnapshot> {
  const res = await fetch(`/debates/${debateId}`, {
    headers: await getAuthHeader(),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: 'Unknown error' }))
    throw new Error(err.detail ?? 'Failed to load debate')
  }
  const { turns, ...session } = await res.json() as DebateSession & { turns: Record<number, TurnResult> }
  return { session, turns: turns ?? {} }
}

export async function createDebate(params: CreateDebateParams): Promise<DebateSession> {
  const res = await fetch('/debates', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...await getAuthHeader() },
    body: JSON.stringify(params),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: 'Unknown error' }))
    throw new Error(err.detail ?? 'Failed to create debate')
  }
  return res.json()
}

// streamTTS calls the backend TTS endpoint and yields Float32 PCM chunks as
// they arrive. Returns the sample rate (from the Content-Type rate= param)
// alongside an async iterator. When the server falls back to a non-streaming
// response (audio/wav), the WAV header is stripped and the payload yielded
// as a single chunk.
export interface TTSStream {
  sampleRate: number
  chunks: AsyncGenerator<Float32Array, void, unknown>
}

export async function streamTTS(
  debateId: string,
  stepKey: string,
  text: string,
): Promise<TTSStream> {
  const res = await fetch(`/debates/${debateId}/tts`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...await getAuthHeader() },
    body: JSON.stringify({ step_key: stepKey, text }),
  })
  if (!res.ok || !res.body) {
    const err = await res.json?.().catch(() => ({ detail: 'TTS failed' }))
    throw new Error(err?.detail ?? 'TTS failed')
  }
  const ct = res.headers.get('Content-Type') ?? ''
  const isWav = ct.includes('audio/wav')
  const m = /rate=(\d+)/.exec(ct)
  const sampleRate = m ? parseInt(m[1], 10) : 24000

  async function* chunks(): AsyncGenerator<Float32Array, void, unknown> {
    const reader = res.body!.getReader()
    // Carry holds leftover bytes across reads so we only emit full int16 samples.
    let carry = new Uint8Array(0)
    let skip = isWav ? 44 : 0 // RIFF header length for PCM WAV
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      if (!value || value.length === 0) continue

      let incoming: Uint8Array = value
      if (skip > 0) {
        if (incoming.length <= skip) { skip -= incoming.length; continue }
        incoming = incoming.subarray(skip)
        skip = 0
      }

      const merged = new Uint8Array(carry.length + incoming.length)
      merged.set(carry, 0)
      merged.set(incoming, carry.length)
      const usable = merged.length - (merged.length % 2)
      if (usable === 0) { carry = merged; continue }

      // Copy into a fresh aligned buffer so Int16Array can read it directly.
      const aligned = merged.slice(0, usable)
      carry = merged.subarray(usable)

      const i16 = new Int16Array(aligned.buffer, aligned.byteOffset, usable / 2)
      const f32 = new Float32Array(i16.length)
      for (let i = 0; i < i16.length; i++) f32[i] = i16[i] / 32768
      yield f32
    }
  }

  return { sampleRate, chunks: chunks() }
}

// ── Tutor ──────────────────────────────────────────────────────────────────────

export interface TutorMessage {
  role: 'user' | 'tutor'
  content: string
}

export interface TutorResult {
  content: string
  suggestions: string[]
}

// Stream a tutor explanation via SSE; invokes onToken for each partial token
// and resolves with the final content and follow-up suggestions carried on the
// `done` event.
export async function streamTutor(
  debateId: string,
  body: { selection: string; turn_key: string; history: TutorMessage[] },
  onToken: (text: string) => void,
): Promise<TutorResult> {
  const res = await fetch(`/debates/${debateId}/tutor`, {
    method: 'POST',
    headers: {
      Accept: 'text/event-stream',
      'Content-Type': 'application/json',
      ...await getAuthHeader(),
    },
    body: JSON.stringify(body),
  })
  if (!res.ok || !res.body) {
    const err = await res.json?.().catch(() => ({ detail: 'Stream failed' }))
    throw new Error(err?.detail ?? 'Stream failed')
  }
  // Backend currently responds with JSON; SSE will return when streaming
  // is re-introduced. The parser below stays in place for that day.
  if (!res.headers.get('content-type')?.includes('text/event-stream')) {
    const parsed = await res.json() as { content?: string; suggestions?: string[] }
    return { content: parsed?.content ?? '', suggestions: parsed?.suggestions ?? [] }
  }
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let finalResult: TutorResult | null = null
  let errorMsg: string | null = null

  while (true) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })

    let idx: number
    while ((idx = buffer.indexOf('\n\n')) !== -1) {
      const raw = buffer.slice(0, idx)
      buffer = buffer.slice(idx + 2)

      let eventName = 'message'
      const dataLines: string[] = []
      for (const line of raw.split('\n')) {
        if (line.startsWith('event:')) eventName = line.slice(6).trim()
        else if (line.startsWith('data:')) dataLines.push(line.slice(5).trimStart())
      }
      const data = dataLines.join('\n')

      if (eventName === 'token') {
        try {
          const parsed = JSON.parse(data)
          const text = typeof parsed === 'string' ? parsed : parsed?.text ?? ''
          if (text) onToken(text)
        } catch {
          if (data) onToken(data)
        }
      } else if (eventName === 'done') {
        try {
          const parsed = JSON.parse(data) as { content?: string; suggestions?: string[] }
          finalResult = { content: parsed?.content ?? '', suggestions: parsed?.suggestions ?? [] }
        } catch { /* noop */ }
      } else if (eventName === 'error') {
        try {
          const parsed = JSON.parse(data)
          errorMsg = parsed?.error ?? parsed?.detail ?? data
        } catch {
          errorMsg = data
        }
      }
    }
  }

  if (errorMsg) throw new Error(errorMsg)
  if (finalResult !== null) return finalResult
  throw new Error('Stream ended without a result')
}

export async function nextTurn(debateId: string): Promise<TurnResult> {
  const res = await fetch(`/debates/${debateId}/next`, {
    method: 'POST',
    headers: await getAuthHeader(),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: 'Unknown error' }))
    throw new Error(err.detail ?? 'Failed to advance debate')
  }
  return res.json()
}

// Stream a turn via SSE; invokes onToken for each partial token and resolves with the
// final TurnResult carried on the `done` event. Throws on `error` events.
export async function streamTurn(
  debateId: string,
  onToken: (text: string) => void,
): Promise<TurnResult> {
  const res = await fetch(`/debates/${debateId}/next`, {
    method: 'POST',
    headers: {
      Accept: 'text/event-stream',
      ...await getAuthHeader(),
    },
  })
  if (!res.ok || !res.body) {
    const err = await res.json?.().catch(() => ({ detail: 'Stream failed' }))
    throw new Error(err?.detail ?? 'Stream failed')
  }
  // Backend currently responds with JSON; SSE will return when streaming
  // is re-introduced. The parser below stays in place for that day.
  if (!res.headers.get('content-type')?.includes('text/event-stream')) {
    return await res.json() as TurnResult
  }
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let currentEvent = 'message'
  let finalResult: TurnResult | null = null
  let errorMsg: string | null = null

  while (true) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })

    let idx: number
    while ((idx = buffer.indexOf('\n\n')) !== -1) {
      const raw = buffer.slice(0, idx)
      buffer = buffer.slice(idx + 2)

      let eventName = 'message'
      const dataLines: string[] = []
      for (const line of raw.split('\n')) {
        if (line.startsWith('event:')) eventName = line.slice(6).trim()
        else if (line.startsWith('data:')) dataLines.push(line.slice(5).trimStart())
      }
      const data = dataLines.join('\n')
      currentEvent = eventName

      if (currentEvent === 'token') {
        try {
          const parsed = JSON.parse(data)
          const text = typeof parsed === 'string' ? parsed : parsed?.text ?? ''
          if (text) onToken(text)
        } catch {
          if (data) onToken(data)
        }
      } else if (currentEvent === 'done') {
        try { finalResult = JSON.parse(data) as TurnResult } catch { /* noop */ }
      } else if (currentEvent === 'error') {
        try {
          const parsed = JSON.parse(data)
          errorMsg = parsed?.detail ?? parsed?.message ?? data
        } catch {
          errorMsg = data
        }
      }
    }
  }

  if (errorMsg) throw new Error(errorMsg)
  if (finalResult) return finalResult
  throw new Error('Stream ended without a result')
}
