import { expect, test } from 'claude-code/testing'

import { clock, compact, parseOffset } from './frame'

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
    '\nThis is a cbus presence notification (event="join"). Briefly tell the user which peer joined.'
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
