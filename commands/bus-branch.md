---
description: Fork this session into a new window, both joined to a cbus channel
argument-hint: "[window|tab|tmux] [channel]"
allowed-tools: Bash(cbus:*), Monitor, AskUserQuestion
---

Fork this conversation into a separate terminal **and** wire both sides onto a
`cbus` channel so parent and child can message each other live (instead of
writing a handoff doc and carrying it back).

The user passed: "$ARGUMENTS" — first word is the target (window | tab | tmux;
ask via AskUserQuestion ONLY if empty), optional second word is the channel name.

Two steps, no more:

1. Run `cbus branch <target> [channel]` — one shot: joins this session to the
   channel (idempotent; channel auto-derives from the git repo name if omitted),
   forks the conversation with the canonical bootstrap prompt, and prints the
   parent `channel/alias`.
2. Arm the parent's listener with the **Monitor** tool, persistent:
   `cbus tail <channel>/<parent-alias>` — description
   `cbus:<channel>/<parent-alias>`. Skip if this session already has a cbus
   Monitor armed for this address.

Then confirm in one line: channel, parent alias, and target. The child
announces its own alias via the bus when it boots; the user can then
`cbus send <channel>/<child-alias> "..."` (I'll do it when asked).

Note: the child resumes this session's transcript at boot, so it will see the
parent's live Monitor as a "no completion record" background-task note. This is
cosmetic and unavoidable — the bootstrap prompt already tells the child to
ignore it. Do not reorder or add steps to try to suppress it.

Do nothing else.
