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
export interface ScorecardRow {
  criterion?: string
  score_a?: number
  score_b?: number
  note?: string
}

export interface JudgeVerdict {
  scorecard?: ScorecardRow[]
  turning_point?: { quote?: string; who?: 'A' | 'B'; why?: string }
  ranking?: { note?: string }
  synthesis?: string
  open?: string[]
}

// Sum the scorecard columns. The judge never names a winner; the client derives
// it from these totals so the ruling is always consistent with the card.
export function scorecardTotals(v: JudgeVerdict | null): { a: number; b: number } {
  let a = 0, b = 0
  for (const row of v?.scorecard ?? []) {
    a += row.score_a ?? 0
    b += row.score_b ?? 0
  }
  return { a, b }
}

// Winner derived from scorecard totals: 'A' | 'B' | 'draw', or null when there
// is no scorecard yet.
export function verdictWinner(v: JudgeVerdict | null): 'A' | 'B' | 'draw' | null {
  if (!v?.scorecard?.length) return null
  const { a, b } = scorecardTotals(v)
  return a === b ? 'draw' : a > b ? 'A' : 'B'
}

// Parse a JSON-output step's content into a typed object. Returns null if the
// string isn't valid JSON yet (mid-stream) or doesn't match. The server stores
// raw model output as a string; callers parse on demand for the steps whose
// schema they know (critic_feedback → CriticFinding, final_verdict → JudgeVerdict).
export function parseStructured<T>(content: string): T | null {
  if (!content) return null
  try { return JSON.parse(content) as T } catch { return null }
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
  step_index: number
  total_steps: number
  done: boolean
  turn_input_tokens: number
  turn_output_tokens: number
  // Provider-billed cost of this turn's generation, in USD.
  turn_cost_usd: number
  total_input_tokens: number
  total_output_tokens: number
  // The owner's interjection that conditioned this turn, echoed for rendering.
  interjection?: string
  // The model that produced this step. A retry may run on a different model than
  // the role was assigned at creation (the Retry Model).
  model?: string
}

export interface CreateDebateParams {
  topic: string
  debater_a: string
  debater_b: string
  num_rounds: number
  max_words: number
}

// MODELS is the authorized model allowlist, mirrored from the backend
// (backend/internal/models/models.go).
export const MODELS = [
  'deepseek/deepseek-v4-pro',
  'qwen/qwen3-235b-a22b-2507',
  'z-ai/glm-4.6',
] as const

// DEFAULT_MODEL is the client-side default a debate starts every role on, until
// the user picks otherwise.
export const DEFAULT_MODEL: string = MODELS[2]

// DebateModels is the client's per-role model choice for a debate: the two
// debaters (which may differ) and a single model for the referee steps
// (moderator, critic, judge). The frontend holds this and sends the right one on
// each generation.
export interface DebateModels {
  a: string
  b: string
  referee: string
}

// modelLabel prettifies a model id for display: ids are provider/model slugs
// (e.g. "z-ai/glm-4.6"), and the part after the slash reads cleanest.
export function modelLabel(id: string): string {
  const i = id.indexOf('/')
  return i === -1 ? id : id.slice(i + 1)
}

// formatCostUSD renders a per-turn cost. Turn costs are usually a fraction of a
// cent, so sub-cent values keep four decimals (never floored to $0.00, which
// would hide a real charge); a cent or more rounds to the usual two.
export function formatCostUSD(usd: number): string {
  if (usd <= 0) return '$0.00'
  if (usd < 0.0001) return '<$0.0001'
  if (usd < 0.01) return `$${usd.toFixed(4)}`
  return `$${usd.toFixed(2)}`
}

// ── API ────────────────────────────────────────────────────────────────────────

import { auth, firebaseEnabled } from './firebase'

// ApiError carries the HTTP status alongside the message so callers can
// distinguish, e.g., a 402 (quota exhausted) from a generic failure. The
// message stays the server's `detail` so existing error displays are unchanged.
export class ApiError extends Error {
  status: number
  detail: string
  constructor(status: number, detail: string) {
    super(detail)
    this.name = 'ApiError'
    this.status = status
    this.detail = detail
  }
}

// Base URL for the backend API. Empty in local browser dev so requests stay
// root-relative and flow through Vite's dev proxy (see vite.config.ts). Set
// VITE_API_BASE to an absolute origin (e.g. the Cloud Run URL) for production
// builds and native (Capacitor) shells, where no proxy exists. Trailing slash
// stripped so `${API_BASE}/debates` never doubles up.
const API_BASE = (import.meta.env.VITE_API_BASE ?? '').replace(/\/$/, '')

async function getAuthHeader(): Promise<Record<string, string>> {
  if (firebaseEnabled) {
    // Firebase is configured, so a real ID token is required. Never fall back to
    // the dev token here — the backend's real verifier rejects it with a 401.
    // The App-level gate holds requests until the session is restored, so this
    // throw is a belt-and-suspenders guard for any call that slips through.
    const user = auth?.currentUser
    if (!user) throw new ApiError(401, 'Not signed in')
    const token = await user.getIdToken()
    return { Authorization: `Bearer ${token}` }
  }
  // Local dev (no Firebase configured): the noop verifier accepts any token.
  return { Authorization: 'Bearer local-dev-token' }
}

