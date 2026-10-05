# Usage

These instructions use native Claude receive, shipped since cbus v0.13.0 for
macOS and Linux alongside native Codex receive. See [Claude
connections](claude.md) for supported sessions, recovery and deliberate migration
from an existing Monitor peer. Native Claude and Codex connections do not need a
Monitor, tail process or periodic model polling; terminal choice is independent.

## Fork a session with the bus pre-wired

```
/bus-branch window            # or: tab | tmux | pane — channel defaults to the repo name
/bus-branch window mytask     # explicit channel name
```

`/bus-branch` first connects the parent natively, then runs
`cbus branch <target> [channel]`. When running that CLI command directly, connect
the parent first with `cbus connect CHANNEL [ALIAS] --json` and check
`cbus list CHANNEL` once. On an unconnected parent, `branch` falls back to a
legacy registration and prints a Monitor-arming hint; do not follow that hint,
connect natively instead. The branch command preserves the managed parent,
reserves the child alias and forks into an iTerm2 or tmux terminal with the
canonical bootstrap turn. The child connects its own session natively and
reports back with `cbus send`. Neither side arms a Monitor. Any background-task
note inherited from an older transcript is historical; do not restart it.

## Open a fresh session instead of forking

```
cbus spawn tab                              # fresh session, connects itself natively
cbus spawn tab mytask --model opus --name coder
cbus spawn tab formations --role documenter  # role prompt rides the first turn
```

`spawn` is `branch`'s fresh-transcript sibling: same terminal launch
(window/tab/tmux/pane) and the same native-connect bootstrap, but the child
starts blank instead of resuming the parent's conversation — the right choice
when a peer shouldn't inherit the parent's history, such as a distinct role in
a [formation](formations.md). `--model` and `--name` fix the child's model and
alias/title the same way they do on `branch`.

