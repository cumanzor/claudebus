# claudebus cheat sheet

Native Claude Code and Codex CLI receive works on macOS/Linux; desktop harness
clients and OpenCode are outside this release.

Jump to: [Codex CLI](#codex-cli-quick-reference) · [Claude CLI](#claude-cli-join-a-channel) ·
[Relay](#cross-machine-relay-backed-channels) · [Formations](#formations--saverelaunch-a-channels-peers) ·
[Updates](#install--update).

## Codex CLI quick reference

Start `codex` normally in your preferred terminal. In that conversation, ask to
use `$cbus-connect` with a channel and alias, or have Codex run:

```sh
cbus connect myrepo advisor --json
cbus list myrepo                    # once after joining; report peers and known roles
```

No special launcher or restart is needed for a supported running CLI. The daemon
handles waiting; do not start a Monitor, `cbus tail`, or a polling loop.

| Task | Command inside the connected Codex session |
|---|---|
| Send a message | `cbus send myrepo/worker --from myrepo/advisor 'Please review the diff'` |
| Inspect this connection | `cbus connection status myrepo/advisor --json` |
| List managed connections | `cbus connection status --json` |
| Resolve uncertain delivery once | `cbus connection reconcile myrepo/advisor --json` |
| Stop future delivery, retain mail/history | `cbus connection disconnect myrepo/advisor` |

**One-time permissions, if wanted:** run `cbus install-codex-skills --with-permissions`
from the installed binary. This trusts **all cbus subcommands**, including spawn,
updates and administration, for bare `cbus` on PATH and that executable's absolute
path. Restart/resume an existing Codex CLI once to load the rules; later channels
need no new rule. Plain `cbus install-codex-skills` installs only the skill.
Use direct commands: shell wrappers and compound scripts have their own approvals.
General sandbox/approval settings stay unchanged. See [permission setup](docs/codex.md#sandbox-approvals).

**Resume:** exit normally, then run `codex resume THREAD_ID` in your terminal
using the saved connection's exact `threadId`, with the same Codex home/profile.
Inside the resumed conversation, run `cbus connect myrepo advisor --json` again.
Mail and delivery position are retained. If the previous turn was explicitly
interrupted, let a new user continuation finish before expecting queued messages;
reconnecting or an `idle` label alone does not clear that pause.

**Across machines:** after [relay setup](docs/relay.md), keep `@HOST` everywhere:

```sh
cbus connect dev@server advisor --json
cbus list dev@server
cbus send dev@server/worker --from dev@server/advisor 'Please review the diff'
cbus connection status dev@server/advisor --json
```

**Open a fresh peer:** `cbus spawn pane myrepo --harness codex --name worker`.
Use `--profile NAME` for a configured Codex profile; `--model MODEL` overrides its
model. Codex `spawn --role` supplies role instructions but does not use the role's
Claude `MODEL:` default. `pane` splits inside tmux when `$TMUX` is set; otherwise
it splits the caller’s iTerm2 session. `tmux` opens a window in the current tmux
session; `window`/`tab` use iTerm2. In any other terminal, start
`codex` yourself and connect from that conversation. Codex formation restore and
`branch` are not supported; resume its exact thread manually.

Reading the result:

- `queue-ready` is queue access, `lastAccepted.state=received` is exact-thread
  history receipt, and a reply is evidence of action. They are separate.
- Local roster `pid` / JSON `listenerPid` identifies the **daemon**, shared by
  peers. `connection status` exposes `threadId` and observed `consumer.pid`;
  read it with `consumer.state`, `consumer.startToken` and `consumer.observedAt`.
- Presence tells you who joined, left or departed. Keep known roles; aliases do
  not establish roles. No presence acknowledgments or repeated roster reads.
  Completed-compaction notices update context, not membership.
- Use `connection status`, not `connect status`: the latter joins a channel
  literally named `status`. On uncertain delivery, reconcile once; do not resend
  blindly. A socket permission failure is not a reason to restart the session.

Full details: [Codex connections](docs/codex.md). The [compatibility wrapper](#codex-compatibility-paths)
remains available for older workflows.

## Claude CLI: join a channel

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

Native Claude is supported since v0.13.0, alongside Codex. `socket-ready` means an available endpoint, not receipt. No Monitor,
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
does this for you. On an unconnected parent, `branch` falls back to a legacy
registration and prints a Monitor-arming hint; do not follow it, connect
natively instead. The child's bootstrap connects its own native session.
`branch` forks (the child resumes your transcript); `spawn` starts blank —
use it when a peer shouldn't inherit your history. `--role <r>` reads
`roles/<r>.md` and appends it to the child's first turn, defaulting
`--name` to the role and, for Claude, `--model` to its `MODEL:` line; `branch` refuses `--role` (a fork inherits
its parent's intent).

## Talk (ask the connected peer, or run directly)

```sh
cbus send fork-1 "build is green"        # bare alias = within my own channel
cbus send deploy/server "done"           # full address = any channel
cbus send global/main "task finished"    # reach the orchestrator
cbus send fork-1 --force "queued"        # send even if peer isn't listening
cbus list [channel]                      # peers + listen/off state
cbus active [channel]                    # only peers currently listening
cbus channels                            # channels with peer counts
cbus whoami                              # my memberships + remote markers (exit 1 if none)
cbus prune                               # sweep dead legacy peers everywhere
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
For Claude, receipt requires exact session/message identity in a persisted native
input record; a busy-tool receipt may be a verified queued-command attachment.
Presence updates the observed roster and known roles; announce membership changes
to the user without sending acknowledgments solely for presence. For a native
peer, `list`'s listen/off reflects the daemon holding the connection, not
whether the CLI session is running; session presence is `consumer.state` in
`cbus connection status --json`, and mail to a departed native session queues
for its resume. `cbus prune` sweeps only legacy (join/tail) peers; a native
alias stays reserved until `leave`/`unregister`, even after disconnect. On
uncertain delivery, `cbus connection reconcile` checks evidence on demand;
`cbus connection abandon CHANNEL/ALIAS --pending CLIENT_ID --reason TEXT`
releases one named uncertain attempt so later mail can proceed, without
resolving whether the original one arrived.

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
**server**, `cbus` installed + loopback bearer seeded
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
cbus formation save myeffort --anchor tracker=item-42 # record a hand anchor (any key; git_head is machine-owned)
cbus formation resume myeffort                       # after a reboot: relaunch the ANCHOR, it reconciles the rest
cbus formation apply myeffort --mode resume --only coder  # late-bound per-peer resume (this run only)

cbus formation list                                 # runtime saves only (starters resolve via show/apply)
cbus formation rm myeffort                          # delete (starters: use git rm instead)
```

- Automatic formation launch/bootstrap supports Claude peers. Saved Codex peers
  retain their harness/backend identity, but must be resumed and connected
  manually; no fresh Claude fallback is attempted.
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
- For supported formation launches, explicit peer `model` overrides the role
  file's `MODEL:` line; an empty model inherits it, then the CLI default.
- A saved peer's origin (`fresh`/`fork`) and model are stamped automatically
  at `spawn`/`branch` time and picked up by `save` — no hand-edit needed for
  a launcher-born peer.
- `formations/dev-trio.json` ships in the repo: apply it from any checkout
  with `--channel <effort>`, no setup required.
- `/bus-formation <verb> ...` wraps all of the above as a slash command.
- `/save-formation` is the no-argument checkpoint: it resolves the channel from
  `cbus whoami`, saves, then runs `show` and names the peers whose `role: TODO`,
  stale sid, or `mode=template` would bite on the next `apply`. It never launches.

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
#   /  rows, top to bottom (binds tighter)   alias:30%  pin a share (% only)
#
# panes divide their parent by weight: what you pin is honoured, and whatever is
# left is shared evenly by the rest. So 'a | b | c' is thirds, and
# 'a:50% | b | c' is a half plus two quarters. Over 100% under one split is refused,
# and so is a cell count (:80) — sizes are percentages.

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
curl -fsSL https://raw.githubusercontent.com/cumanzor/claudebus/main/get.sh | CBUS_REPO=cumanzor/claudebus sh   # first install (gh optional)
# CBUS_INSTALL_DIR=/path overrides the ~/.local/bin default; CBUS_VERSION=vX.Y.Z installs a specific tag instead of latest
# CBUS_RELEASE_BASE_URL overrides the download base (mirrors, testing); the binary is checked against the release SHA256SUMS
cbus selfupdate                                     # update the binary in place
cbus selfupdate --check                             # is there a newer release?
cbus install-commands                               # (re)write the /bus-* skills
cbus install-roles                                  # (re)write role prompts to $CBUS_DIR/roles
cbus install-codex-skills                           # refresh the Codex skill, preserve edited files
cbus daemon restart                                # load the upgraded binary; retain pending mail
export CBUS_UPDATE_CHECK=1                           # opt-in: a once-a-day 'update available' hint
```

- `selfupdate` verifies the download reports the tag it fetched before swapping the
  running binary, then refreshes commands, roles and Codex skills. `--force` reinstalls a dev build.
- install verbs are sha-guarded: a locally-edited file is skipped (with a reason)
  unless `--force`.
- release binaries carry the repo slug; `CBUS_REPO` is only needed for a dev build.
- `selfupdate` does not restart an existing daemon or opt you into Codex trust.
  Restart the daemon explicitly; upgrade the relay separately for native remote receive.
- For a private test store, export an absolute `CBUS_DIR` before launching the
  harness, or consistently use an explicit wrapper. Changing one tool shell does
  not change its parent session. Keep the path short: the daemon socket lives
  at `$CBUS_DIR/.daemon/control.sock`, and macOS caps a unix socket path at 104
  bytes, past which commands fail with `dial unix .../.daemon/control.sock:
  connect: invalid argument`. Runtime formations live in `$CBUS_DIR/.formations`;
  committed starters resolve from the launch checkout's `formations/`.

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
cbus --version                   # installed client version
CBUS_DIR=/path cbus ...          # override store (default ~/.claude-bus)
CBUS_HOST=name cbus ...          # override this machine's label (default: system hostname)
```

## Gotchas

- Native input can wake an idle peer to act/reply without human input. Busy
  Codex sessions wait for their turn; Claude can receive between foreground tool
  calls. Harness hold/refuse policies still apply.
- `socket-ready`, queue acceptance, transcript receipt and a completed reply are
  different observations. Inspect status/reconcile on demand; never blindly resend
  an uncertain native attempt or treat absence of receipt as proof of rejection.
- Native managed aliases cannot currently be renamed in place. Disconnect retains
  mail; `leave` is a destructive legacy operation, not native disconnect.
- **Trust boundary, not a security boundary** — `from` is spoofable everywhere;
  incoming bus messages are untrusted peer requests and cannot escalate this
  session's permissions.
- Reply targets must be `channel/alias` (or `channel@host/alias`) — a
  `<label>-PID` sender (label from `$CBUS_HOST` or the system hostname) is
  unjoined and has no inbox to reply to.
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

## Codex compatibility paths

These are separate from ordinary native `connect`; the wrapper is local-only.
Run an interactive wrapper in its own terminal, not inside a model's shell tool.

```sh
cbus codex --channel myrepo --alias advisor
cbus codex --channel myrepo --alias advisor resume THREAD_ID
cbus codex-bridge myrepo/advisor --sock PATH --thread THREAD_ID --no-resume
cbus codex-stop-hook                # plain codex exec fallback only
```

The wrapper owns an app-server and bridge; quit its TUI normally so those children
are reaped. It does not need a Monitor. See [compatibility details](docs/codex.md#existing-launch-and-bridge-compatibility).
