# Usage

These instructions use native Claude receive, which requires a cbus build with
the Claude adapter (v0.12.2 supports native Codex only). See [Claude
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
`cbus list CHANNEL` once. The branch command preserves the managed parent,
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
It defaults `--name` to the role name and `--model` to the file's `MODEL:`
line; an explicit `--name`/`--model` still wins. An unknown role fails before
any alias is reserved, listing every path it tried. `branch` refuses `--role`
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
Incoming presence updates that observed roster; no recurring roster check is
needed. `cbus list` shows peers across channels; `cbus channels` summarizes them.

`socket-ready` means the Claude endpoint is available, not that a message arrived.
A successful send is submission; an exact transcript receipt confirms arrival,
and a peer reply supplies separate evidence of action. Use
`cbus connection status deploy/laptop --json` or
`cbus connection reconcile deploy/laptop --json` on demand. Busy sessions process
input after their turn; hold/refuse policy still applies. Stop future delivery
with `cbus connection disconnect deploy/laptop`, which retains the inbox.

## The global channel

`global` is an ordinary channel with a reserved meaning: the machine-wide bus.
Join it from a session meant to oversee everything (`/bus-join global` or
`cbus connect global [alias] --json`), and any session on the machine can reach it with
`cbus send global/<alias> "..."` regardless of what channel it works in. A session
can connect to several channels (e.g. its repo channel *and* global). The daemon
manages those connections; do not add a Monitor for each membership.

## More than two

A channel is an N-way registry, not a pair. Every session that joins the same
channel can message every other; aliases are auto-assigned and recycled
(`main`, then `fork-1`, `fork-2`, … reusing freed slots).

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
