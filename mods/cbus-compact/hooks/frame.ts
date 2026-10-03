const HEAD = /◀ cbus msg from=(\S+) to=(\S+) ts=(\S*)(?: kind=(\S+))?[^\n]*\n/g
const END = /\n?◀ cbus end from=\S+/
const EVENT = /\(event="([^"]*)"\)/
const WRAP = 440

const PRESENCE: Record<string, string> = { join: 'joined', leave: 'left', departed: 'departed', rename: 'renamed' }

const enc = new TextEncoder()

const channel = (addr: string) => addr.split('/')[0] ?? ''
const alias = (addr: string) => addr.split('/').slice(1).join('/') || addr

// undo cbus's 440-byte hard wrap: a full-width line was split mid-segment
const unwrap = (body: string) => {
  const lines = body.split('\n')
  let out = ''
  lines.forEach((line, i) => {
    out += line
    if (i < lines.length - 1) out += enc.encode(line).length === WRAP ? '' : '\n'
  })
  return out
}

const pad = (n: number) => String(n).padStart(2, '0')

// the hook sandbox has no reliable local zone, so the offset comes from the host
export const clock = (ts: string, offsetMin: number): string => {
  const ms = Date.parse(ts)
  if (Number.isNaN(ms)) return '--:--'
  const d = new Date(ms + offsetMin * 60_000)
  return `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`
}

// `date +%z` output, e.g. -0600
export const parseOffset = (z: string): number => {
  const m = /([+-])(\d{2})(\d{2})/.exec(z)
  if (!m) return 0
  return (m[1] === '-' ? -1 : 1) * (Number(m[2]) * 60 + Number(m[3]))
}

export const compact = (text: string, offsetMin = 0): string | null => {
  const heads = [...text.matchAll(HEAD)]
  if (heads.length === 0) return null
  return heads
    .map((m, i) => {
      const [, from = '?', to = '', ts = '', kind] = m
      const start = (m.index ?? 0) + m[0].length
      const stop = heads[i + 1]?.index ?? text.length
      const rest = text.slice(start, stop)
      const end = END.exec(rest)
      const body = unwrap(end ? rest.slice(0, end.index) : rest).trim()
      const tail = end ? rest.slice(end.index + end[0].length) : ''
      const at = clock(ts, offsetMin)
      const who = channel(from) === channel(to) ? alias(from) : from
      if (kind === 'presence') {
        const ev = EVENT.exec(tail)?.[1] ?? ''
        return `▸ [${at}] ${who} ${PRESENCE[ev] ?? (ev || 'presence')}`
      }
      return `▸ [${at}] from ${who}: ${kind === 'grant' ? '[grant] ' : ''}${body}`
    })
    .join('\n')
}
