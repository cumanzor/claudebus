# Profiles

A profile carries the part of a peer's guidance that depends on **what is running
the seat**, not on what the seat does. The orchestrator supplies this reference
with the role briefing; cbus does not automatically inject files from this directory.

## Why this is a separate file from `roles/`

`roles/<seat>.md` is a mandate: what a coder gates, how a verdict is shaped, what
the documenter owns. It was earned from live failures in this repo and it holds
regardless of who runs it.

Model tuning is the opposite. "You already verify your own work, do not add a
pass" is correct for one model and actively harmful for another. A formation
routinely runs several generations at once — as of this writing orchestrator and
coder on Opus 5, reviewer on Fable 5, documenter on Sonnet 5, and codex peers on
another provider entirely. Any tuning written into the shared doctrine block is
wrong for most of those seats by construction.

So the split is: **mandate in the role file, tuning in the profile.**

## Cross-harness is the sharp case

Current role files lead with native `cbus connect`. Claude and Codex use different
native adapters; neither needs a Monitor or `cbus tail`. Older copied roles can
still carry obsolete Monitor instructions. [codex.md](codex.md) explains the
Codex queue, permissions and lifecycle, and separates the compatibility bridge
from the ordinary CLI path.

## Resolution

Today the orchestrator sends the matching profile with the kickoff by hand
(orchestrator process rule 15). The intended mechanism reuses what cbus already
measures:

- `internal/client/role.go` — `LoadRole` already parses the `MODEL:` line and
  returns it with the body. A profile resolves off that value through the same
  repo-then-`$CBUS_DIR` cascade.
- `internal/client/ledger.go` — `HarnessName()` already reports `claude`,
  `codex`, `grok` or `opencode` from an ancestor walk, never guessed.
- `cbus codex` knows it is codex at launch by construction.

Wiring that into the launch path is filed separately rather than smuggled into a
prompt change.

## Handoff

A handoff carries the **successor's** profile, resolved from the successor's
model. Never a copy of the predecessor's. This is the whole point: the mandate
survives the handoff unchanged, the tuning is re-resolved for whoever picks it up.

## Absence is safe

There is no `sonnet5.md` because no Sonnet-5-specific prompting guidance was
found to ground one, and an invented profile is worse than none. A seat with no
matching profile runs on its role file alone, which is the behaviour every seat
had before this directory existed.

Each profile cites its source. If you cannot cite one, do not add the file.
