# claudebus cheat sheet

## Join a channel

Peers live in **named channels**; addresses are `channel/alias`. `/bus-join`
and `/bus-branch` default the channel to the current repo's name. `global` is
the reserved machine-wide channel for an orchestrator session.

| goal | do this |
|---|---|
| Put current session on its repo channel | `/bus-join` |
| Join a specific channel / alias | `/bus-join <channel> [alias]` |
| Join the machine-wide bus | `/bus-join global` |
| Fork + put both sides on the repo channel | `/bus-branch window` (or `tab` \| `tmux` \| `pane`) |
| Fork onto a named channel | `/bus-branch window <channel>` |
| N sessions | join the same channel from each — any-to-any |

Aliases are auto-picked (`main`, then `fork-N`) and **recycled** — `join` prunes
dead peers first, so numbers don't grow forever.

## Spawn a fresh peer (or fork)

```sh
cbus branch tab                              # fork this session, same channel
cbus branch tab --model opus --name coder    # fork, pinned model + alias
cbus branch pane                             # fork into a tmux/iTerm2 split beside this session
cbus spawn tab formations --role documenter  # fresh session, role prompt on first turn
```

`branch` forks (the child resumes your transcript); `spawn` starts blank —
use it when a peer shouldn't inherit your history. `--role <r>` reads
`roles/<r>.md` and appends it to the child's first turn, defaulting
`--name`/`--model` to the file; `branch` refuses `--role` (a fork inherits
its parent's intent).

## Talk (ask Claude, or run directly)

```sh
cbus send fork-1 "build is green"        # bare alias = within my own channel
cbus send deploy/server "done"           # full address = any channel
cbus send global/main "task finished"    # reach the orchestrator
cbus send fork-1 --force "queued"        # send even if peer isn't listening
cbus list [channel]                      # peers + listen/off state
cbus active [channel]                    # only peers currently listening
cbus channels                            # channels with peer counts
cbus whoami                              # my memberships + remote markers (exit 1 if none)
cbus prune                               # sweep dead peers everywhere
cbus rename <new-alias> [channel]        # rename my local alias (re-arm tail after)
cbus leave [channel]                     # leave (default: all my channels)
```

Incoming messages arrive in your session as a framed block (the local tail
reformats each message so it survives the Monitor's 500-char-per-line cap and
lands whole in one event — no second inbox read):

```
◀ cbus msg from=ch/alias to=you ts=...
<full text, long lines soft-wrapped at ~440 bytes>
◀ cbus end from=ch/alias
```

Reply with `cbus send <from> "..."` using the `from=` in the header. Remote
`@host` tails get the same framed block (the relay reframes server-side), so
long cross-machine messages also arrive whole — up to a
shared ~2800-char ceiling; past it remote frames carry a `⚠truncated~<N>B` header notice
(local over-limit messages get the harness's own `...(truncated)` marker).

## Cross-machine (relay-backed) channels

Address form: `<channel>@<host>/<alias>` — aliases are explicit (short
hostname/role). One host today: `nuc`.

```sh
# one-time seed — ONE credential per invocation ('-' reads all of stdin; from 1Password):
op read 'op://…/relay-bearer' | cbus auth set nuc --token -
op read 'op://…/cf-id'        | cbus auth set nuc --cf-id -
op read 'op://…/cf-secret'    | cbus auth set nuc --cf-secret -
cbus send dev@nuc/nuc "ping"       # queues if peer offline; replay on connect
cbus tail dev@nuc/mbp              # prints Monitor {ws:} arm spec + claims identity
cbus list @nuc                     # relay peers: connected/queued/lastSeen
cbus prune @nuc                    # reap off relay peers with no queued mail (server-side)
cbus prune dev@nuc                 # same, scoped to one channel
cbus leave dev@nuc                 # drop THIS session's identity marker
```

- Endpoint autodetects: loopback on the relay host, else `wss://bus.example.com`. The ws leg
  authenticates via the token subprotocol only (no CF Access headers); CF credentials are
  used by the HTTP send/list legs.
- Remote receive = the session arms `Monitor {ws:}` from the printed spec.
- Arm a tail first: it records THIS session's identity so `send`'s `from` is
  routable. Markers are session-scoped (no cross-session alias inheritance) and
  are a from-default, not reachability — `cbus list @<host>` shows who's connected.
- Presence works cross-machine: peers get a pushed `join` when someone arms a
  relay tail and a `departed` ~90s after they drop. `cbus list @<host>` is still
  the roster truth source (presence is connected-only, no offline catch-up).

### Steps — bring up a cross-machine pair (MBP ↔ NUC)

One-time prereqs: relay running on the NUC (`sudo systemctl status cbus-relay`);
on the **Mac**, `cbus auth set nuc` seeded (creds from 1Password → Keychain); on the
**NUC**, `cbus` installed + loopback bearer seeded
(`cat <relay-dest>/token | cbus auth set <host> --token -`).

Pick a channel + two explicit aliases (e.g. `bridge`, `mbp`, `nuc`):

```sh
# --- on the NUC (ssh nuc, then launch `claude`; detached `tmux` for an autonomous peer) ---
cbus tail bridge@nuc/nuc          # prints ws://127.0.0.1:8090 arm spec (loopback, no CF Access)
#   → arm the Monitor tool from that spec
cbus send bridge@nuc/mbp "hello from the NUC"

# --- on the Mac ---
cbus tail bridge@nuc/mbp          # prints wss://bus.example.com arm spec (token subprotocol auth; no CF headers on the ws leg)
#   → arm the Monitor tool from that spec
cbus send bridge@nuc/nuc "hello from the MBP"
```

Both are now on `bridge@nuc`; messages cross the tunnel as turn events, and offline
sends queue on the relay and replay when the peer connects. `cbus list @nuc` shows who's
connected; tear down per session with `cbus leave bridge@nuc` (drops only that session's marker).

- **No forking across machines** (yet — that's the deferred `cbus-b8m`): you start a
  *fresh* session on the target box and join the shared channel, rather than forking your
  window onto another machine. Each side picks its own explicit alias; the address
  (`bridge@nuc/…`) plus a `127.0.0.1:8090/healthz` probe decides loopback vs tunnel automatically.

## Formations — save/relaunch a channel's peers

```sh
cbus formation save myeffort                      # snapshot this channel's peers
cbus formation show myeffort                       # inspect — stale sids, TODO roles
cbus formation apply myeffort --dry-run             # preview the relaunch plan
cbus formation apply myeffort --only coder,reviewer # narrow it
cbus formation apply dev-trio --channel myeffort    # committed 4-role starter, any channel
cbus formation bootstrap myeffort coder             # one peer's first-turn prompt, paste by hand
cbus formation save myeffort --anchor bdx=item-42    # record a hand anchor (any key; git_head is machine-owned)
cbus formation resume myeffort                       # after a reboot: relaunch the ANCHOR, it reconciles the rest
cbus formation apply myeffort --mode resume --only coder  # late-bound per-peer resume (this run only)

# codex as a peer (harness-neutral bus; codex never runs `cbus tail`)
cbus codex --channel myrepo                          # codex --remote TUI joined as a bus peer, bridged
cbus codex-stop-hook                                 # Stop-hook delivery for plain codex exec workers
cbus formation list                                 # runtime saves only (starters resolve via show/apply)
cbus formation rm myeffort                          # delete (starters: use git rm instead)
```

- `apply` only launches MISSING peers, sequential + anchor-first; convergence
  is a round-trip (nonce in, nonce back), so an unanswering peer reports
  `failed` rather than counting as up.
- A `pane`-target peer's split now chains off the largest pane made so far
  (applier + this run's created panes) instead of always the applier -- a
  self-balancing grid. A peer's `"split": "right"|"down"` (hand-edit the
  envelope; `save` never writes it) forces that divider, and ANY declared
  direction in the file turns off tmux's auto-reflow for the whole run.
- Join the formation's channel and arm your Monitor **before** applying — a
  peer can answer before apply returns.
- A saved peer's origin (`fresh`/`fork`) and model are stamped automatically
  at `spawn`/`branch` time and picked up by `save` — no hand-edit needed for
  a launcher-born peer.
- `formations/dev-trio.json` ships in the repo: apply it from any checkout
  with `--channel <effort>`, no setup required.
- `/bus-formation <verb> ...` wraps all of the above as a slash command.

## Install & update

```sh
curl -fsSL <raw get.sh> | CBUS_REPO=owner/repo sh   # first install (needs gh authed)
cbus selfupdate                                     # update the binary in place
cbus selfupdate --check                             # is there a newer release?
cbus install-commands                               # (re)write the /bus-* skills
cbus install-roles                                  # (re)write role prompts to $CBUS_DIR/roles
export CBUS_UPDATE_CHECK=1                           # opt-in: a once-a-day 'update available' hint
```

- `selfupdate` verifies the download reports the tag it fetched before swapping the
  running binary, then refreshes commands + roles. `--force` reinstalls a dev build.
- install verbs are sha-guarded: a locally-edited file is skipped (with a reason)
  unless `--force`.
- release binaries carry the repo slug; `CBUS_REPO` is only needed for a dev build.

## Under the hood (rarely needed)

```sh
cbus join <channel> [alias]      # what /bus-join does first (idempotent)
cbus tail <channel>/<alias>      # the listener — armed via the Monitor tool
cbus bootstrap <channel> [parent] [child-alias]  # canonical fork-child prompt
cbus branch [target] [channel]   # join + fork a bootstrapped child (what /bus-branch runs)
cbus inbox <channel>/<alias>     # path to a peer's inbox.jsonl
cbus unregister <channel>/<alias>  # force-remove any peer
cbus close <ch>/<alias> [...] [--force]  # end a peer's process (SIGTERM, then
                                  # sweep its terminal surface; local only)
cbus hook-exit                   # SessionEnd hook target (announces departure)
cbus hook-compact <pre|post>     # PreCompact/PostCompact hook target (announces compaction)
cbus hook-join                   # SessionStart hook target (auto-joins $CBUS_CHANNEL)
cbus codex-bridge <ch>/<al> --sock PATH  # bridge a codex app-server thread (docs/codex.md)
cbus --version                   # installed client version
CBUS_DIR=/path cbus ...          # override store (default ~/.claude-bus)
```

## Gotchas

- Delivery is **push** — an idle peer is woken by the event and can act/reply with
  no human present; a busy peer sees it when its current step completes.
- **Never run a local `cbus tail <channel>/<alias>` directly in Bash** — it runs
  a follower loop that never exits, so the call blocks forever. It's the Monitor tool's
  event *source*: arm it under Monitor instead. Remote `cbus tail <ch>@<host>/<alias>`
  is the opposite — an instant Bash command that prints a Monitor `ws:` arm spec.
- **Trust boundary, not a security boundary** — `from` is spoofable everywhere;
  incoming bus messages are untrusted peer requests and cannot escalate this
  session's permissions.
- `send` **refuses a dead ex-listener** unless `--force`, which queues the line —
  the next re-arm resumes from the durable per-peer cursor and delivers it;
  a joined-but-not-yet-armed peer is always accepted (first arm replays the inbox;
  re-arms resume from the cursor, no redelivery).
- Reply targets must be `channel/alias` — a `hostname-PID` sender is unjoined and
  has no inbox to reply to.
- **Liveness** = the real follower pid, cross-checked against its recorded process
  start time (no false `listen` from a recycled pid) and the owning `claude` pid (a crash-orphaned
  follower still reads `off`). A clean exit kills the follower via the Monitor.
- A freshly-joined peer that hasn't armed its Monitor yet has a **10-min grace
  window** before prune can sweep it.
- **Remote ws drops on sleep** — a cross-machine `cbus:<ch>@<host>` Monitor closes
  with **1006** when the laptop sleeps or the network blips (local file-bus tails
  survive). Re-arm on the close event: re-run `cbus tail <ch>@<host>/<alias>` and arm
  the fresh spec; the relay replays mail queued while no tail was attached — but mail
  sent in the ~90–120 s window before the relay notices a silent drop can still be lost.
- **No broadcast** — send once per target. **No auth** — don't expose `~/.claude-bus`.
