export type Ask = {
  id: string
  question: string
  from?: string
  options: string[]
  recommended?: string
  context?: string
  at: string
}

export type Resolved = Ask & { answer: string; resolvedAt: string }

export type Asks = { seq: number; open: Ask[]; resolved: Resolved[] }

declare module 'claude-code' {
  interface PluginState {
    'cbus-asks': { asks: Shaped<Asks> }
  }
}
