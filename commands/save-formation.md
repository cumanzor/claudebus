---
description: Checkpoint this session's cbus channel as a formation, then triage what the bare save left unfilled
argument-hint: "[name] [channel] [--anchor tracker=<id>]"
allowed-tools: Bash(cbus:*), Bash(git rev-parse:*)
---

Zero-friction checkpoint of the fleet this session is sitting in: save the
channel's topology, then say what the save left unusable for a later `apply`.
Read-mostly. It writes one envelope file and reports. It never launches, kills,
renames, or edits a peer.

The user passed: "$ARGUMENTS", and everything in it is optional. First bare word is the
formation name, second bare word is the channel, and flags pass through to
`cbus formation save`.

1. **Resolve the channel.** Run `cbus whoami`. It prints this session's
   channel/alias registrations and exits 1 when this session is not joined.
   - Joined to exactly one local channel: use it, no questions.
   - Joined to several: ask which one. Do not guess, a wrong channel writes a
     formation full of peers that belong to another effort.
   - Not joined (exit 1): fall back to the git repo basename
     (`basename $(git rev-parse --show-toplevel)`, sanitized to
     `[A-Za-z0-9._-]`), else `global`. Say which fallback you used: an unjoined
     save records a channel this session is not part of, which is fine for a
     drive-by checkpoint and wrong if the user meant their own fleet.

   The formation name defaults to the channel name. An explicit first argument
   wins. Pass the channel to `save` only when it is not this session's own,
   since the argument defaults to it.

2. **Save.** Run `cbus formation save <name> [channel] [--anchor key=value ...]`.

   A save refreshes an existing file in place and preserves hand-edited fields.
   It records alias, sessionId, cwd, machine, profile when the session stamped
   one, and origin/model when the launcher recorded a birth-record. It reports
   `+N new` and `N kept, not on the channel now`.

   Drift anchors: the convention is `--anchor tracker=<id>`, which links the
   effort's tracker item so a later apply can diff what moved. `git_head` is
   machine-owned and stamped for you. If the effort has a tracker epic and the
   envelope carries no `tracker` anchor yet, offer to add it.

3. **Show and triage.** Run `cbus formation show <name>` and surface the things
   that bite on restore. Report them per peer, by name, not as a count.

   **`role: TODO`**: a bare save does not fill rolefile/role, so `apply` would
   brief that peer with nothing and it would come back with no idea what the
   effort is. Fix one of two ways: point its `rolefile` at
   `$CBUS_DIR/roles/<role>.md` (the set `cbus install-roles` places: coder,
   documenter, orchestrator, reviewer), or write freeform role text into that
   peer's `role` field in `$CBUS_DIR/.formations/<name>.json`. Name every
   affected peer.

   **Stale sids**: `STALE, no transcript found on this machine`. Those peers
   cannot resume or fork, `onStale=template` applies and they come back fresh,
   losing their context. Flag them so the user decides now whether the seat is
   still worth a template restart.

   **`kept, not on the channel now`**: seats preserved from an earlier save that
   are not live right now. Usually correct, a peer closed on purpose stays in
   the topology. State it anyway so an accidental drop gets caught here instead
   of at restore time.

   **Per-peer `mode`**: `resume` brings the session back as itself, `template`
   starts it fresh from its role. A peer whose transcript is present is usually
   more useful resumed, so call out any `mode=template` peer that still has a
   live sid.

   **The anchor peer**: `apply` launches it first and `cbus formation resume`
   needs it. Say which peer it is, and flag it if show reports no anchor.

4. **Report** in a few lines: formation name, channel, peer count, new vs kept,
   then only the items that need a decision, each tied to its peer. If show
   reports no warnings and no TODO roles, say the checkpoint is clean and stop.

Never run `apply`, `resume`, `bootstrap`, or `rm` from here, and never arm a
Monitor. If the user wants to relaunch or delete a formation, send them to
`/bus-formation`.

Do nothing else.
