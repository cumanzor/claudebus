import { atom, read, update } from 'claude-code'
import type { Register } from 'claude-code'

import { compact, parseOffset, presenceOnly } from './frame'

// a longer reply, or one that ran a tool, says something the frame alone does not
const QUIET_MAX = 280
const KEEP = 200

const quietReplies = atom({ plugin: 'cbus-compact', key: 'quietReplies' } as const, [], { shape: 'quiet-1' })

export const register: Register = on => {
  let offsetMin = 0
  const presenceTurns = new Map<string, number>()

  on('session.start', async ($, e, next) => {
    const out = await $.process.run(['date', '+%z']).catch(() => null)
    if (out) offsetMin = parseOffset(String(out.stdout ?? ''))
    return next(e)
  })

  on('ui.render', { component: 'UserMessage' }, async ($, e, next) => {
    if (e.props.isExpanded) return next(e)
    const text = compact(e.props.text, offsetMin)
    if (text === null) return next(e)
    return next({ ...e, props: { ...e.props, text } })
  })

  on('turn.start', async ($, e, next) => {
    if (!e.agentId && presenceOnly(e.text)) presenceTurns.set(e.turnId, 0)
    return next(e)
  })

  on('tool.call', async ($, e, next) => {
    if (!e.agentId) for (const [id, n] of presenceTurns) presenceTurns.set(id, n + 1)
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    const tools = presenceTurns.get(e.turnId)
    presenceTurns.delete(e.turnId)
    const answer = e.answer.trim()
    if (tools === 0 && !e.isAborted && answer !== '' && answer.length <= QUIET_MAX)
      await update($, quietReplies, list => [...list.filter(t => t !== answer), answer].slice(-KEEP))
    return next(e)
  })

  on('ui.render', { component: 'AssistantMessage' }, async ($, e, next) => {
    if (e.props.isSummary) return next(e)
    const quiet = await read($, quietReplies)
    if (!quiet.includes(e.props.text.trim())) return next(e)
    const { Box } = $.ui.resolve(e)
    return <Box />
  })
}
