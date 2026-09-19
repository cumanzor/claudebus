---
description: Fork this session into a new window, both joined to a cbus channel
argument-hint: "[window|tab|tmux|pane] [channel] [--model m] [--name n]"
allowed-tools: Bash(cbus:*), AskUserQuestion
---

Fork this conversation into a separate terminal **and** wire both sides onto a
`cbus` channel so parent and child can message each other live (instead of
writing a handoff doc and carrying it back).

The user passed: "$ARGUMENTS" — first word is the target (window | tab | tmux |
pane; ask via AskUserQuestion ONLY if empty), optional second word is the channel name.

First connect this session with `cbus connect CHANNEL [ALIAS] --json` using
`/bus-join` guidance, so the parent already has a native listener. Then:

1. Run `cbus branch <target> [channel]` — one shot: joins this session to the
   channel (idempotent; channel auto-derives from the git repo name if omitted),
   reserves the child's alias, forks the conversation with the canonical
   bootstrap prompt, and prints BOTH addresses (parent + reserved child). The
   child's session title is its alias (picker + terminal title, and the tmux
   window name when the target is tmux). If the user
   mentions a model (e.g. "fork with sonnet"), append `--model <m>` — valid
   values today: sonnet, fable, claude-opus-4-8. "opus" is temporarily pinned
   to Opus 4.8: pass `claude-opus-4-8` verbatim, never bare `opus` (which now
   resolves to Opus 5). If the user names the child (e.g. "call
   it tester2"), append `--name <n>` — it becomes the child's alias AND title
   (alias charset: [A-Za-z0-9._-]); otherwise one is auto-picked (fork-N).
2. Preserve the parent's native connection. The child receives its own native
   connect instructions; no Monitor or tail loop is needed on either side.

Then confirm in one line: channel, parent alias, child alias, and target. The
child's alias is known up front (reserved), so `cbus send
<channel>/<child-alias> "..."` works as soon as its join presence event
arrives (I'll send when asked).

An inherited background-task note belongs to the parent's historical transcript.
Do not restart that listener in the child.

Do nothing else.
