import type { Ask, Asks, Resolved } from '../types'

export const EMPTY: Asks = { seq: 0, open: [], resolved: [] }

const MAX_OPTIONS = 6
const KEEP_RESOLVED = 20

export type AskInput = {
  question?: unknown
  options?: unknown
  recommended?: unknown
  context?: unknown
}

const str = (v: unknown) => (typeof v === 'string' ? v.trim() : '')

// accepts q2, Q2, 2 or "q2:" so the model's spelling of an id never misses
export const normId = (v: unknown): string => {
  const m = /^\s*q?(\d+)\s*:?\s*$/i.exec(String(v ?? ''))
  return m ? `q${Number(m[1])}` : ''
}

export const addAsk = (
  s: Asks,
  input: AskInput,
  at: string,
): { state: Asks; ask: Ask } | { error: string } => {
  const question = str(input.question)
  if (!question) return { error: 'question is required' }
  const options = Array.isArray(input.options)
    ? input.options.map(str).filter(Boolean)
    : []
  if (options.length > MAX_OPTIONS)
    return { error: `at most ${MAX_OPTIONS} options; put the rest in the question` }
  const recommended = str(input.recommended)
  if (recommended && options.length > 0 && !options.includes(recommended))
    return { error: `recommended must be one of the options: ${options.join(', ')}` }
  const context = str(input.context)
  const seq = s.seq + 1
  const ask: Ask = {
    id: `q${seq}`,
    question,
    options,
    at,
    ...(recommended ? { recommended } : {}),
    ...(context ? { context } : {}),
  }
  return { state: { ...s, seq, open: [...s.open, ask] }, ask }
}

export const resolveAsk = (
  s: Asks,
  rawId: unknown,
  answer: string,
  at: string,
): { state: Asks; ask: Resolved } | { error: string } => {
  const id = normId(rawId)
  const found = s.open.find(a => a.id === id)
  if (!found) {
    const ids = s.open.map(a => a.id).join(', ') || 'none'
    return { error: `no open question ${String(rawId)}; open: ${ids}` }
  }
  const done: Resolved = { ...found, answer: answer.trim() || 'resolved', resolvedAt: at }
  return {
    state: {
      ...s,
      open: s.open.filter(a => a.id !== id),
      resolved: [done, ...s.resolved].slice(0, KEEP_RESOLVED),
    },
    ask: done,
  }
}

export const optionLabel = (ask: Ask, opt: string) =>
  opt === ask.recommended ? `${opt} (rec)` : opt

export const BAND_ROWS = 3

export const describe = (ask: Ask) => {
  const opts = ask.options.map(o => optionLabel(ask, o)).join(' / ')
  return `${ask.id}: ${ask.question}${opts ? ` [${opts}]` : ''}`
}

// what the model reads beside a user prompt while questions are open
export const openNote = (open: readonly Ask[]): string | null => {
  if (open.length === 0) return null
  return [
    'Open questions waiting on the user (cbus-asks band):',
    ...open.map(describe),
    'If this prompt answers any of them, act on the answer and call mcp__cbus-asks__resolve for each one answered. A bare "1 yes" or "q2 develop" refers to these ids.',
  ].join('\n')
}

// text a band button adds to the draft, joined to what the user already typed
export const draftPart = (draft: string, ask: Ask, opt: string) => {
  const sep = draft.trim() === '' ? '' : /[\s,;]$/.test(draft) ? '' : '; '
  return `${sep}${ask.id}: ${opt}`
}
