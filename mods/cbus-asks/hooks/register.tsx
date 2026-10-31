import { atom, read, update } from 'claude-code'
import type { Register } from 'claude-code'

import type { Asks } from '../types'
import type { AskInput } from './asks'
import type { Ask } from '../types'
import { addAsk, BAND_ROWS, draftPart, EMPTY, label, normId, openNote, optionLabel, paneHotkeys, parseAnswers, resolveAsk, statusLine } from './asks'

const PANE = 'cbus-asks'

// bump the tag when Ask's fields change: a reload keeps what the old code stored
const asks = atom({ plugin: 'cbus-asks', key: 'asks' } as const, EMPTY, { shape: 'asks-1' })

const now = async ($: { clock: { now: () => Promise<number> } }) =>
  new Date(await $.clock.now()).toISOString()

// runs a transition under update's retry and hands back the last attempt's outcome
const apply = async <T extends object>(
  $: Parameters<typeof read>[0] & { ui: { status: (text: string | undefined) => void } },
  step: (s: Asks) => ({ state: Asks } & T) | { error: string },
): Promise<T | { error: string }> => {
  const box: { out: ({ state: Asks } & T) | { error: string } } = { out: { error: 'not run' } }
  await update($, asks, s => {
    box.out = step(s)
    return 'error' in box.out ? s : box.out.state
  })
  if (!('error' in box.out)) $.ui.status(statusLine(box.out.state.open))
  return box.out
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await $.tool.register({
      name: 'ask',
      description:
        'Put a question you are waiting on the user for in a band above their prompt, where it stays until resolved. ' +
        'Use it for every decision or approval you need from the user instead of leaving it in a reply, where peer traffic and narration bury it. ' +
        'Write the question so it stands alone: name the thing being decided and the choices, as if the user read nothing else you wrote. ' +
        'Never write "my question about X is still open"; the band already shows it. ' +
        'If your launch prompt names a coordinator, do not call this tool: send the question to the coordinator on the bus instead. A coordinator carrying a peer\'s question sets from to that seat. Returns the id (q1, q2, ...).',
      inputSchema: {
        type: 'object',
        properties: {
          question: { type: 'string', description: 'One or two sentences that make sense with no other context; the band shows it whole.' },
          options: { type: 'array', items: { type: 'string' }, maxItems: 6, description: 'Short answer labels, drawn as buttons in the /asks pane. Leave out for an open question.' },
          recommended: { type: 'string', description: 'The option you would pick; must be one of options.' },
          context: { type: 'string', description: 'One or two lines on why it matters, shown in the /asks pane.' },
          from: { type: 'string', description: 'The seat that asked, as channel/alias, when you carry a peer\'s question.' },
        },
        required: ['question'],
      },
      isDeferred: false,
    })
    await $.tool.register({
      name: 'resolve',
      description:
        'Close an open question from the ask tool once the user answers it, or withdraw it when it no longer applies (answer "withdrawn: <why>").',
      inputSchema: {
        type: 'object',
        properties: {
          id: { type: 'string', description: 'The question id, e.g. q2.' },
          answer: { type: 'string', description: 'What the user decided, in a few words.' },
        },
        required: ['id', 'answer'],
      },
      isDeferred: false,
    })
    await $.tool.register({
      name: 'list',
      description: 'List the questions still open in the ask band, with their ids.',
      isDeferred: true,
    })
    await $.command.register({
      name: 'asks',
      description: 'Open the questions pane with answer buttons; "/asks drop q2" closes a question, "/asks clear" all of them, "/asks close" the pane',
      argumentHint: '[drop <id> | clear | close]',
    })
    $.ui.status(statusLine((await read($, asks)).open))
    return next(e)
  })

  on('tool.call', { tool: 'mcp__cbus-asks__ask' }, async ($, e) => {
    const at = await now($)
    const out = await apply($, s => addAsk(s, e as AskInput, at))
    if ('error' in out) return { deny: out.error }
    return { result: `${out.ask.id} is in the band above the user's prompt. Do not restate it in your reply.` }
  })

  on('tool.call', { tool: 'mcp__cbus-asks__resolve' }, async ($, e) => {
    const { id, answer } = e as { id?: unknown; answer?: unknown }
    const at = await now($)
    const out = await apply($, s => resolveAsk(s, id, String(answer ?? ''), at))
    if ('error' in out) return { deny: out.error }
    return { result: `${out.ask.id} closed: ${out.ask.answer}` }
  })

  on('tool.call', { tool: 'mcp__cbus-asks__list' }, async $ => {
    const { open } = await read($, asks)
    return { result: openNote(open) ?? 'No open questions.' }
  })

  on('prompt.submit', async ($, e, next) => {
    if (e.isMeta || e.command) return next(e)
    const at = await now($)
    const closed: string[] = []
    for (const { id, answer } of parseAnswers(e.text)) {
      const out = await apply($, s => resolveAsk(s, id, answer, at))
      if (!('error' in out)) closed.push(`${out.ask.id}: ${out.ask.answer}`)
    }
    const notes = [...(e.context ?? [])]
    if (closed.length > 0)
      notes.push(`The user answered these in this prompt and cbus-asks closed them already; act on the answers, no resolve call needed:\n${closed.join('\n')}`)
    const note = openNote((await read($, asks)).open)
    if (note) notes.push(note)
    return notes.length === (e.context ?? []).length ? next(e) : next({ ...e, context: notes })
  })

  on('command.run', { command: 'asks' }, async ($, e) => {
    const [verb, rawId] = e.args.trim().split(/\s+/)
    if (verb === 'drop') {
      if (!normId(rawId)) return { text: 'usage: /asks drop <id>' }
      const at = await now($)
      const out = await apply($, s => resolveAsk(s, rawId, 'dropped by the user', at))
      return { text: 'error' in out ? out.error : `${out.ask.id} dropped.` }
    }
    if (verb === 'clear') {
      const at = await now($)
      const ids = (await read($, asks)).open.map(a => a.id)
      for (const id of ids) await apply($, s => resolveAsk(s, id, 'cleared by the user', at))
      return { text: ids.length ? `Cleared ${ids.join(', ')}.` : 'Nothing to clear.' }
    }
    if (verb === 'close') {
      await $.ui.close({ id: PANE })
      return { text: 'Questions pane closed.' }
    }
    await $.ui.open({ id: PANE, title: 'Questions for you', focus: true, closeOnEscape: true })
    return { text: 'Questions pane opened. Number keys pick an answer, Esc closes.' }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey) return next(e)
    const { open } = await read($, asks)
    if (open.length === 0) return next(e)
    const { Box, Text } = $.ui.resolve(e)
    const shown = open.slice(0, BAND_ROWS)
    return (
      <Box flexDirection="column">
        <Text dimColor>Waiting on you ({open.length}), /asks to answer</Text>
        {shown.map(ask => (
          <Box key={ask.id} flexDirection="row" columnGap={1}>
            <Box flexShrink={0}>
              <Text bold>{label(ask)}</Text>
            </Box>
            <Text>{ask.question}</Text>
          </Box>
        ))}
        {open.length > shown.length && <Text dimColor>+{open.length - shown.length} more in /asks</Text>}
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    const { open, resolved } = await read($, asks)
    const pick = (ask: Ask, opt: string) => async () => {
      const { text } = await $.prompt.read()
      await $.prompt.fill({ text: draftPart(text, ask, opt), mode: 'append' })
    }
    const hotkeys = paneHotkeys(open)
    return (
      <Box flexDirection="column" rowGap={1}>
        {open.length === 0 && <Text dimColor>Nothing is waiting on you.</Text>}
        {open.map(ask => (
          <Box key={ask.id} flexDirection="column">
            <Box flexDirection="row" columnGap={1}>
              <Box flexShrink={0}>
                <Text bold>{label(ask)}</Text>
              </Box>
              <Text>{ask.question}</Text>
            </Box>
            {ask.context && <Text dimColor>{ask.context}</Text>}
            {ask.options.length > 0 && (
              <Box flexDirection="row" flexWrap="wrap" columnGap={1}>
                {ask.options.map((opt, i) => {
                  const key = hotkeys.get(`${ask.id}:${i}`)
                  return (
                    <Button
                      key={`${ask.id}:${i}`}
                      label={optionLabel(ask, opt)}
                      onPress={pick(ask, opt)}
                      {...(key ? { hotkey: key, plain: true } : {})}
                    />
                  )
                })}
              </Box>
            )}
          </Box>
        ))}
        {resolved.length > 0 && (
          <Box flexDirection="column">
            <Text dimColor>Answered</Text>
            {resolved.slice(0, 8).map(r => (
              <Text dimColor>
                {r.id} {r.question} → {r.answer}
              </Text>
            ))}
          </Box>
        )}
        <Text dimColor>Number keys add an answer to your draft; Esc closes.</Text>
      </Box>
    )
  })
}
