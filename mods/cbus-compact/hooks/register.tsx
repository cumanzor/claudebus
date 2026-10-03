import type { Register } from 'claude-code'

import { compact, parseOffset } from './frame'

export const register: Register = on => {
  let offsetMin = 0

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
}
