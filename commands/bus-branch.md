---
description: Fork this session into a new window, both joined to a cbus channel
argument-hint: "[window|tab|tmux] [channel]"
allowed-tools: Bash(cbus:*), Bash(/Users/dev/.claude/bin/cc-branch.sh:*), Monitor, AskUserQuestion
---

Fork this conversation into a separate terminal **and** wire both sides onto a
`cbus` channel so parent and child can message each other live (instead of
writing a handoff doc and carrying it back).

The user passed: "$ARGUMENTS" — first word is the target (window | tab | tmux;
ask via AskUserQuestion if empty), optional second word is the channel name.

Do this in order:

1. **Pick the channel**: use the one the user passed, else the git repo's
   basename (`basename $(git rev-parse --show-toplevel)`, sanitized to
   `[A-Za-z0-9._-]`), else `global`.
2. **Join this (parent) session**: run `cbus join <channel>` — it is idempotent,
   auto-picks the alias (`main`, then `fork-N`), and prunes dead peers in the
   channel first. Note the parent alias it prints.
3. **Arm the parent's listener** with the **Monitor** tool, persistent:
   `cbus tail <channel>/<parent-alias>` — description
   `cbus:<channel>/<parent-alias>`. Skip if this session already has a cbus
   Monitor armed for this address. Arming *before* the fork means the parent is
   already listening when the child announces itself — no race.
4. **Fork with a bootstrap turn** — the canonical child prompt comes from the
   CLI so it can't drift from cbus behavior:

   `/Users/dev/.claude/bin/cc-branch.sh <target> --prompt "$(cbus bootstrap <channel> <parent-alias>)"`

5. Confirm in one line: channel, parent alias, and target. The child announces
   its own alias via the bus when it boots; the user can then
   `cbus send <channel>/<child-alias> "..."` (I'll do it when asked).

Note: the child resumes this session's transcript at boot, so it will see the
parent's live Monitor as a "no completion record" background-task note. This is
cosmetic and unavoidable (the transcript is read when the child starts, always
after the parent armed) — the bootstrap prompt already tells the child to ignore
it. Do not reorder the steps to try to suppress it.

Do nothing else.
