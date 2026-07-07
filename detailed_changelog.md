# Changelog (detailed)

## [2026-07-07 00:20:44 UTC] [Core / Liveness / CLI / Commands] Initial release — channel-based session bus with hardened liveness

[Attempt #1]

Initial commit of claudebus: a tiny file-based message bus that lets two or more
live Claude Code sessions talk to each other (e.g. a parent session and a forked
window), built entirely on supported primitives — the `Monitor` tool plus plain
files under `~/.claude-bus` (`CBUS_DIR`).

### Design highlights

- **Named channels.** Store layout is `<channel>/<alias>/{meta.json,inbox.jsonl}`.
  Peers are addressed as `channel/alias`; a bare `alias` in `send`/`tail` resolves
  within the sender's own channel(s). The channel name `global` is reserved by
  convention as the machine-wide bus for a master orchestrator. `/bus-listen` and
  `/bus-branch` default the channel to the current git repo's basename, so two
  sessions in the same repo find each other with no pairing step.
- **Auto-recycled aliases.** `cbus join <channel>` auto-picks `main`, then the
  lowest free `fork-N`; it is idempotent for a session already in the channel and
  prunes dead peers in that channel *before* picking, so alias numbers get reused
  instead of growing forever. A peer that joined but hasn't armed its Monitor yet
  (listenerPid null) gets a 10-minute grace window so auto-prune can't sweep a
  sibling mid-setup.
- **Hardened liveness.** `listenerPid` is the real `tail` process (via `exec`, so
  the recorded pid *is* the listener). Two failure modes are guarded:
  - *pid recycling* — a live pid is only trusted if its process args still
    reference this peer's inbox (`ps -ww -o args=` grep), so an unrelated process
    that inherited the number is not reported as a false `listen`.
  - *crash-orphaned listener* — on arm, `cbus tail` records `ownerPid`, the owning
    `claude` process (found by walking parents from `$PPID`). On a hard kill the
    `tail`/`zsh` subtree orphans together with a still-live pid, but `ownerPid` is
    gone, so the peer reads `off`. (A clean session exit stops the Monitor, which
    kills the tail directly — this covers the abnormal path.)
- **No lost messages during setup.** `join` truncates the inbox and the listener
  replays from line 1 (`tail -n +1 -F`), so anything sent between join and arming
  the Monitor is still delivered.

### [Files Changed]

- `bin/cbus` — the CLI. Subcommands: `join`, `tail`, `send` (`--from`/`--force`),
  `list [--active] [channel]`, `active`, `channels`, `whoami`, `inbox`, `prune`,
  `leave`, `unregister`; `register` kept as a deprecated v1 alias for
  `join global`. Helpers: `find_owner_pid` (walks parents to the `claude` pid) and
  `meta_listener_alive` (pid + inbox-args + ownerPid checks) centralize liveness
  for `list`, `channels`, `send`, `join`'s taken-check, and `prune`.
- `bin/cc-branch.sh` — session fork helper (relaunches through `ccs <profile>`
  when a CCS config dir is detected).
- `commands/bus-listen.md` — `/bus-listen [channel] [alias]`: join a channel and
  arm the Monitor listener.
- `commands/bus-branch.md` — `/bus-branch [window|tab|tmux] [channel]`: join the
  parent, fork the conversation, then arm the parent's Monitor. Ordering matters:
  the fork happens *before* arming so the child's transcript snapshot doesn't
  contain a live background-task record (which showed as a stale "no completion
  record was found" note in forked sessions under the old ordering).
- `install.sh` — copies (or `--link` symlinks) `cbus`, `cc-branch.sh`, and the two
  commands into `~/.local/bin` and `~/.claude/`.
- `README.md`, `CHEATSHEET.md` — docs for the channel model, liveness guarantees,
  and CLI.
- `.gitignore` — ignores runtime `.claude-bus/` state.

### [Possible Ripple Effects]

- v1 flat-registry stores (`<alias>/meta.json` directly under `CBUS_DIR`) are
  detected as "legacy v1 entry" by `list` and removed by `prune`; `register`
  still works as a `join global` alias for any callers using the old verb.
- Liveness now shells out to `ps` per peer during `list`/`channels`/`send` — a
  negligible cost at interactive scale, but not free if scripted in a tight loop.
- `ownerPid` detection depends on a `claude`-named ancestor process; if none is
  found (unusual launcher), it falls back to pid + inbox-args liveness only.

### [Testing Notes]

- Verified the listener process tree under a real Monitor: `tail → zsh → claude`;
  `ownerPid` correctly captured the real `claude` pid.
- pid-recycling case: pointed `listenerPid` at a live non-tail process → reported
  `off` (inbox args don't match).
- crash-orphan case: real tail alive + args match but `ownerPid` killed → `off`;
  `send` correctly refused with "not listening".
- Exercised join/idempotent re-join/explicit-alias-taken, multi-session channel,
  `active` filter, bare-alias vs full-address send, `--force` queueing, `prune`
  grace window + legacy-entry cleanup, dead-listener sweep on next join, and
  `leave`. Round-trip send delivered as a Monitor event end-to-end.
