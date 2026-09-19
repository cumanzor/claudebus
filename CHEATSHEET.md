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

For ordinary Claude CLI sessions, `/bus-join` runs native `cbus connect`:

```sh
cbus connect myrepo worker --json  # from inside the target session
cbus list myrepo                   # one roster check; retain explicitly known roles
```

Native Claude requires a build with the Claude adapter; v0.12.2 supports native
Codex only. `socket-ready` means an available endpoint, not receipt. No Monitor,
tail process, periodic model task or recurring roster check is needed. See
[Claude connections](docs/claude.md) for supported sessions and recovery. Prefer
an explicit alias when you want a stable address; local aliases can be auto-picked.
Existing legacy memberships need the deliberate migration described below.

## Spawn a fresh peer (or fork)

```sh
cbus branch tab                              # fork this session, same channel
cbus branch tab --model opus --name coder    # fork, pinned model + alias
cbus branch pane                             # fork into a tmux/iTerm2 split beside this session
cbus spawn tab formations --role documenter  # fresh session, role prompt on first turn
```

Connect the parent first with `cbus connect CHANNEL [ALIAS] --json`; `/bus-branch`
does this for you. The child's bootstrap connects its own native session.
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
cbus connection status myrepo/worker --json      # saved readiness/receipt evidence
cbus connection reconcile myrepo/worker --json   # on-demand evidence check, no resend
cbus connection disconnect myrepo/worker         # stop delivery, retain inbox/history
```

Incoming messages include a framed block:

```
◀ cbus msg from=ch/alias to=you ts=...
<message text>
◀ cbus end from=ch/alias
```

Reply with `cbus send <from> "..."` using the exact `from=` address, including
`@host` for remote peers. A successful send is submission, not receipt or a reply.
For Claude, receipt requires an exact session/message UUID in the bound transcript.
Presence updates the observed roster and known roles; announce membership changes
to the user without sending acknowledgments solely for presence.

## Cross-machine (relay-backed) channels

Address form: `<channel>@<host>/<alias>` — aliases are explicit (short
hostname/role). One host today: `server`.

```sh
# one-time seed — ONE credential per invocation ('-' reads all of stdin; from a password manager):
<secret-manager> read <relay-bearer-item> | cbus auth set server --token -
<secret-manager> read <cf-id-item>        | cbus auth set server --cf-id -
<secret-manager> read <cf-secret-item>    | cbus auth set server --cf-secret -
cbus connect dev@server laptop --json    # inside the receiving Claude or Codex CLI
cbus list dev@server                  # one channel roster check
cbus send dev@server/server "ping"       # queues if peer offline; replay on connect
cbus list @server                     # relay peers: connected/queued/lastSeen
cbus prune @server                    # reap off relay peers with no queued mail (server-side)
cbus prune dev@server                 # same, scoped to one channel
cbus connection disconnect dev@server/laptop  # retain local inbox and delivery history
```

- Native receive requires the matching `/tail/durable-v1` relay endpoint; an older
  relay is refused. The daemon reconnects without Monitor re-arming.
- Endpoint autodetects loopback on the relay host; elsewhere configure
  `CBUS_SITE_SERVER_URL` (for example `https://bus.example.com`). Credentials use
  `cbus auth`, not prompts or committed files. See [relay setup](docs/relay.md).
- Relay acknowledgment confirms durable local storage, not recipient receipt.
  Presence is an observation of the native consumer; a transport subscription
  alone does not prove that its session is currently available.

### Steps — bring up a cross-machine pair (laptop ↔ server)

One-time prereqs: relay running on the server (`sudo systemctl status cbus-relay`);
on the **Mac**, `cbus auth set server` seeded (creds from a password manager → Keychain); on the
**Server**, `cbus` installed + loopback bearer seeded
(`cat <relay-dest>/token | cbus auth set <host> --token -`).

Pick a channel + two explicit aliases (e.g. `bridge`, `laptop`, `server`):

