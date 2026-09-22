---
description: Rename this session's legacy cbus alias and re-arm its Monitor on the new address
argument-hint: "<new-alias> [channel]"
allowed-tools: Bash(cbus:*), Monitor, TaskStop
---

Rename this session's local `cbus` alias so peers can track it by a meaningful
name (e.g. `fork-3` → `discovery`) instead of an auto-picked one.

The user passed: "$ARGUMENTS" — first word is the new alias (required), optional
second word is the channel (needed only if this session joined more than one).

Native managed aliases cannot currently be renamed in place. The CLI refuses this
case; report that refusal and preserve the connection and pending mail. The steps
below apply only to legacy join/Monitor peers.

1. Run `cbus rename <new-alias> [channel]`. It `mv`s this session's peer dir and
   rewrites `meta.alias`, printing the new `channel/alias` address. It refuses if
   the name is taken by a live listener, if the session isn't joined, or if the
   session is in multiple channels and no channel was given — surface that error
   and stop.
2. The old `cbus tail` is now stale (it follows the old inbox path). **Re-arm the
   listener**: stop the existing cbus Monitor for this session (TaskStop on the
   task whose description was `cbus:<channel>/<old-alias>`), then arm the Monitor
   tool on `cbus tail <channel>/<new-alias>` — description
   `cbus:<channel>/<new-alias>`. It is not persistent: it expires at its
   `timeout_ms` (max 30 min) and must be re-armed; see
   `docs/claude-monitor-stopgap.md` for the opt-in workaround. ⚠️ `cbus tail`
   goes to the **Monitor** tool, never Bash — it blocks forever in a shell (the
   follower never exits).
3. Report the new address in one line. If you want the Claude Code session title
   to match, note that you (the user) can set it with `/rename <new-alias>` — the
   TUI title can't be set programmatically.

Do nothing else.
