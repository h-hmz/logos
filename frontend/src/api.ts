// ── Types ──────────────────────────────────────────────────────────────────────

export interface Model {
  id: string
  name: string
}

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
  model: string
  max_words: number
}

// ── API ────────────────────────────────────────────────────────────────────────

export async function fetchModels(): Promise<Model[]> {
  const res = await fetch('/models')
  if (!res.ok) throw new Error('Failed to load models')
  return res.json()
}

export async function fetchPhilosophers(): Promise<Philosopher[]> {
  const res = await fetch('/philosophers')
  if (!res.ok) throw new Error('Failed to load philosophers')
  return res.json()
}

export async function createDebate(params: CreateDebateParams): Promise<DebateSession> {
  const res = await fetch('/debates', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(params),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: 'Unknown error' }))
    throw new Error(err.detail ?? 'Failed to create debate')
  }
  return res.json()
}

export async function suggestTopics(debaterA: string, debaterB: string): Promise<string[]> {
  const res = await fetch('/suggest-topic', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ debater_a: debaterA, debater_b: debaterB }),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: 'Unknown error' }))
    throw new Error(err.detail ?? 'Failed to suggest topics')
  }
  const data = await res.json()
  return (data.topics ?? []) as string[]
}

export async function nextTurn(debateId: string): Promise<TurnResult> {
  const res = await fetch(`/debates/${debateId}/next`, { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: 'Unknown error' }))
    throw new Error(err.detail ?? 'Failed to advance debate')
  }
  return res.json()
}

// Stream a turn via SSE; invokes onToken for each partial token and resolves with the
// final TurnResult carried on the `done` event. Throws on `error` events.
export function streamTurn(
  debateId: string,
  onToken: (text: string) => void
): Promise<TurnResult> {
  return new Promise(async (resolve, reject) => {
    try {
      const res = await fetch(`/debates/${debateId}/next`, {
        method: 'POST',
        headers: { Accept: 'text/event-stream' },
      })
      if (!res.ok || !res.body) {
        const err = await res.json?.().catch(() => ({ detail: 'Stream failed' }))
        reject(new Error(err?.detail ?? 'Stream failed'))
        return
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

      if (errorMsg) { reject(new Error(errorMsg)); return }
      if (finalResult) { resolve(finalResult); return }
      reject(new Error('Stream ended without a result'))
    } catch (e) {
      reject(e)
    }
  })
}
