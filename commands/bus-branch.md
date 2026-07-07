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

Do this in order — the ordering matters, see step 3:

1. **Pick the channel**: use the one the user passed, else the git repo's
   basename (`basename $(git rev-parse --show-toplevel)`, sanitized to
   `[A-Za-z0-9._-]`), else `global`.
2. **Join this (parent) session**: run `cbus join <channel>` — it is idempotent,
   auto-picks the alias (`main`, then `fork-N`), and prunes dead peers in the
   channel first. Note the parent alias it prints. Do **not** arm the Monitor
   yet.
3. **Fork with a bootstrap turn** — fork *before* arming the parent's Monitor:
   a live Monitor task in the transcript gets copied into the child's snapshot
   and shows up there as a stale "no completion record" background task.

   `/Users/dev/.claude/bin/cc-branch.sh <target> --prompt "You are a forked Claude Code session on the cbus message bus. Run: cbus join <channel>  (it auto-picks your alias — note it). Then arm the Monitor tool (persistent) on 'cbus tail <channel>/<your-alias>', description 'cbus:<channel>/<your-alias>'. Your parent is '<channel>/<parent-alias>' — announce yourself with: cbus send <channel>/<parent-alias> \"joined as <your-alias>\". When you finish your task, cbus send the parent a short result summary instead of writing a handoff doc. Confirm you have joined in one line, then wait for instructions."`

4. **Now arm the parent's listener** with the **Monitor** tool, persistent:
   `cbus tail <channel>/<parent-alias>` — description
   `cbus:<channel>/<parent-alias>`. Skip if this session already has a cbus
   Monitor armed for this channel.
5. Confirm in one line: channel, parent alias, and target. The child announces
   its own alias via the bus when it boots; the user can then
   `cbus send <channel>/<child-alias> "..."` (I'll do it when asked).

If this session already had a cbus Monitor armed before the fork, the child
will show one harmless "no completion record" note for it — say so and move on.

Do nothing else.
