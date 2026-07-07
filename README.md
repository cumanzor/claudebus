# claudebus

A tiny **file-based message bus that lets two (or more) live Claude Code sessions
talk to each other** — for example a parent session and a `/branch-term` fork in
another window, so the fork can report results back live instead of writing a
handoff doc you carry over by hand.

> In a hurry? See **[CHEATSHEET.md](CHEATSHEET.md)**.

It's built entirely from **supported primitives** — the `Monitor` tool plus plain
files — so it doesn't depend on any undocumented internals and works across
terminal windows, tabs, tmux, and CCS profiles.

Peers are organized into **named channels**. A parent/fork pair working on a repo
gets its own channel (named after the repo by default), so aliases stay short and
unrelated work doesn't share an address space. The channel name `global` is
reserved by convention as the machine-wide bus — join it when you want a master
orchestrator that anything can reach.

```
                        channel "myrepo"
  session A ──cbus send myrepo/fork-1──▶  ~/.claude-bus/myrepo/fork-1/inbox.jsonl ──Monitor tail──▶ session B
  session B ──cbus send myrepo/main────▶  ~/.claude-bus/myrepo/main/inbox.jsonl ────Monitor tail──▶ session A
```

## How it works

Each participating session **joins a channel** and **arms a listener**:

- **Store** — `~/.claude-bus/<channel>/<alias>/` holds:
  - `meta.json` — registry entry: `{alias, channel, sessionId, listenerPid, ownerPid, cwd, host, ts}`
  - `inbox.jsonl` — append-only, one JSON message per line
- **Join** — `cbus join <channel>` auto-picks the alias (`main` if free, then
  `fork-1`, `fork-2`, …), is idempotent for a session already in the channel, and
  **auto-prunes dead peers first** so alias numbers get recycled instead of
  growing forever.
- **Receive** — the session runs `cbus tail <channel>/<alias>` under Claude Code's
  **Monitor** tool (persistent). `tail` `exec`s in place so *its own pid* becomes
  the liveness signal. Each incoming line arrives in that session's conversation
  as a JSON event `{"from","to","ts","text"}` (addresses in full `channel/alias`
  form), delivered at a turn boundary.
- **Send** — `cbus send <channel>/<alias> "text"` appends a line to the target's
  inbox. Within your own channel a bare alias works: `cbus send fork-1 "text"`.
  The sender's `from` is resolved automatically from `$CLAUDE_CODE_SESSION_ID`.

### Design details worth knowing

- **No lost messages during setup.** `join` truncates the inbox and the *first*
  arm replays from line 1 (`tail -n +1 -F`), so anything sent between *join* and
  *arming the Monitor* is still delivered — `send` accepts a joined-but-not-yet-
  armed peer for exactly this reason. A *re*-arm follows from the end of the
  inbox instead, so old messages are never redelivered.
- **Liveness is a real process, not a stale flag.** The tracked `listenerPid` *is*
  the `tail` process. When a window closes or the Monitor is stopped, the peer flips
  to `off` on its own, and `cbus send` refuses to message a dead window (override
  with `--force`, which queues the line best-effort — a re-arm follows from the
  end of the inbox, so it may never be delivered). Two edge cases are hardened:
  - *pid recycling* — a live pid is only trusted if its process args still
    reference this peer's inbox, so an unrelated process that inherited the number
    doesn't read as a false `listen`.
  - *crash-orphaned listener* — on arm, cbus also records `ownerPid`, the owning
    `claude` process. If the session is hard-killed (crash, `kill -9`), the `tail`
    can survive as an orphan with a live pid — but its `ownerPid` is gone, so the
    peer still reads `off`. (On a *clean* exit Claude Code stops the Monitor, which
    kills the tail directly; this covers the abnormal path.)
- **Self-cleaning registry.** `join` prunes dead peers in its channel before
  picking an alias; empty channels are removed. A peer that joined but hasn't
  armed its Monitor yet gets a 10-minute grace window so it can't be swept
  mid-setup. `cbus prune` does a manual sweep across all channels.

## Install

```sh
./install.sh          # copy into ~/.local/bin and ~/.claude/
./install.sh --link   # symlink instead, so `git pull` updates in place
```

This places:

| file | destination | purpose |
|---|---|---|
| `bin/cbus` | `~/.local/bin/cbus` | the message-bus CLI |
| `bin/cc-branch.sh` | `~/.claude/bin/cc-branch.sh` | session fork helper (only needed for `/bus-branch`) |
| `commands/bus-listen.md` | `~/.claude/commands/bus-listen.md` | slash command to join a channel |
| `commands/bus-branch.md` | `~/.claude/commands/bus-branch.md` | slash command to fork + auto-join both sides |

Make sure `~/.local/bin` is on your `PATH`.

