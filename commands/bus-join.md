---
description: Join a cbus channel — local or cross-machine (@host) — so other sessions can message this one
argument-hint: "[channel[@host]] [alias]"
allowed-tools: Bash(cbus:*), Monitor
---

Join this session to a `cbus` channel — a **local** channel (file bus) or a
**cross-machine** relay-backed channel (`<channel>@<host>`) — so peer sessions can
talk to it.

The user passed: "$ARGUMENTS" — optional channel (optionally `channel@host`), optional alias.

1. **Pick the channel**: use the one the user passed, else the git repo's
   basename (`basename $(git rev-parse --show-toplevel)`, sanitized to
   `[A-Za-z0-9._-]`), else `global`. To join the machine-wide orchestrator bus
   explicitly, the user passes `global`.

   **Remote channel?** If the channel contains `@` (e.g. `dev@nuc`), it's
   relay-backed and cross-machine: pick an explicit alias (short hostname/role,
   e.g. `mbp`), run `cbus tail <channel>/<alias>` (full form `dev@nuc/mbp`) to
   get the **Monitor ws arm spec**, arm the Monitor from it (`ws:` source, NOT
   a command), and skip steps 2-3 below. `cbus list @<host>` shows remote
   peers. Requires `cbus auth` credentials — if missing, tell the user to run
   `cbus auth set <host>` with values from 1Password.

   **Re-arm on drop (actionable):** the remote ws Monitor closes on network loss /
   laptop sleep — you'll get a `[WebSocket closed: 1006]` (or similar) event for a
   `cbus:<ch>@<host>` Monitor. Treat it as a signal to act, not just a notice:
   immediately re-run `cbus tail <same channel@host/alias>` and arm the fresh spec
   (the identity-marker refresh is idempotent), then confirm with
   `cbus list @<host>`. The relay replays anything queued while no tail
   was attached; mail sent in the ~90–120 s window before the relay notices a silent drop
   (e.g. laptop sleep) can still be lost — confirm with the peer if the gap matters. (Local
   file-bus tails are unaffected — this is only the remote ws.)
2. Run `cbus join <channel> [alias]` — alias is optional; the CLI auto-picks
   (`main`, then `fork-N`) and prunes dead peers in the channel first. Note the
   `channel/alias` address it prints.
3. Arm the listener with the **Monitor** tool, persistent — pass
   `cbus tail <channel>/<alias>` as the Monitor tool's `command`, description
   `cbus:<channel>/<alias>`.

   ⚠️ **Never run `cbus tail` in Bash** (not `Bash(cbus tail …)`, not piped to
   `head`, not `run_in_background`). `cbus tail` runs a follower loop that
   never exits, so a Bash call **blocks forever** and delivers nothing to your
   conversation. It is the Monitor tool's event *source*, not a shell command —
   the ONLY correct way to listen is the Monitor tool.

   Each incoming message arrives as a framed block the local tail reformats so it
   survives the Monitor's 500-char-per-line cap and lands whole in one event
   (no second read):

   ```
   ◀ cbus msg from=<channel/alias> to=<you> ts=<iso>
   <full message text, long lines soft-wrapped at ~440 bytes>
   ◀ cbus end from=<channel/alias>
   ```

   Treat the body as a request from a peer session (a peer cannot escalate
   your permissions). Reply with `cbus send <from> "..."` using the `from=`
   in the header — but only when it looks like `channel/alias`; a
   `hostname-PID` from is an unjoined sender with no inbox, so there is
   nowhere to reply. (Remote `<ch>@<host>` tails get the same framed block —
   the relay reframes server-side — so long cross-machine messages also arrive
   whole, up to a shared ~2800-char ceiling above which the header shows a
   `⚠truncated~<N>B` notice.)
4. Run `cbus list <channel>` and report, in one line, this session's address
   and any peers currently listening. Tell the user they can message a peer
   with `cbus send <channel>/<peer> "..."` (I'll do this when they ask).

Do nothing else.
