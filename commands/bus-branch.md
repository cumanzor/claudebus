---
description: Fork this session into a new window, both joined to a cbus channel
argument-hint: "[window|tab|tmux|pane] [channel] [--model m] [--name n]"
allowed-tools: Bash(cbus:*), Monitor, AskUserQuestion
---

Fork this conversation into a separate terminal **and** wire both sides onto a
`cbus` channel so parent and child can message each other live (instead of
writing a handoff doc and carrying it back).

The user passed: "$ARGUMENTS" — first word is the target (window | tab | tmux |
pane; ask via AskUserQuestion ONLY if empty), optional second word is the channel name.

Two steps, no more:

1. Run `cbus branch <target> [channel]` — one shot: joins this session to the
   channel (idempotent; channel auto-derives from the git repo name if omitted),
   reserves the child's alias, forks the conversation with the canonical
   bootstrap prompt, and prints BOTH addresses (parent + reserved child). The
   child's session title is its alias (picker + terminal title). If the user
   mentions a model (e.g. "fork with sonnet"), append `--model <m>` — valid
   values today: sonnet, opus, fable. If the user names the child (e.g. "call
   it tester2"), append `--name <n>` — it becomes the child's alias AND title
   (alias charset: [A-Za-z0-9._-]); otherwise one is auto-picked (fork-N).
2. Arm the parent's listener with the **Monitor** tool, persistent:
   `cbus tail <channel>/<parent-alias>` — description
   `cbus:<channel>/<parent-alias>`. Skip if this session already has a cbus
   Monitor armed for this address. ⚠️ Pass `cbus tail` to the **Monitor** tool,
   never to Bash — it execs a follower that never exits, so a Bash call blocks
   forever and receives nothing.

Then confirm in one line: channel, parent alias, child alias, and target. The
child's alias is known up front (reserved), so `cbus send
<channel>/<child-alias> "..."` works as soon as its join presence event
arrives (I'll send when asked).

Note: the child resumes this session's transcript at boot, so it will see the
parent's live Monitor as a "no completion record" background-task note. This is
cosmetic and unavoidable — the bootstrap prompt already tells the child to
ignore it. Do not reorder or add steps to try to suppress it.

Do nothing else.