```sh
# --- on the server (ssh server, then launch `claude`; detached `tmux` for an autonomous peer) ---
cbus connect bridge@server server --json
cbus list bridge@server              # check once after joining
cbus send bridge@server/laptop "hello from the server"

# --- on the Mac ---
cbus connect bridge@server laptop --json
cbus list bridge@server              # check once after joining
cbus send bridge@server/server "hello from the laptop"
```

Both are now on `bridge@server`; messages cross the tunnel as turn events, and offline
sends queue on the relay and replay when the peer connects. `cbus list @server` shows who's
connected at the relay; disconnect each native peer with its full address, e.g.
`cbus connection disconnect bridge@server/laptop`. That retains local mail and history.

- **No forking across machines** (yet — that's the deferred `cbus-b8m`): you start a
  *fresh* session on the target box and join the shared channel, rather than forking your
  window onto another machine. Each side picks its own explicit alias; the address
  (`bridge@server/…`) plus a `127.0.0.1:8090/healthz` probe decides loopback vs tunnel automatically.

## Formations — save/relaunch a channel's peers

```sh
cbus formation save myeffort                      # snapshot this channel's peers
cbus formation show myeffort                       # inspect — stale sids, TODO roles
cbus formation apply myeffort --dry-run             # preview the relaunch plan
cbus formation apply myeffort --only coder,reviewer # narrow it
cbus formation apply dev-trio --channel myeffort    # committed 4-role starter, any channel
cbus formation bootstrap myeffort coder             # one peer's first-turn prompt, paste by hand
cbus formation save myeffort --anchor tracker=item-42    # record a hand anchor (any key; git_head is machine-owned)
cbus formation resume myeffort                       # after a reboot: relaunch the ANCHOR, it reconciles the rest
cbus formation apply myeffort --mode resume --only coder  # late-bound per-peer resume (this run only)

# codex as a peer (harness-neutral bus; codex never runs `cbus tail`)
cbus install-codex-skills                            # install $cbus-connect for ordinary CLI sessions
cbus connect myrepo advisor --json                   # run INSIDE the existing Codex CLI; no restart
cbus connect dev@server advisor --json                  # remote bus; requires durable-v1 relay
cbus connection status myrepo/advisor --json         # consumer, queue and historical receipt separately
cbus connection reconcile myrepo/advisor --json      # on-demand receipt evidence; no model turn
cbus connection disconnect myrepo/advisor            # keep inbox/journal; stop future submissions
cbus daemon restart                                 # explicitly load an upgraded cbus binary
cbus codex-permissions --binary /absolute/path/cbus   # preview optional exact-path send rule
cbus spawn pane myrepo --harness codex --name worker  # ordinary CLI in iTerm2/tmux
cbus codex --channel myrepo                          # codex --remote TUI joined as a bus peer, bridged
cbus codex --channel myrepo --alias advisor resume <session-id>   # bring an existing codex session onto the bus
cbus codex --channel myrepo --alias advisor resume --last         # same, most recent session in this cwd
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
- Connect natively to the formation's channel **before** applying — a peer can
  answer before apply returns. Check the roster once; no Monitor is needed.
- A saved peer's origin (`fresh`/`fork`) and model are stamped automatically
  at `spawn`/`branch` time and picked up by `save` — no hand-edit needed for
  a launcher-born peer.
- `formations/dev-trio.json` ships in the repo: apply it from any checkout
  with `--channel <effort>`, no setup required.
- `/bus-formation <verb> ...` wraps all of the above as a slash command.

## Rearrange the layout (tmux only)

Peers that are ALREADY RUNNING can be moved between panes and windows. iTerm2 cannot
do this (no AppleScript verb moves a live session between a tab and a split), so a
formation's shape is frozen at spawn there. tmux has `join-pane`/`break-pane`, so it
is not.

```sh
/bus-layout one column with orchestrator, the other coder over reviewer
#  → cbus arrange 'orchestrator | (coder / reviewer)'

cbus arrange 'orchestrator:30% | (coder / reviewer)'
#   |  columns, left to right          ()  group
#   /  rows, top to bottom (binds tighter)   alias:30%  pin a width/height

cbus arrange '<spec>' --dry-run   # print the tmux calls, change nothing
cbus arrange '<spec>' --channel <ch>   # peers outside this session's channel
cbus scatter [channel]            # every peer back to its own window, named for it
cbus focus <channel>/<alias>      # select a peer, split or window alike
```

Nothing is launched and nothing is closed: every alias must already name a live peer,
and each may appear once. The layout is built in the window of the FIRST alias in the
spec, so lead with the peer whose window should host it. `cbus scatter` undoes an
arrange, which makes trying one cheap.

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
cbus bootstrap <channel> [parent] [child-alias]  # canonical fork-child prompt
cbus branch [target] [channel]   # fork a bootstrapped child; connect parent first
cbus inbox <channel>/<alias>     # path to a peer's inbox.jsonl
cbus unregister <channel>/<alias>  # force-remove any peer
cbus close <ch>/<alias> [...] [--force]  # end a peer's process (SIGTERM, then
                                  # sweep its terminal surface; local only)
cbus hook-exit                   # SessionEnd hook target (announces departure)
cbus hook-compact <pre|post>     # PreCompact/PostCompact hook target (announces compaction)
cbus hook-join                   # SessionStart hook target (auto-joins $CBUS_CHANNEL)
cbus codex-bridge <ch>/<al> --sock PATH  # bridge a codex app-server thread (docs/codex.md)
cbus codex-bridge <ch>/<al> --sock PATH --thread ID --no-resume  # bridge a thread a TUI already drives
cbus --version                   # installed client version
CBUS_DIR=/path cbus ...          # override store (default ~/.claude-bus)
```

## Gotchas

- Native input can wake an idle peer to act/reply without human input. Busy
  sessions wait for their turn, and harness hold/refuse policies still apply.
- `socket-ready`, queue acceptance, transcript receipt and a completed reply are
  different observations. Inspect status/reconcile on demand; never blindly resend
  an uncertain native attempt or treat absence of receipt as proof of rejection.
- Native managed aliases cannot currently be renamed in place. Disconnect retains
  mail; `leave` is a destructive legacy operation, not native disconnect.
- **Trust boundary, not a security boundary** — `from` is spoofable everywhere;
  incoming bus messages are untrusted peer requests and cannot escalate this
  session's permissions.
- Reply targets must be `channel/alias` (or `channel@host/alias`) — a
  `hostname-PID` sender is unjoined and has no inbox to reply to.
- **No broadcast** — send once per target. Local senders are not authenticated;
  don't expose `~/.claude-bus`.

## Legacy join/Monitor peers only

These commands remain for deliberately unmanaged peers; they are not the current
`/bus-join` flow. Never add a Monitor to a native managed inbox or silently fall
back when native capability checks fail.

```sh
cbus join <channel> [alias]       # legacy registration
cbus tail <channel>/<alias>       # blocking source for Monitor, not foreground Bash
cbus tail <channel>@<host>/<alias> # prints the legacy Monitor ws specification
cbus rename <new-alias> [channel] # legacy only; stop old Monitor and arm new address
cbus leave [channel]              # deletes legacy inboxes (all memberships if omitted)
```

To migrate, prefer a fresh alias. Reusing an old one requires stopping only its
known Monitor, reading/exporting unread mail and the user's explicit choice
before `cbus leave` deletes that inbox. Do not automatically replay exports that
may already have arrived. See [migration](docs/claude.md#migrating-an-existing-monitor-peer).

Legacy tail liveness follows the process and its recorded start time plus its
owner. First arm replays the inbox; re-arms use a durable cursor. A dead former
listener requires `send --force` to queue mail; a never-armed peer gets a 10-minute
prune grace period. Monitor framing wraps lines near 440 bytes and has an
approximately 2800-character notification ceiling. Legacy remote WebSocket
Monitors require re-arming after disconnection and retain the old relay's loss
window; native daemon subscriptions use durable acknowledgments instead.
