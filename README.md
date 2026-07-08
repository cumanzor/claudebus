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
  **Monitor** tool (persistent). `tail` `exec`s in place so *its own pid* becomes
  the liveness signal. Each incoming line arrives in that session's conversation
  as a JSON event `{"from","to","ts","text"}` (addresses in full `channel/alias`
  form) — push delivery: an idle session is woken immediately; a busy one sees it
  when its current step completes.
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
| `commands/bus-join.md` | `~/.claude/commands/bus-join.md` | slash command to join a channel |
| `commands/bus-branch.md` | `~/.claude/commands/bus-branch.md` | slash command to fork + auto-join both sides |
| `commands/bus-rename.md` | `~/.claude/commands/bus-rename.md` | slash command to rename this session's alias |

Make sure `~/.local/bin` is on your `PATH`.

> **Paths:** `cbus branch` finds the fork helper at `~/.claude/bin/cc-branch.sh`
> (override with `CC_BRANCH=/path`). `cc-branch.sh` itself relaunches through
> `ccs <profile>` when it detects a CCS config dir — drop that branch if you don't use
> CCS and just call `claude` directly.

## Usage

### Fork a session with the bus pre-wired

```
/bus-branch window            # or: tab | tmux — channel defaults to the repo name
/bus-branch window mytask     # explicit channel name
```

Runs `cbus branch <target> [channel]` — a single command that joins the parent
to the channel and forks the conversation into a new terminal (via
`cc-branch.sh`) with the canonical bootstrap turn — then arms the parent's
listener. The child self-joins the same channel, arms its own listener, and
announces itself to the parent, reporting results back with `cbus send` instead
of a handoff doc.

The child resumes the parent's transcript at boot, so it sees the parent's live
Monitor as one harmless "no completion record" background-task note. This is
cosmetic and unavoidable — the transcript is read when the child starts, always
after the parent armed — and the bootstrap prompt tells the child to ignore it.

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

## Networked relay (NUC)

`relay/` holds `cbus-relay`, a std-lib-only Go daemon that extends the bus across
machines (epic in progress; the cbus client side lands separately):

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
  displacement checks — no duplicate delivery on handover).

### Using remote channels from cbus

The client speaks to the relay through the `<channel>@<host>/<alias>` address
form (one host today: `nuc`):

```sh
cbus auth set nuc --token - --cf-id - --cf-secret -   # seed macOS Keychain (values from 1Password; '-' reads stdin)
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
  goes through `https://bus.example.com` with CF Access service-token headers.
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
cbus branch [target] [channel]   join + fork a bootstrapped child in one shot
cbus prune [channel]             remove dead peers (and empty channels)

remote (relay-backed) — address form <channel>@<host>/<alias>:
cbus send <ch>@<host>/<al> TEXT  POST to the relay (queues if peer offline)
cbus tail <ch>@<host>/<al>       print Monitor ws arm spec + claim identity
cbus list [<ch>]@<host>          relay peers: connected / queued / lastSeen
cbus leave <ch>@<host>           drop THIS session's identity marker
cbus auth set <host> [--token V] [--cf-id V] [--cf-secret V]   ('-' = stdin)
cbus auth status [host]          credential state, masked
cbus leave [channel]             leave channel(s) this session joined
cbus rename <new-alias> [channel]  rename this session's local alias (re-arm after)
cbus unregister <channel>/<alias>  force-remove any peer

env: CBUS_DIR (default ~/.claude-bus), CBUS_PYTHON (default python3),
     CC_BRANCH (fork helper path, default ~/.claude/bin/cc-branch.sh)
```

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
- **Requires `python3`** (used only for robust JSON read/write) and a BSD/GNU `tail` with `-F`.

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
