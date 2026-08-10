# Formations

![a three-peer dev fleet driving itself: the orchestrator joins, spawns coder and reviewer with cbus spawn pane, dispatches a task over the bus, routes the result to review, and announces the verdict](demo-fleet.gif)

*A dev fleet driving itself: the orchestrator's first prompt is the only human
input — it spawns its coder and reviewer as panes with `cbus spawn pane`, waits
for their presence announcements, then runs a task → review → verdict loop
entirely over the bus.*


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

## Starter templates

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

## Birth records

`spawn` and `branch` stamp how a peer was born — `fresh` or `fork` — plus its
model, into the peer's registry entry (`meta.json`) at launch time, before
the child even boots. `formation save` picks these up automatically, so a
spawn-born peer saves with its origin and model already filled in and can be
resumed later with no hand-edit. This is deliberately launcher-side: a
session cannot reliably know its own origin, but the process that launched it
always does.

## The `/bus-formation` skill

`commands/bus-formation.md` wraps all of this in one slash command —
`/bus-formation save myeffort`, `/bus-formation apply dev-trio --channel
myeffort --dry-run`, and so on — for driving formations from inside a Claude
Code session rather than shelling out directly.

## Coming back after a reboot

`cbus formation resume <name>` is the whole recovery story: run it from any
fresh shell on the machine that saved the formation. It relaunches just the
anchor session (right directory, right profile, resuming its own transcript),
and the restored anchor wakes to a decision brief: the saved roster, which
peers are still resumable, and the `apply` commands to bring them back as
themselves or fresh — its call, confirmed with you. A guard refuses
double-resumes while the anchor is booting, and a formation that is already
running refuses with directions to the live seat.

## Anchors and integrations

Envelopes carry a free-form `drift_anchors` map. cbus itself owns exactly one
key (`git_head`, recorded at save and diffed at apply); every other key is
yours, written by hand or with `formation save --anchor key=value`, and
survives re-saves untouched. That makes anchors the integration seam: any
external system can adopt a key and consume envelopes read-only. As the worked
example, the author links each formation to his private issue tracker with
`--anchor bdx=<id>`, and a personal dashboard renders formations by reading
envelopes and relay spool metadata without ever writing them — which is why
the on-disk formats are treated as compatibility surfaces.

On that note: commit messages, changelogs, and the architecture docs reference
that same private tracker by opaque ids (`cbus-xxx`, `bdx-xxx`). They are
provenance markers, not links — there is nothing to resolve them against, and
the history reads fine without them.
