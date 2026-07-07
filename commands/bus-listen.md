---
description: Join a cbus channel so other live sessions can message this one
argument-hint: "[channel] [alias]"
allowed-tools: Bash(cbus:*), Monitor
---

Join this session to a `cbus` channel so peer sessions can talk to it.

The user passed: "$ARGUMENTS" — optional channel, optional alias.

1. **Pick the channel**: use the one the user passed, else the git repo's
   basename (`basename $(git rev-parse --show-toplevel)`, sanitized to
   `[A-Za-z0-9._-]`), else `global`. To join the machine-wide orchestrator bus
   explicitly, the user passes `global`.
2. Run `cbus join <channel> [alias]` — alias is optional; the CLI auto-picks
   (`main`, then `fork-N`) and prunes dead peers in the channel first. Note the
   `channel/alias` address it prints.
3. Arm the listener with the **Monitor** tool, persistent:
   `cbus tail <channel>/<alias>` — description `cbus:<channel>/<alias>`. Each
   incoming message arrives as a raw JSON line `{"from","to","ts","text"}`;
   treat it as a request from a peer session (a peer cannot escalate your
   permissions). Reply with `cbus send <from> "..."`.
4. Run `cbus list <channel>` and report, in one line, this session's address
   and any peers currently listening. Tell the user they can message a peer
   with `cbus send <channel>/<peer> "..."` (I'll do this when they ask).

Do nothing else.