// Quota is the authenticated user's lifetime dollar budget. A negative
// limit_usd/remaining_usd means unlimited (enforcement disabled); see ADR 0009.
export interface Quota {
  limit_usd: number
  spent_usd: number
  remaining_usd: number
}

export async function fetchQuota(): Promise<Quota> {
  const res = await fetch(`${API_BASE}/me/quota`, {
    headers: await getAuthHeader(),
  })
  if (!res.ok) throw new ApiError(res.status, 'Failed to load quota')
  return res.json()
}

export async function fetchPhilosophers(): Promise<Philosopher[]> {
  const res = await fetch(`${API_BASE}/philosophers`, {
    headers: await getAuthHeader(),
  })
  if (!res.ok) throw new Error('Failed to load data')
  return res.json()
}

export interface DebateSnapshot {
  session: DebateSession
  turns: Record<number, TurnResult>
}

export async function getDebate(debateId: string): Promise<DebateSnapshot> {
  const res = await fetch(`${API_BASE}/debates/${debateId}`, {
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
  const res = await fetch(`${API_BASE}/debates`, {
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
  const res = await fetch(`${API_BASE}/debates/${debateId}/tts`, {
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
  // Provider-billed cost of this tutor reply, in USD.
  cost_usd: number
}

// Stream a tutor explanation via SSE; invokes onToken for each partial token
// and resolves with the final content and follow-up suggestions carried on the
// `done` event.
export async function streamTutor(
  debateId: string,
  body: { selection: string; turn_key: string; history: TutorMessage[]; model: string },
  onToken: (text: string) => void,
): Promise<TutorResult> {
  const res = await fetch(`${API_BASE}/debates/${debateId}/tutor`, {
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
    throw new ApiError(res.status, err?.detail ?? 'Stream failed')
  }
  // Backend currently responds with JSON; SSE will return when streaming
  // is re-introduced. The parser below stays in place for that day.
  if (!res.headers.get('content-type')?.includes('text/event-stream')) {
    const parsed = await res.json() as { content?: string; suggestions?: string[]; cost_usd?: number }
    return { content: parsed?.content ?? '', suggestions: parsed?.suggestions ?? [], cost_usd: parsed?.cost_usd ?? 0 }
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
          const parsed = JSON.parse(data) as { content?: string; suggestions?: string[]; cost_usd?: number }
          finalResult = { content: parsed?.content ?? '', suggestions: parsed?.suggestions ?? [], cost_usd: parsed?.cost_usd ?? 0 }
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

// streamTurnResult POSTs to a turn-producing endpoint (next or retry) with SSE
// requested, invoking onToken for each partial token and resolving with the
// final TurnResult carried on the `done` event. Throws ApiError on a non-OK
// response (so callers can read status, e.g. 402) and Error on an SSE `error`
// event. streamTurn and streamRetry differ only in path and body.
async function streamTurnResult(
  path: string,
  body: object,
  onToken: (text: string) => void,
): Promise<TurnResult> {
  const res = await fetch(`${API_BASE}${path}`, {
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
    throw new ApiError(res.status, err?.detail ?? 'Stream failed')
  }
  // Backend may respond with plain JSON instead of SSE; return it directly. The
  // parser below handles the event-stream case.
  if (!res.headers.get('content-type')?.includes('text/event-stream')) {
    return await res.json() as TurnResult
  }
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
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

      if (eventName === 'token') {
        try {
          const parsed = JSON.parse(data)
          const text = typeof parsed === 'string' ? parsed : parsed?.text ?? ''
          if (text) onToken(text)
        } catch {
          if (data) onToken(data)
        }
      } else if (eventName === 'done') {
        try { finalResult = JSON.parse(data) as TurnResult } catch { /* noop */ }
      } else if (eventName === 'error') {
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

// Stream a turn via SSE; invokes onToken for each partial token and resolves
// with the final TurnResult. model is the allowlisted model the turn runs on
// (required). interjection, when non-empty, is the owner's note
// steering this turn.
export function streamTurn(
  debateId: string,
  onToken: (text: string) => void,
  model: string,
  interjection?: string,
): Promise<TurnResult> {
  return streamTurnResult(`/debates/${debateId}/next`, { interjection: interjection ?? '', model }, onToken)
}

// Stream a retry of the debate's last completed step via SSE. stepKey is the
// step the caller expects to redo; the backend rejects anything that is not the
// current last record (409). model is the allowlisted model the re-roll runs on
// (required). The fresh generation replaces that step's record.
export function streamRetry(
  debateId: string,
  stepKey: string,
  onToken: (text: string) => void,
  model: string,
): Promise<TurnResult> {
  return streamTurnResult(`/debates/${debateId}/retry`, { step_key: stepKey, model }, onToken)
}