> **Paths:** `commands/bus-branch.md` hardcodes the absolute path to `cc-branch.sh`
> (Claude Code slash commands need absolute paths in `allowed-tools`). If your `$HOME`
> or config dir differs, edit that path. `cc-branch.sh` itself relaunches through
> `ccs <profile>` when it detects a CCS config dir — drop that branch if you don't use
> CCS and just call `claude` directly.

## Usage

### Fork a session with the bus pre-wired

```
/bus-branch window            # or: tab | tmux — channel defaults to the repo name
/bus-branch window mytask     # explicit channel name
```

Joins the parent to the channel, arms the parent's listener, then forks the
current conversation into a new terminal via `cc-branch.sh` with a bootstrap
turn (printed by `cbus bootstrap <channel> <parent>`, so the prompt ships with
the binary): the child self-joins the same channel, arms its own listener, and
announces itself to the parent. The child reports results back with `cbus send`
instead of a handoff doc.

The child resumes the parent's transcript at boot, so it sees the parent's live
Monitor as one harmless "no completion record" background-task note. This is
cosmetic and unavoidable — the transcript is read when the child starts, always
after the parent armed — and the bootstrap prompt tells the child to ignore it.

### Put two already-open sessions on a channel

Run `/bus-listen` in each window — both default to the channel named after the
repo they're in, so two sessions in the same repo find each other with no pairing
step. For cross-repo pairs, pass the same channel name explicitly:

```
# window 1 (any repo)
/bus-listen deploy laptop

# window 2 (any repo)
/bus-listen deploy server
```

Then from either side, ask Claude to send:

```
# in window 2:  "send laptop: build's green, deploying"
#   -> cbus send laptop "build's green, deploying"        (bare alias: same channel)
#   -> cbus send deploy/laptop "..."                      (full address: from anywhere)
```

The message pops into window 1's conversation as an event. `cbus list` shows every
peer across channels; `cbus channels` summarizes channels.

### The global channel

`global` is an ordinary channel with a reserved meaning: the machine-wide bus.
Join it from a session meant to oversee everything (`/bus-listen global` or
`cbus join global`), and any session on the machine can reach it with
`cbus send global/<alias> "..."` regardless of what channel it works in. A session
can be in several channels at once (e.g. its repo channel *and* global) — arm one
Monitor per membership.

### More than two

A channel is an N-way registry, not a pair. Every session that joins the same
channel can message every other; aliases are auto-assigned and recycled
(`main`, then `fork-1`, `fork-2`, … reusing freed slots).

## CLI reference

```
cbus join <channel> [alias]      join a channel (alias auto: main, fork-N;
                                 prunes dead peers in the channel first)
cbus tail <channel>/<alias>      stream inbox — run under the Monitor tool
cbus send <target> [opts] TEXT   append a message to a peer's inbox;
                                 target is <channel>/<alias>, or a bare
                                 <alias> within your own channel(s)
     --from <ch/alias>           override sender (default: auto-resolved)
     --force                     send even if target's listener died (best effort)
cbus list [--active] [channel]   peers with listen/off state, host, cwd
cbus active [channel]            only peers currently listening (= list --active)
cbus channels                    channels with peer counts
cbus whoami                      this session's channel/alias memberships
cbus inbox <channel>/<alias>     print inbox path
cbus bootstrap <channel> [parent]  print the canonical fork-child prompt
cbus prune [channel]             remove dead peers (and empty channels)
cbus leave [channel]             leave channel(s) this session joined
cbus unregister <channel>/<alias>  force-remove any peer

env: CBUS_DIR (default ~/.claude-bus), CBUS_PYTHON (default python3)
```

`cbus register <alias>` is kept as a deprecated v1 alias for `cbus join global <alias>`.

## Caveats

- **No sender authentication.** Anything that can write to `~/.claude-bus` can inject a
  message, and `from` is taken at face value. Claude Code treats an incoming bus message
  as an untrusted peer request (no permission escalation), which is the right guardrail —
  but don't expose the bus directory beyond your own machine.
- **Channels are namespaces, not isolation.** Any local process can send to any channel;
  the channel only scopes addressing and cleanup.
- **Delivery is at turn boundaries.** A message lands the next time the receiving session
  takes a turn — it won't interrupt a session that's sitting idle mid-prompt until it acts.
- **No broadcast primitive.** `cbus send` targets one peer; message N times to reach N peers.
- **Requires `python3`** (used only for robust JSON read/write) and a BSD/GNU `tail` with `-F`.

## Why not the built-in teammate mailbox?

Claude Code's teammate `SendMessage` is *also* a file-based mailbox under
`<config>/teams/<team>/inboxes/`, and an external process *can* write to it. But it only
works when the session already has an **active teammate** (the inbox poller isn't armed in
a solo session), so you'd have to keep a dummy keepalive teammate alive per session — and
the file layout is undocumented and can change in any release. claudebus gets the same
result from stable, documented primitives, adds a real liveness-aware registry, and spans
CCS profiles.
