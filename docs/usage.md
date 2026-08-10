# Usage

## Fork a session with the bus pre-wired

```
/bus-branch window            # or: tab | tmux | pane — channel defaults to the repo name
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

## Open a fresh session instead of forking

```
cbus spawn tab                              # fresh session, joins + arms itself
cbus spawn tab mytask --model opus --name coder
cbus spawn tab formations --role documenter  # role prompt rides the first turn
```

`spawn` is `branch`'s fresh-transcript sibling: same terminal launch
(window/tab/tmux) and the same join-and-arm-on-its-own bootstrap, but the child
starts blank instead of resuming the parent's conversation — the right choice
when a peer shouldn't inherit the parent's history, such as a distinct role in
a [formation](formations.md). `--model` and `--name` fix the child's model and
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

The message pops into window 1's conversation as an event. `cbus list` shows every
peer across channels; `cbus channels` summarizes channels.

## The global channel

`global` is an ordinary channel with a reserved meaning: the machine-wide bus.
Join it from a session meant to oversee everything (`/bus-join global` or
`cbus join global`), and any session on the machine can reach it with
`cbus send global/<alias> "..."` regardless of what channel it works in. A session
can be in several channels at once (e.g. its repo channel *and* global) — arm one
Monitor per membership.

## More than two

A channel is an N-way registry, not a pair. Every session that joins the same
channel can message every other; aliases are auto-assigned and recycled
(`main`, then `fork-1`, `fork-2`, … reusing freed slots).

## Presence & session-end announcements

`join` / `leave` / `rename` / `departed` events are broadcast automatically to every
non-dead peer in the channel as `kind=presence` messages (replayed at a
joined-but-unarmed peer's first arm). Presence is **local-only** — it does not cross
the relay. A SessionEnd hook (`cbus hook-exit`, wired manually in
`~/.claude/settings.json`) announces graceful exits immediately; hard kills fall back
to the lazy prune's `departed` broadcast. PreCompact/PostCompact hooks
(`cbus hook-compact pre|post`, same manual wiring) announce compaction the same
way — `compact-pre`/`compact-post` events, local-only for now — so an
orchestrating peer learns a session is about to lose, or just lost, its
in-context state.