`--role <r>` reads a committed role prompt from `roles/<r>.md` (the spawn
cwd's git repo first, then `$CBUS_DIR/roles` as a machine-global fallback) and
appends its body to the child's first turn, after the connect instructions.
It defaults `--name` to the role name. Claude spawn also defaults `--model` to the
file's `MODEL:` line; an explicit `--name`/`--model` still wins. Codex spawn uses
explicit `--model`, otherwise its Codex profile/default, not the Claude role model.
A `--model` string passes through to the CLI unchanged: an alias such as `opus`
floats to whatever that CLI currently resolves it to, while a role's `MODEL:`
line is a pinned id, so the two can diverge.
An unknown role fails before any alias is reserved, listing every path it tried. `branch` refuses `--role`
outright — a fork inherits its parent's intent, and handing a forked peer
someone else's role prompt is exactly the ghost-orchestrator failure
formations exist to prevent (see below).

## Put two already-open sessions on a channel

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

After connecting, each session checks `cbus list deploy` once and reports the
other listening peers and explicitly known roles. Aliases are not role evidence.
For a native peer, listening means the daemon holds the connection, not that
its CLI session is running; session presence is `consumer.state` in
`cbus connection status CHANNEL/ALIAS --json`, and mail to a departed native
session queues for its resume. Incoming presence updates that observed roster;
no recurring roster check is needed. `cbus list` shows peers across channels;
`cbus channels` summarizes them.

For a local native connection, the roster's PID (`listenerPid` in `cbus list
--json`) identifies the cbus daemon, so several peers can share it. To identify
the harness session, use `cbus connection status CHANNEL/ALIAS --json`: `threadId`
is the session ID and `consumer.pid` is the observed CLI process. Read that PID
together with `consumer.state`, `consumer.startToken` and `consumer.observedAt`;
a retained PID alone does not prove the session is still running. Legacy Monitor
peers instead use `listenerPid` for their tail process.

`socket-ready` means the Claude endpoint is available, not that a message arrived.
A successful send is submission; an exact transcript receipt confirms arrival,
and a peer reply supplies separate evidence of action. Use
`cbus connection status deploy/laptop --json` or
`cbus connection reconcile deploy/laptop --json` on demand. Busy Claude sessions
can receive input between foreground tool calls; hold/refuse policy still applies.
Stop future delivery with `cbus connection disconnect deploy/laptop`, which retains the inbox.

A connection ends with its session. Once the consumer process (the Claude Code
or Codex CLI the connection is bound to) has exited, checked by pid and start
time rather than by a stale "online" observation, the daemon collects the
connection after a short grace (15 minutes, measured from the session's last
transcript or rollout write). A pending delivery attempt does not hold it: the
uncertain message is past the delivered offset, so it lands in the archive's
unread mail. A record left by `leave` or `unregister` is collected on the next
pass. A record with no consumer process on file falls back to 14 days of
inactivity, and one with a pending attempt and no consumer on file is never
collected; `cbus connection gc --dry-run` names the `reconcile` or `abandon`
command it needs.

The daemon runs a pass a minute after it starts, so a reboot's dead sessions go
as soon as their grace has passed, then every 5 minutes. `cbus connection gc`
runs one now, and `--dry-run` shows the same classification without changing
anything. `/health` reports the last automatic pass under `lastGC`, and each
collection is a line in the daemon log.

Collection runs under each record's lane and peer lock and holds the connect
gate for the pass, so a session resuming at the same moment either finishes
first and is kept, or connects afresh afterwards. A collected record moves to
`.daemon/connections/.archive/YYYY-MM/<id>/` with its inbox folder (only when
the record still owns it; a newer connection's folder stays) and an
`unread.jsonl` of the lines past its delivered offset. Its session token is
deleted, not archived, and tokens no record references any more are removed in
the same pass. A pass that fails partway leaves the record for the next one.
Archives are deleted after 30 days.

When a resumed session connects and a record of the same session on the same
channel was collected while it was down, `cbus connect` says so on stderr and
names that archive's `unread.jsonl` (`archivedPredecessor` in `--json` output).

Settings, read by the daemon at start and by the CLI: `CBUS_GC=off` stops the
automatic passes, `CBUS_GC_GRACE` (default `15m`), `CBUS_GC_INACTIVE` (default
`14d`) and `CBUS_GC_ARCHIVE` (default `30d`). The CLI also takes `--grace` and
`--older-than` for one run.

Over a relay, collection closes the dead peer's subscription, so the relay
sends `departed` to the channel. Mail sent to that alias afterwards waits in
the relay's per-alias queue and reaches whichever connection next subscribes as
that alias.
To end the peer's session as well, `cbus close deploy/laptop` signals the
Claude or Codex process bound to that connection, after disconnecting it,
and then closes its tmux pane or iTerm2 tab. It never signals the daemon,
keeps the inbox and the registration, and refuses when it cannot prove the
process is still that peer's session. The checks and the signal are separate
system calls, so this narrows but cannot fully exclude a process that exits
and is replaced in between.

## The global channel

`global` is an ordinary channel with a reserved meaning: the machine-wide bus.
Join it from a session meant to oversee everything (`/bus-join global` or
`cbus connect global [alias] --json`), and any session on the machine can reach it with
`cbus send global/<alias> "..."` regardless of what channel it works in. A session
can connect to several channels (e.g. its repo channel *and* global). The daemon
manages those connections; do not add a Monitor for each membership.

## More than two

A channel is an N-way registry, not a pair. Every session that joins the same
channel can message every other; aliases are auto-assigned, and for legacy
join peers, recycled (`main`, then `fork-1`, `fork-2`, … reusing freed slots).
A native alias is held (its inbox retained) until an explicit `leave`/`unregister`,
even after disconnect; `cbus prune` skips native peers and only sweeps legacy ones.

## The daemon

`connect` starts the daemon on demand; it is not installed as a login service.
There is one daemon per `$CBUS_DIR`, shared by every peer using that store.
Its socket is `$CBUS_DIR/.daemon/control.sock` and its log is
`$CBUS_DIR/.daemon/daemon.log`. A connecting client sends its machine's
label (`$CBUS_HOST` or the system hostname); the daemon records that label,
falling back to its own only when an older client sends none.

```sh
cbus daemon status [--json]   # pid, version, protocol
cbus daemon restart           # load a new binary; pending mail is retained
cbus daemon stop              # stop it; the next connect starts a fresh one
```

A daemon whose version or protocol differs from the binary, even by a patch,
refuses new connects and `daemon start` rather than being silently reused; see
[install.md](install.md) for the exact refusal text. `send`/`list` do not go
through that check.

The daemon journals each inbox by device number, inode and delivery offset.
On macOS a reboot can give the volume a new device number while the file
keeps its inode (measured on macOS). Per the Linux kernel's own documentation,
a btrfs subvolume, a device-mapper remap or an overlay mount can change a
file's device number the same way; this project has not measured that at
runtime. On macOS and Linux a change of device number alone is accepted when
the inode matches and the last consumed record and the pending record are
byte-identical at their journaled offsets. The daemon then saves the new
device number and logs `inbox device changed with the same inode and
matching records; identity re-stamped`. This checks those two records, not
every byte already delivered. The bound Claude transcript, presence
recipients and the Codex rollout also accept a device-only change with the
same inode, but not by that record check: each is matched by its own identity
instead (the transcript's Claude session ID, the recipient's session and
connection IDs with the event ID scan, the rollout's thread ID). On Windows
the volume serial number stays part of the identity.

A different inode, an inbox shorter than the delivered offset, or a changed
record is still refused. The daemon log and `cbus connection status CH/AL
--json` then show `inbox changed or truncated; refusing to rearm an unknown
epoch` (or `replay` on delivery), followed by the recovery. `cbus connection
disconnect CH/AL` stops the retries and keeps the inbox. To resume delivery,
save any unread mail past the journaled offset first, then `cbus unregister
CH/AL` and connect again from that exact session, or connect under a fresh
alias.

## Presence & session-end announcements

Native presence follows the observed CLI session, separately from the daemon or
relay connection. Tell the user the full address that joined, left, departed or
was renamed; preserve known roles, and treat the timestamp as an observation,
not a promise of current availability. Do not acknowledge presence on the bus.
Compaction updates context without implying a membership change. Native managed
aliases cannot currently be renamed in place.

Legacy `join`/Monitor peers retain their listener-based presence and optional
`cbus hook-exit` / `cbus hook-compact pre|post` hooks. Relay presence differs by
transport; see [relay.md](relay.md). Before replacing a legacy membership, follow
the [migration steps](claude.md#migrating-an-existing-monitor-peer): prefer a
fresh alias, or stop only the known Monitor, export unread mail and explicitly
approve its removal before `cbus leave` deletes that inbox. Do not mix transports
for one inbox or blindly replay exported messages.
