---
description: Save, inspect, or relaunch a cbus formation — a channel's saved peer topology
argument-hint: "save <name> | show <name> | apply <name> [--dry-run] | bootstrap <name> <alias> | list | rm <name>"
allowed-tools: Bash(cbus:*), Monitor
---

A **formation** is a saved snapshot of a channel's shape: its peers, their roles
and models, and how to relaunch them. Use it to bring an effort back after a
reboot, to hand a whole fleet to a successor, or to stamp out an equivalent one.

The user passed: "$ARGUMENTS" — first word is the verb, second is usually the
formation name. Run the matching `cbus formation` command and report its output.
Do not add verbs: the surface is exactly `save | apply | bootstrap | list | show | rm`.

**save `<name>` [channel]** — capture this channel's peers. Channel defaults to
this session's own. It records only what the bus knows (alias, sessionId, cwd,
machine); model, role, origin and profile are hand-maintained — say so, and point
the user at `cbus formation show <name>` to see what still needs filling in.

**show `<name>`** — the peers, flagging stale sids (the recorded transcript is
gone) and TODO roles. Read this before applying anything.

**apply `<name>`** — relaunch the peers that are MISSING, sequentially, anchor
first. Preconditions worth checking before you run it:
- This session must be joined to the formation's channel: peers are briefed to
  answer THIS address, so `cbus join <channel> <alias>` first, and arm your
  Monitor **before** applying — a peer can answer before apply returns.
- **Prefer `--dry-run` first** and show the user the plan. It launches nothing and
  builds the plan exactly as a real apply does.
- `--only a,b` narrows it; `--wait <dur>` sets how long to wait for each peer to
  answer (default 90s, `0` = launch and return).

Apply opens terminal windows. Unless the user clearly asked for the launch, run
`--dry-run` and let them confirm before the real one.

It reports per peer: `present` (already live — left alone), `resumed`, `forked`,
`templated`, `degraded` (its transcript was gone, so it came back fresh),
`skipped`, `refused`, `failed` (launched but never answered). A `refused` line
always carries its reason — relay it verbatim; those are the prohibitions that
stop a peer being restored as the wrong session, and they are worth reading, not
working around.

**bootstrap `<name> <alias>` [--brief TEXT]** — print ONE peer's first-turn prompt
for the user to paste by hand. This is the path for a peer `apply` will not launch
(one recorded on another machine — cross-machine launch is not in v1).

**rm `<name>`** — delete the saved formation. It may hold hand-authored notes, so
confirm with the user first.

Report what happened in one line, plus any refused/failed peers and their reasons.
Do nothing else.
