import { expect, test } from 'claude-code/testing'
import type { RenderElement } from 'claude-code'
import type { TestBody } from 'claude-code/testing'

import { addAsk, draftPart, EMPTY, normId, openNote, resolveAsk } from './asks'

const AT = '2026-10-30T22:00:00Z'

const two = () => {
  const a = addAsk(EMPTY, { question: 'Is the 11-row ledger right?', options: ['yes', 'no'] }, AT)
  if ('error' in a) throw new Error(a.error)
  const b = addAsk(a.state, { question: 'MR target?', options: ['develop', 'release/2.15.0'], recommended: 'develop' }, AT)
  if ('error' in b) throw new Error(b.error)
  return b.state
}

test('ids count up and survive a resolve', () => {
  const s = two()
  expect(s.open.map(a => a.id).join(',')).toBe('q1,q2')
  const r = resolveAsk(s, 'q1', 'yes', AT)
  if ('error' in r) throw new Error(r.error)
  const c = addAsk(r.state, { question: 'next' }, AT)
  if ('error' in c) throw new Error(c.error)
  expect(c.ask.id).toBe('q3')
  expect(c.state.resolved[0]?.answer).toBe('yes')
})

test('id spellings the model or user may use', () => {
  expect(normId('q2')).toBe('q2')
  expect(normId('Q2')).toBe('q2')
  expect(normId(2)).toBe('q2')
  expect(normId(' 2: ')).toBe('q2')
  expect(normId('ledger')).toBe('')
})

test('input is checked', () => {
  expect('error' in addAsk(EMPTY, { question: '  ' }, AT)).toBe(true)
  expect('error' in addAsk(EMPTY, { question: 'x', options: ['a', 'b'], recommended: 'c' }, AT)).toBe(true)
  expect('error' in addAsk(EMPTY, { question: 'x', options: ['1', '2', '3', '4', '5', '6', '7'] }, AT)).toBe(true)
  const r = resolveAsk(two(), 'q9', 'x', AT)
  expect('error' in r && r.error).toBe('no open question q9; open: q1, q2')
})

test('the note names every open question with its options', () => {
  expect(openNote([])).toBe(null)
  const note = openNote(two().open) ?? ''
  expect(note).toContain('q1: Is the 11-row ledger right? [yes / no]')
  expect(note).toContain('q2: MR target? [develop (rec) / release/2.15.0]')
  expect(note).toContain('mcp__cbus-asks__resolve')
})

test('a button press joins what the user already typed', () => {
  const [q1] = two().open
  if (!q1) throw new Error('no q1')
  expect(draftPart('', q1, 'yes')).toBe('q1: yes')
  expect(draftPart('q2: develop', q1, 'yes')).toBe('; q1: yes')
  expect(draftPart('q2: develop, ', q1, 'yes')).toBe('q1: yes')
})

const BAND = {
  plugin: 'cbus-asks',
  component: 'AbovePrompt',
  surface: 'terminal',
  props: { hasSurvey: false, isWorking: false, maxRows: 20, bodyColumns: 100, scroll: { offset: 0, bodyRows: 19 }, view: {} },
} as const

const PANE = {
  plugin: 'cbus-asks',
  component: 'Pane',
  requestId: 'cbus-asks',
  surface: 'terminal',
  props: { title: 'Questions for you', isFocused: false, bodyColumns: 80, placement: 'dock', scroll: { offset: 0, bodyRows: 20 }, view: {} },
} as const

const boot = async (...[$, on]: Parameters<TestBody>) => {
  on('tool.register', (_, e) => ({ value: { tool: `mcp__cbus-asks__${e.name}` } }))
  on('command.register', (_, e) => ({ value: { command: e.name } }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('clock.now', () => ({ value: Date.parse('2026-10-30T22:00:00Z') }))
  on('ui.render', ($, e) => h($.ui.resolve(e).Text, null, 'engine') as RenderElement)
  await $.session.start({ cwd: '/', surface: 'terminal', isInteractive: true })
}

test('the band shows the question, the pane its buttons, resolve clears both', async ($, on) => {
  await boot($, on)
  const asked = await $.tool.call({ tool: 'mcp__cbus-asks__ask', question: 'MR target?', options: ['develop', 'release'], recommended: 'develop', context: 'release is frozen' })
  expect(String(asked.result)).toContain('q1 is in the band')

  const band = await $.ui.mount(BAND)
  expect((await band.find({ type: 'Text', text: 'MR target?' }))?.text).toBe('MR target?')
  expect(await band.find({ type: 'Button', key: 'q1:0' })).toBeFalsy()
  await band.unmount()

  const pane = await $.ui.mount(PANE)
  expect(await pane.find({ type: 'Button', key: 'q1:0' })).toBeTruthy()
  expect((await pane.find({ type: 'Text', text: 'release is frozen' }))?.text).toBe('release is frozen')
  await pane.unmount()

  const done = await $.tool.call({ tool: 'mcp__cbus-asks__resolve', id: '1', answer: 'develop' })
  expect(String(done.result)).toBe('q1 closed: develop')
  const empty = await $.ui.mount(BAND)
  expect(await empty.find({ type: 'Text', text: 'MR target?' })).toBeFalsy()
  await empty.unmount()
})

test('the band stops at three questions and counts the rest', async ($, on) => {
  await boot($, on)
  for (const n of [1, 2, 3, 4, 5])
    await $.tool.call({ tool: 'mcp__cbus-asks__ask', question: `Question ${n}?` })

  const ui = await $.ui.mount(BAND)
  expect((await ui.find({ type: 'Text', text: 'Question 3?' }))?.text).toBe('Question 3?')
  expect(await ui.find({ type: 'Text', text: 'Question 4?' })).toBeFalsy()
  expect((await ui.find({ type: 'Text', text: /more/ }))?.text).toBe('+2 more in /asks')
  await ui.unmount()
})
