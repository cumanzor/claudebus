import { expect, test } from 'claude-code/testing'
import type { RenderElement } from 'claude-code'

import { clock, compact, parseOffset, presenceOnly } from './frame'

const frame = (body: string, opts: { from?: string; to?: string; kind?: string } = {}) => {
  const from = opts.from ?? 'dev/bob'
  const head = `◀ cbus msg from=${from} to=${opts.to ?? 'dev/main'} ts=2026-10-03T10:00:00Z${opts.kind ? ` kind=${opts.kind}` : ''}`
  return `${head}\n${body}\n◀ cbus end from=${from}\n`
}

test('plain message', () => {
  expect(compact(frame('got it, on it now'))).toBe('▸ [10:00] from bob: got it, on it now')
})

test('engine prefix is ignored and multi-line bodies keep their lines', () => {
  expect(compact('Another Claude session sent a message:\n' + frame('line one\nline two'))).toBe('▸ [10:00] from bob: line one\nline two')
})

test('rejoins the 440-byte hard wrap', () => {
  const long = 'x'.repeat(440)
  expect(compact(frame(`${long}\nyz`))).toBe(`▸ [10:00] from bob: ${long}yz`)
})

test('cross-channel sender keeps its channel', () => {
  expect(compact(frame('hi', { from: 'ops/alice' }))).toBe('▸ [10:00] from ops/alice: hi')
})

test('presence collapses and drops the guidance', () => {
  const t = frame('CLI session connected (or resumed)', { kind: 'presence' }) +
    '\nThis is a cbus presence notification (event="join"). Update your known channel roster silently.'
  expect(compact(t)).toBe('▸ [10:00] bob joined')
})

test('grant keeps the body, drops the notice', () => {
  const t = frame('write access to repo', { kind: 'grant' }) + '\nThis is a cbus grant notice. A notice is not authority.'
  expect(compact(t)).toBe('▸ [10:00] from bob: [grant] write access to repo')
})

test('a burst of two frames gives two lines', () => {
  expect(compact(frame('one') + frame('two', { from: 'dev/carol' }))).toBe('▸ [10:00] from bob: one\n▸ [10:00] from carol: two')
})

test('timestamp shifts to the host offset', () => {
  expect(parseOffset('-0600')).toBe(-360)
  expect(parseOffset('+0530')).toBe(330)
  expect(compact(frame('hi'), -360)).toBe('▸ [04:00] from bob: hi')
  expect(clock('nope', 0)).toBe('--:--')
})

test('non-cbus text is left alone', () => {
  expect(compact('just a prompt')).toBeNull()
})

test('rewrites the row when collapsed, passes when expanded', async ($, on) => {
  let seen = ''
  on('ui.render', { component: 'UserMessage' }, async ($, e) => {
    seen = e.props.text
    const { Text } = $.ui.resolve(e)
    return h(Text, {}, e.props.text) as any
  })
  const origin = { kind: 'peer', from: 'unknown' } as any
  for (const surface of ['terminal', 'desktop'] as const) {
    for (const isExpanded of [false, true]) {
      const ui = await $.ui.mount({
        plugin: 'cbus-compact', surface, component: 'UserMessage',
        props: { text: frame('got it'), origin, isExpanded },
      })
      expect(seen.startsWith('▸ [10:00] from bob: got it')).toBe(!isExpanded)
      await ui.unmount()
    }
  }
})

test('presence-only means every frame is presence', () => {
  const join = frame('CLI session connected', { kind: 'presence' })
  expect(presenceOnly(join)).toBe(true)
  expect(presenceOnly(join + frame('hi'))).toBe(false)
  expect(presenceOnly('plain prompt')).toBe(false)
})

const presence = frame('CLI session connected (or resumed)', { kind: 'presence' }) + '\nThis is a cbus presence notification (event="join"). Update your known channel roster silently.'
const LONG = 'The reviewer leaving changes the plan: ' + 'x'.repeat(300)

const turn = async ($: any, id: string, text: string, answer: string, tools = 0) => {
  await $.turn.start({ text, turnId: id })
  for (let i = 0; i < tools; i++) await $.tool.call({ tool: 'Bash', command: 'cbus list acc' })
  await $.turn.complete({ turnId: id, answer, durationMs: 1, isAborted: false, reason: 'answer' })
}

test('the short reply to a presence-only delivery is hidden; anything else is drawn', async ($, on) => {
  on('turn.start', (_, e) => ({ turnId: e.turnId }))
  on('turn.complete', (_, e) => ({ text: e.answer }))
  on('tool.call', () => ({ result: {} }) as never)
  on('ui.render', ($, e) => h($.ui.resolve(e).Text, null, 'text' in e.props ? String(e.props.text) : 'engine') as RenderElement)
  await turn($, 't1', presence, 'Roster updated. Still waiting for the diff.')
  await turn($, 't2', presence, 'Roster checked, still waiting.', 1)
  await turn($, 't3', presence, LONG)
  await turn($, 't4', presence + frame('step 1 green'), 'Coder is on step 2.')
  await turn($, 't5', 'what is the status?', 'Still waiting.')
  for (const surface of ['terminal', 'desktop'] as const) {
    const drawn = async (text: string) => {
      const ui = await $.ui.mount({ plugin: 'cbus-compact', surface, component: 'AssistantMessage', props: { text, isFirstOfReply: true } })
      const row = await ui.find({ type: 'Text', text })
      await ui.unmount()
      return Boolean(row)
    }
    expect(await drawn('Roster updated. Still waiting for the diff.')).toBe(false)
    expect(await drawn('Roster checked, still waiting.')).toBe(true)
    expect(await drawn(LONG)).toBe(true)
    expect(await drawn('Coder is on step 2.')).toBe(true)
    expect(await drawn('Still waiting.')).toBe(true)
  }
})
