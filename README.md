# claudebus

A tiny **file-based message bus that lets two (or more) live Claude Code sessions
talk to each other** — for example a parent session and a `/branch-term` fork in
another window, so the fork can report results back live instead of writing a
handoff doc you carry over by hand.

> In a hurry? See **[CHEATSHEET.md](CHEATSHEET.md)**.

It's built entirely from **supported primitives** — the `Monitor` tool plus plain
files — so it doesn't depend on any undocumented internals and works across
terminal windows, tabs, tmux, and CCS profiles. The client is a single Go binary
(`cmd/cbus`); the retired bash implementation is kept in `bin/` as the rollback
artifact until P3.

Peers are organized into **named channels**. A parent/fork pair working on a repo
gets its own channel (named after the repo by default), so aliases stay short and
unrelated work doesn't share an address space. The channel name `global` is
reserved by convention as the machine-wide bus — join it when you want a master
orchestrator that anything can reach.

> **Scope — bespoke by design.** This is a personal, single-operator tool wired to
> one specific setup (a homelab NUC reachable over a Cloudflare tunnel). It's here to
> be *read* — an honest write-up of the architecture and tradeoffs — not packaged for
> others to deploy. The cross-machine relay assumes your own trusted network and an
> authenticated tunnel; see [Security & network model](#security--network-model).

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
  **Monitor** tool (persistent). It re-`exec`s itself as a small follower process so
  *its own pid* becomes the liveness signal (its argv carries the inbox path via
  `--inbox`, which the liveness check matches). The follower reframes each stored message into a
  short `◀ cbus msg from=… to=… ts=…` header + the text soft-wrapped at ~440 bytes
  + a `◀ cbus end` marker. Why: the Monitor truncates any single stdout line at
  **500 chars**, so a raw 1-line JSON event forces the receiver into a second inbox
  read; emitting several short lines (which the Monitor batches into one
  notification) delivers a long message whole in the first event. Push delivery: an
  idle session is woken immediately; a busy one sees it when its step completes.
  Remote `@host` tails get the identical framing — the **relay** reframes each
  message server-side into one multiline ws frame (a multiline frame is capped
  per-line at 500, not 500 total), so long cross-machine messages arrive whole
  too. Both paths share a ~2800-char per-notification ceiling. Past it, remote frames carry an
  in-band `⚠truncated~<N>B` header notice; on the local path the harness itself appends a
  `...(truncated)` marker — visible on both paths, by different mechanisms.
- **Send** — `cbus send <channel>/<alias> "text"` appends a line to the target's
  inbox. Within your own channel a bare alias works: `cbus send fork-1 "text"`.
  The sender's `from` is resolved automatically (this
  session's own registration where possible; unjoined senders fall back to an unroutable
  `hostname-PID`).

### Design details worth knowing

- **No lost messages during setup.** `join` truncates the inbox and the *first*
  arm replays the whole inbox from the start, so anything sent between *join* and
  *arming the Monitor* is still delivered — `send` accepts a joined-but-not-yet-
  armed peer for exactly this reason. A *re*-arm follows from the end of the
  inbox instead, so old messages are never redelivered.
- **Liveness is a real process, not a stale flag.** The tracked `listenerPid` *is*
  the follower process. When a window closes or the Monitor is stopped, the peer flips
  to `off` on its own, and `cbus send` refuses to message a dead window (override
  with `--force`, which queues the line best-effort — a re-arm follows from the
  end of the inbox, so it may never be delivered). Two edge cases are hardened:
  - *pid recycling* — a live pid is only trusted if its process args still
    reference this peer's inbox, so an unrelated process that inherited the number
    doesn't read as a false `listen`.
  - *crash-orphaned listener* — on arm, cbus also records `ownerPid`, the owning
    `claude` process. If the session is hard-killed (crash, `kill -9`), the follower
    can survive as an orphan with a live pid — but its `ownerPid` is gone, so the
    peer still reads `off`. (On a *clean* exit Claude Code stops the Monitor, which
    kills the follower directly; this covers the abnormal path.)
- **Self-cleaning registry.** `join` prunes dead peers in its channel before
  picking an alias; empty channels are removed. A peer that joined but hasn't
  armed its Monitor yet gets a 10-minute grace window so it can't be swept
  mid-setup. `cbus prune` does a manual sweep across all channels.

## Install

The client is a single static Go binary — no runtime dependencies (python3 is no
longer needed). Build and install it as `cbus`:

