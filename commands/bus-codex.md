---
description: Bring a Codex CLI session onto a cbus channel as a peer, fresh or resumed
argument-hint: "[channel] [--alias a] [resume <session-id>|resume --last]"
allowed-tools: Bash(cbus:*), Monitor, AskUserQuestion
---

Put a **Codex CLI** session on a `cbus` channel as a real peer — its own alias,
its own listener, bus messages delivered into its thread as turns — and join
THIS session to the same channel first, so the two can message each other
immediately. A codex peer never runs `cbus tail`; the bridge listens for it.

The user passed: "$ARGUMENTS" — optional channel (a local name; codex peers are
local-only, there is no `@host` form here), optional `--alias <a>` (defaults to
`codex`; give it a real seat name like `advisor` when the user names a role), and
optionally a **resume** form (below). Omitted channel: derive it the way
`/bus-spawn` does — this session's own channel (the channel half of `cbus
whoami`, which exits 1 when not joined), else the git toplevel basename, else
`global`.

⚠️ **Never run `cbus codex` as a plain Bash call.** It launches an interactive
TUI on the caller's terminal and blocks until that TUI exits. In a tool call the
TUI gets no terminal, dies on stdin EOF, and the wrapper then tears itself down
without ever joining — you get no peer and a confusing error. It goes in a
window of its own (step 3).

## Fresh or resumed

- **Fresh session**: pass no resume args.
- **`resume <session-id>`**: a codex session UUID. The peer joins UNDER that id
  and arrives carrying that session's history.
- **`resume --last`**: the most recent codex session **in the launch window's
  cwd**, which is why step 3 pins the directory.
- **`resume`** alone opens codex's session picker in the window and waits for a
  human to choose (five minute window). Only use it if the user is watching.
- **`--thread <id>`** pins the thread when resuming by session NAME rather than
  id, since a name is not a thread id.

Where an id comes from: a past codex peer's own registration. `cbus list
<channel> --json` gives each peer a `sessionId`, and for a codex peer that IS
its codex session id — so a dead codex peer is brought back, history intact, by
resuming its own recorded id.

## Steps

1. **Join this side first** — skip steps 1 and 2 if this session already has a
   cbus Monitor armed for this channel. Run `cbus join <channel>` (idempotent)
   and note the `channel/alias` it prints.
2. **Arm this session's listener** with the **Monitor** tool, persistent:
   command `cbus tail <channel>/<alias>`, description `cbus:<channel>/<alias>`.
   ⚠️ Never run a local `cbus tail` in Bash — it execs a follower that never
   exits.
3. **Launch the codex peer in a window of its own.** With tmux available, do it
   yourself — `-c` pins the cwd, which decides what `resume --last` resolves to
   and where the peer's tools run:

   ```sh
   tmux new-window -c "$PWD" 'cbus codex --channel <ch> --alias <al> [resume <id>|resume --last]'
   ```

   Outside tmux, use `tmux new-session -d -s <name> -c "$PWD" '<same command>'`.
   With no tmux at all, do NOT try to background it: hand the user the exact
   command to run in a new terminal (they can prefix it with `!` to run it in
   this session's terminal) and wait for them.
4. **Verify** with `cbus list <channel>`: the alias must read `listen`. It takes
   a few seconds (app-server up, TUI attached, thread rendezvous, bridge armed).
   `off` after that means the launch failed — read the window.

Then confirm in one line: channel, this session's address, the codex peer's
alias, and whether it is fresh or resumed from which id.

## Two traps

- **Do not kill the window or `pkill` the wrapper** to stop a codex peer. The
  app-server is a child that only dies through the wrapper's own teardown; kill
  the wrapper abruptly and it orphans, keeps that thread's writer lock, and
  every later resume of that session is refused with "already has an active
  writer". Quit the TUI instead.
- **Never tell a codex peer to arm its own listener.** Its profile forbids `cbus
  tail`; the bridge is its listener, and a peer that tries costs a model turn to
  learn it was wrong.

Do nothing else.