```sh
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" \
  -o ~/.local/bin/cbus ./cmd/cbus
```

Then place the slash commands (copy `commands/*.md` into `~/.claude/commands/`):

| file | destination | purpose |
|---|---|---|
| `commands/bus-join.md` | `~/.claude/commands/bus-join.md` | join a channel |
| `commands/bus-branch.md` | `~/.claude/commands/bus-branch.md` | fork + auto-join both sides |
| `commands/bus-spawn.md` | `~/.claude/commands/bus-spawn.md` | open a fresh session, joined to a channel |
| `commands/bus-rename.md` | `~/.claude/commands/bus-rename.md` | rename this session's alias |
| `commands/bus-formation.md` | `~/.claude/commands/bus-formation.md` | save/apply/bootstrap a [formation](#formations) |

Make sure `~/.local/bin` is on your `PATH`. `cbus --version` shows what's installed.

> **Legacy installers** (kept until P3 homogenization deletes the bash artifacts —
> see [compat-deletion-plan](docs/architecture/compat-deletion-plan.md)):
> `./install.sh` installs the **retired bash client** over `~/.local/bin/cbus` —
> running it is the rollback procedure, so don't run it casually. `./install-cbus-go.sh`
> is the transitional side-by-side installer (builds the Go client as `cbus-go`).

> **Forking:** `cbus branch` forks natively (iTerm2 window/tab via osascript, or
> tmux) and relaunches through `ccs <profile>` when it detects a CCS config dir. The
> old `bin/cc-branch.sh` helper is no longer consulted.

## Usage

### Fork a session with the bus pre-wired

```
/bus-branch window            # or: tab | tmux — channel defaults to the repo name
/bus-branch window mytask     # explicit channel name
```

Runs `cbus branch <target> [channel]` — a single command that joins the parent
to the channel and forks the conversation into a new terminal (natively — iTerm2
window/tab or tmux) with the canonical bootstrap turn — then arms the parent's
listener. The child self-joins the same channel, arms its own listener, and
announces itself to the parent, reporting results back with `cbus send` instead
of a handoff doc.

The child resumes the parent's transcript at boot, so it sees the parent's live
Monitor as one harmless "no completion record" background-task note. This is
cosmetic and unavoidable — the transcript is read when the child starts, always
after the parent armed — and the bootstrap prompt tells the child to ignore it.

### Open a fresh session instead of forking

```
cbus spawn tab                              # fresh session, joins + arms itself
cbus spawn tab mytask --model opus --name coder
cbus spawn tab formations --role documenter  # role prompt rides the first turn
```

`spawn` is `branch`'s fresh-transcript sibling: same terminal launch
(window/tab/tmux) and the same join-and-arm-on-its-own bootstrap, but the child
starts blank instead of resuming the parent's conversation — the right choice
when a peer shouldn't inherit the parent's history, such as a distinct role in
a [formation](#formations). `--model` and `--name` fix the child's model and
alias/title the same way they do on `branch`.

`--role <r>` reads a committed role prompt from `roles/<r>.md` (the spawn
cwd's git repo first, then `$CBUS_DIR/roles` as a machine-global fallback) and
appends its body to the child's first turn, after the join/arm instructions.
It defaults `--name` to the role name and `--model` to the file's `MODEL:`
line; an explicit `--name`/`--model` still wins. An unknown role fails before
any alias is reserved, listing every path it tried. `branch` refuses `--role`
outright — a fork inherits its parent's intent, and handing a forked peer
someone else's role prompt is exactly the ghost-orchestrator failure
formations exist to prevent (see below).

### Put two already-open sessions on a channel

Run `/bus-join` in each window — both default to the channel named after the
repo they're in, so two sessions in the same repo find each other with no pairing
step. For cross-repo pairs, pass the same channel name explicitly:

```
# window 1 (any repo)
/bus-join deploy laptop

# window 2 (any repo)
/bus-join deploy server
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
Join it from a session meant to oversee everything (`/bus-join global` or
`cbus join global`), and any session on the machine can reach it with
`cbus send global/<alias> "..."` regardless of what channel it works in. A session
can be in several channels at once (e.g. its repo channel *and* global) — arm one
Monitor per membership.

### More than two

A channel is an N-way registry, not a pair. Every session that joins the same
channel can message every other; aliases are auto-assigned and recycled
(`main`, then `fork-1`, `fork-2`, … reusing freed slots).

### Presence & session-end announcements

`join` / `leave` / `rename` / `departed` events are broadcast automatically to every
non-dead peer in the channel as `kind=presence` messages (replayed at a
joined-but-unarmed peer's first arm). Presence is **local-only** — it does not cross
the relay. A SessionEnd hook (`cbus hook-exit`, wired manually in
`~/.claude/settings.json`) announces graceful exits immediately; hard kills fall back
to the lazy prune's `departed` broadcast.

## Formations

A **formation** is a saved snapshot of a channel's shape: its peers, their
roles and models, and how to relaunch them — so a whole multi-session fleet
(an orchestrator plus a coder, reviewer, and documenter, say) can be brought
back after a reboot, handed to a successor, or stamped out fresh for a new
effort, instead of rebuilt by hand one `/bus-join` at a time.

```sh
cbus formation save myeffort              # snapshot this channel's peers
cbus formation show myeffort              # inspect it — stale sids, TODO roles
cbus formation apply myeffort --dry-run   # preview the relaunch plan
cbus formation apply myeffort             # relaunch the peers that are missing
cbus formation bootstrap myeffort coder   # print one peer's first-turn prompt
cbus formation list                       # every saved formation
cbus formation rm myeffort                # delete a saved formation
```

- **`save`** records only what the bus actually knows about each peer — alias,
  session id, cwd, machine — plus whatever the launcher recorded when the peer
  was born (see *Birth records*, below). Everything else (a hand-picked role,
  notes, narrative) is yours to fill in, and a later save never overwrites a
  hand-edited field.
- **`apply`** relaunches exactly the peers missing from the channel,
  sequentially and anchor-first (the orchestrator comes up before anyone who
  expects to reach it). Convergence is a round-trip, not a timer: each kickoff
  carries a nonce and apply reads its own inbox for the answer, so a peer that
  launches but never responds is reported `failed`, not silently counted as
  up. `--dry-run` builds the exact same plan without launching anything;
  `--only a,b` narrows it; `--channel <ch>` retargets a formation (including a
  starter template, see below) at a different channel for one run without
  touching the file; `--brief TEXT` adds an effort brief to every kickoff;
  `--wait <dur>` sets how long to wait for each peer's answer (default 90s).
- **`bootstrap`** prints one peer's first-turn prompt for you to paste by
  hand — the path for a peer `apply` won't launch itself (recorded on another
  machine; cross-machine launch isn't in v1) or for previewing a brief before
  opening a fleet.
- Three restore modes decide *how* a peer comes back, and the modes never
  cross: a session resumed as itself continues its own transcript; a forked
  peer is told plainly that it is not the original and must not act on
  unfinished parent work; a peer whose transcript is gone comes back on a
  fresh one, briefed from its role file. **A peer is never forked across
  roles** — the clearest way to reproduce the original design mistake this
  feature exists to fix (a restored session picking up a different role than
  the one it was saved as, and acting on stale intent under someone else's
  name).

### Starter templates

The repo ships `formations/dev-trio.json`, a four-role starter (orchestrator,
coder, reviewer, documenter) with no session ids and no models — models come
from each role file's `MODEL:` line at apply time. `cbus formation apply
dev-trio --channel myeffort` works from any checkout with an empty local
store. A formation name resolves against your own saved formations first,
then the repo's committed starters — a runtime save shadows a committed
starter of the same name, and `apply`/`show` print which source they used so
a shadow is stated, not a surprise. `rm` and `save` only ever touch your
local store: `rm` of a committed starter is refused (delete it with `git rm`
instead), and a `save` that inherits fields from a starter template still
writes your local copy, never the repo file.

### Birth records

`spawn` and `branch` stamp how a peer was born — `fresh` or `fork` — plus its
model, into the peer's registry entry (`meta.json`) at launch time, before
the child even boots. `formation save` picks these up automatically, so a
spawn-born peer saves with its origin and model already filled in and can be
resumed later with no hand-edit. This is deliberately launcher-side: a
session cannot reliably know its own origin, but the process that launched it
always does.

### The `/bus-formation` skill

`commands/bus-formation.md` wraps all of this in one slash command —
`/bus-formation save myeffort`, `/bus-formation apply dev-trio --channel
myeffort --dry-run`, and so on — for driving formations from inside a Claude
Code session rather than shelling out directly.

## Networked relay (NUC)

`relay/` holds `cbus-relay`, a std-lib-only Go daemon that extends the bus across
machines (shipped; the client speaks to it via `<channel>@<host>` addresses —
see below):

> **Cross-machine messaging requires a relay.** The local file bus (see *How it
> works*) never leaves one machine; anything crossing a machine boundary goes
> through a `cbus-relay` daemon, which is the shared rendezvous point. **One relay
> serves every participating machine** — you don't run one per host. Adding a
> machine to the mesh means pointing it at the *existing* relay, not standing up a
> new one: set `CBUS_SITE_<HOST>_URL` to the relay's base and `cbus auth set
> <host>` with its bearer (plus CF Access service-token if it's behind a tunnel),
> then address `<channel>@<host>/<alias>`. A machine only needs its *own* relay if
> you want other machines to address channels *hosted on it* (`@that-host`) —
> uncommon. The relay host itself reaches its channels over loopback and needs no
> `CBUS_SITE_*` override; every other client does.

- **`POST /send`** (bearer token) appends `{from,to,ts,text}` — the exact local
  inbox shape — to a Maildir spool (`spool/<channel>/<alias>/{tmp,new,cur}`).
- **`GET /tail?channel=&alias=`** upgrades to WebSocket, authed via
  `Sec-WebSocket-Protocol: bearer.cbus.<token>` (k8s-apiserver pattern — the
  Claude Code Monitor `ws:` source can't send headers). Replays queued messages,
  then streams; delivered messages move `new/` → `cur/` (at-least-once).
- **`GET /peers`** (bearer) — presence/queue depth; liveness = relay presence +
  30s/90s ping heartbeat, not pids. **`/healthz`** — unauthenticated.
- Runs as systemd unit `cbus-relay` on the NUC, loopback `127.0.0.1:8090`,
  fronted by the CF tunnel. Deploy with `relay/deploy.sh` (builds on the NUC).
- One active tail per peer: a new `/tail` displaces the old (per-message
  displacement checks; delivery is at-least-once — a narrow handover race can deliver
  one in-flight message to both tails).

### Using remote channels from cbus

The client speaks to the relay through the `<channel>@<host>/<alias>` address
form. Each `<host>` resolves from its `CBUS_SITE_<HOST>_URL` env var — there are
no built-in hosts (the examples below use `nuc`):

```sh
# seed the macOS Keychain — ONE credential per invocation (each '-' reads ALL of stdin,
# so the three can't share one line); values piped from 1Password:
op read 'op://…/relay-bearer'  | cbus auth set nuc --token -
op read 'op://…/cf-client-id'  | cbus auth set nuc --cf-id -
op read 'op://…/cf-secret'     | cbus auth set nuc --cf-secret -
cbus send dev@nuc/nuc "build finished"                # POST /send — queues if the peer is offline
cbus tail dev@nuc/mbp                                 # prints the Monitor ws arm spec + claims 'mbp' as your identity
cbus list @nuc                                        # peers the relay knows: connected / queued / lastSeen
cbus leave dev@nuc                                    # drop THIS session's identity marker
```

Details that matter:

- **Aliases are explicit** — pick a short hostname/role (`mbp`, `nuc`, `ci`).
  There's no remote registry; a taken alias is self-evident because the relay
  keeps one active tail per peer (your Monitor visibly drops if displaced).
- **Endpoint autodetects**: a session on the relay host probes
  `127.0.0.1:8090/healthz` and talks loopback with no CF Access; everyone else
  goes through the host's `CBUS_SITE_<HOST>_URL` (e.g. `https://bus.example.com`)
  with CF Access service-token headers.
- **Credentials are never in code**: `cbus auth` stores them in the macOS
  Keychain (`security(1)`) or, on Linux, 0600 files under `~/.config/cbus/`.
- **Receive is Monitor-native**: remote `tail` prints the `Monitor {ws:}` arm
  spec (URL + `bearer.cbus.<token>` subprotocol) rather than exec'ing a
  process — the session arms it, and messages arrive as turn events exactly
  like local ones.
- Arming a remote tail records a **session-scoped identity marker**
  (`.remote/<host>/<channel>/<sessionId>` = `{alias, ownerPid, ts}`) so *this
  session's* later sends on that channel auto-fill a routable `from`. Sessions
  never inherit each other's aliases (no cross-session impersonation); a
  session without its own marker falls back to `hostname-PID` (unroutable —
  same caveat as local unjoined senders). Markers carry the owning `claude`
  pid, so `cbus prune` sweeps them when their session dies. A marker is a
  from-default, **not** proof of reachability — `cbus list <ch>@<host>` is the
  truth source for who is actually connected.
- **Relay presence** — the relay pushes `join`/`departed` presence to connected
  peers on a channel (server-side, cbus-ijx.5), so a session tailing a relay
  channel is notified when a peer arms or drops, like on a local channel. It is
  connection-lifecycle, not registration: `join` fires on ws attach, `departed`
  ~90s after a tail drops (a grace window that debounces sleep/wake re-arms).
  `/peers` stays the state truth source; the pushed events are edge notifications.
  Delivery is connected-only — offline roster catch-up stays `cbus list`.
- **Relay peers are append-only** — the spool creates a peer's maildir on its
  first queued message and never GCs it, so an off peer lingers in
  `cbus list <ch>@<host>` forever (`off`, `queued 0`). The relay holds no pid to
  test liveness on, so local `cbus prune` can't reach it. `cbus prune <ch>@<host>`
  (or bare `@<host>`) reaps those from the server side: it drops every peer that
  has no live tail **and** no queued mail — a peer with pending mail is always
  kept, so nothing undelivered is lost.

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
                                 (local only — ignored on @host targets: the relay spool always queues)
cbus list [--active] [channel]   peers with listen/off state, host, cwd
cbus active [channel]            only peers currently listening (= list --active)
cbus channels                    channels with peer counts
cbus whoami                      local memberships + remote identity markers (exit 1 if none)
cbus inbox <channel>/<alias>     print inbox path
cbus bootstrap <channel> [parent]  print the canonical fork-child prompt
cbus branch [target] [channel]   join + fork a bootstrapped child in one shot
     --model <m>                 launch the child on a specific model
     --name <n>                  fix the child's alias AND session title
cbus spawn [target] [channel]    open a FRESH session (blank transcript, not
                                 a fork) that joins + arms itself
     --model <m>                 launch the child on a specific model
     --name <n>                  fix the child's alias AND session title
     --role <r>                  append the committed role prompt roles/<r>.md
                                 to the child's first turn; defaults --name to
                                 the role and --model to its MODEL: line
                                 (spawn-only — branch refuses --role)
cbus formation save <name> [ch]  capture a channel's topology (model/role/
                                 origin/profile are hand-maintained, except
                                 origin+model when the launcher stamped them)
cbus formation apply <name>      relaunch a formation's MISSING peers here;
                                 name resolves runtime-first, then the repo's
                                 formations/ starter templates
     --channel ch                target ch for this run (a template serves
                                 any effort; the envelope file is unchanged)
     --dry-run                   print the plan, launch nothing
     --only a,b                  only these peers
     --wait <dur>                how long to wait for each peer to answer
                                 (default 90s; 0 = launch and return)
     --brief TEXT                effort brief added to every peer's kickoff
cbus formation bootstrap <name> <alias> [--brief TEXT]
                                 print ONE peer's first-turn prompt to paste
                                 by hand (the path apply won't launch itself)
cbus formation list              saved channel topologies ($CBUS_DIR/.formations)
cbus formation show <name>       one formation's peers, flagging stale sids
                                 and TODO roles
cbus formation rm <name>         delete a saved formation (runtime only —
                                 a committed starter refuses: use git rm)
cbus prune [channel]             remove dead peers (and empty channels); a bare
                                 `cbus prune` also sweeps dead remote identity markers
cbus hook-exit                   SessionEnd hook target: announce departure (always exit 0)
cbus --version                   print the installed client version

remote (relay-backed) — address form <channel>@<host>/<alias>:
cbus send <ch>@<host>/<al> TEXT  POST to the relay (queues if peer offline)
cbus tail <ch>@<host>/<al>       print Monitor ws arm spec + claim identity
cbus list [<ch>]@<host>          relay peers: connected / queued / lastSeen
cbus prune [<ch>]@<host>         reap off relay peers with no queued mail
                                 (channel-scoped; omit <ch> to sweep the host)
cbus leave <ch>@<host>           drop THIS session's identity marker
cbus auth set <host> [--token V] [--cf-id V] [--cf-secret V]   ('-' = stdin)
cbus auth status [host]          credential state, masked
cbus leave [channel]             leave channel(s) this session joined
cbus rename <new-alias> [channel]  rename this session's local alias (re-arm after)
cbus unregister <channel>/<alias>  force-remove any peer

env: CBUS_DIR (default ~/.claude-bus); CBUS_SITE_<HOST>_URL / CBUS_RELAY_LOCAL_URL
     (relay endpoint overrides); CBUS_ALIAS (last-resort local from)
```

`--help` still prints a vestigial `CBUS_PYTHON` line for byte-parity with the bash client; the
Go client ignores it. A `--` terminator ends flag parsing; flags must precede message text.

`cbus register <alias>` is kept as a deprecated v1 alias for `cbus join global <alias>`.

## Security & network model

claudebus deliberately aims to be a **trust boundary, not a security boundary**, and the
design is honest about that line.

- **Local bus — trust boundary, not a sandbox.** Everything under `~/.claude-bus` is
  readable/writable by anything running as your user; any such process can append to any
  inbox and set `from` to whatever it likes. There is *no* sender authentication, by design.
  The guardrail lives on the receiving side: Claude Code treats an incoming bus message as an
  untrusted peer request that can't escalate permissions (and delivery can wake an idle
  session — see Caveats). Safe on your own machine; unsafe beyond it. Don't put `~/.claude-bus`
  on a shared or networked filesystem.
- **Cross-machine relay — keep it off the open internet.** The relay is a single-operator
  service with **no multi-tenant auth**. It must only be reachable either (a) on a trusted
  LAN/tailnet, or (b) through an **authenticated Cloudflare tunnel with service-token keys** —
  which is how this deployment runs it: the daemon binds `127.0.0.1` only, fronted by CF Access.
  `POST /send` sits behind a CF Access **service token** *and* the relay's own bearer (a request
  must clear both edge and origin); `GET /tail` uses a CF Access **bypass** scoped to that path
  only (the Monitor `ws:` client can't send Access headers), with auth carried in
  `Sec-WebSocket-Protocol: bearer.cbus.<token>`. All keys live in the macOS Keychain / `0600`
  files via `cbus auth` — never in code, argv, or the repo. Do **not** expose `:8090` directly:
  without the tunnel + Access in front, anyone reaching it with the bearer can read/inject on
  any channel.
- **Identity is a convenience, not a credential.** `from` is spoofable (local and remote). The
  session-scoped remote marker prevents *accidental* cross-session impersonation, but it is not
  auth. `cbus list <ch>@<host>` reports who's actually connected; a marker is only a from-default.
- **What it deliberately does not do:** no encryption at rest beyond filesystem permissions, no
  multi-user isolation, no message signing, no broadcast. It's a coordination bus for one
  operator's machines, not a shared messaging service.

## Caveats

- **No sender authentication.** Anything that can write to `~/.claude-bus` can inject a
  message, and `from` is taken at face value. Claude Code treats an incoming bus message
  as an untrusted peer request (no permission escalation), which is the right guardrail —
  but don't expose the bus directory beyond your own machine.
- **Channels are namespaces, not isolation.** Any local process can send to any channel;
  the channel only scopes addressing and cleanup.
- **Delivery is push — an idle session wakes and can act autonomously.** A Monitor
  event re-invokes the receiving agent on its own: a session sitting idle at the
  prompt processes the message (and can reply) with no human present. Only a *busy*
  session defers — the event queues until its current step completes rather than
  interrupting it. Corollary: a peer message can trigger action while you're away,
  which is why incoming messages are treated as untrusted peer requests.
- **No broadcast primitive.** `cbus send` targets one peer; message N times to reach N peers.
- **No runtime dependencies.** The client is a single static Go binary (the bash-era
  python3 and `tail -F` requirements are gone).
- **Message size cap:** messages over 1 MiB are rejected, matching the relay's `/send` cap.

## Why not the built-in teammate mailbox?

Claude Code's teammate `SendMessage` is **closed by design**: teammates are spawn-bound
subprocesses, registered with their parent session **in-process at spawn time**. There
*are* team files on disk (`<config>/teams/session-<sid>/` — config, inboxes), but they are
not a delivery path: a hand-launched `claude` process with matching `--team-name` /
`--parent-session-id` flags comes up alive yet is unreachable via SendMessage, and writing
to the inbox files registers nothing (verified empirically). A session can only message
agents it forked itself — there is no cross-session addressing at all. That closed
boundary is exactly what claudebus provides: an open, file-based channel any process,
window, or CCS profile can append to, built from stable documented primitives with a
liveness-aware registry. The two compose: SendMessage for in-session fan-out, cbus for
session-to-session.

## License

[MIT](LICENSE) — © 2026 Carlos Umanzor.

A fun, single-operator personal project. Fork it, read it, take ideas from it — that's
what it's here for. MIT only asks that you keep the copyright line. If it ends up in
something commercial, a little attribution is appreciated (not required). No support,
warranty, or contribution process is implied — see the *bespoke by design* note at the top.
