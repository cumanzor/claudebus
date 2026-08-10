# Cutover decision package — bash `cbus` → Go `cbus-go`

> **CUTOVER EXECUTED 2026-07-13.** The per-machine swap in "What changes at cutover"
> was performed on the MBP and the NUC — `~/.local/bin/cbus` on both is now the Go
> binary (verify: `cbus --version`). This package is preserved as the decision
> record. **Update (2026-07-17):** the `install.sh` / `install-cbus-go.sh` installers
> were retired (`de07cbe`); rollback is now a manual copy of `bin/cbus` (in-repo
> until P3, since removed — recover it from git history), not `./install.sh` — see
> the corrected procedure below. Installer
> references elsewhere in this package describe the P2.6 readiness state as recorded.

Prepared at P2.6 (cutover **readiness**; zero cutover executed). This is the summary
that decided the per-machine binary swap. It was prepared pre-cutover; the cutover has
since been executed (see status above).

## Recommendation

**GO.** Every gate — hermetic, cross-platform, and the recorded live `/bus-branch`
smoke — is green; cbus-go is a byte-faithful, rollback-safe replacement for bash
`cbus`.

- **Class A/B differential sweep — 27/27 on BOTH platforms** (MBP darwin/arm64, NUC
  linux/amd64 via the temp-binary mechanism, nothing installed). Every verb's output
  + exit code is byte-identical to bash (volatiles normalized), with exactly two
  ruled deltas that are *intended*: (a) `--help` differs by the one obsolete
  `CC_BRANCH` env line (branch is native since P2.5); (b) trailing junk is an error
  (`whoami junk`) where bash silently discarded.
- **Rollback-safety — all pass.** cbus-go writes joins / sends / presence / meta with
  the D3 `lastActivity` field; bash `cbus` then reads (`list`/`whoami`/`channels`/
  `inbox`) and operates (`send`/`unregister`/`rename`/`leave`) on all of it. Go and
  bash sends coexist in one inbox; bash ignores the extra `lastActivity` field
  cleanly. **A rollback to bash finds fully-usable state — no migration.**
- **Installer** (`install-cbus-go.sh`) — version-stamped build, mode-agnostic
  placement (validated over a symlink: replaces it, does NOT clobber the repo source
  — the M12 fix), read-only SessionEnd hook check. Touches only cbus-go.
- P2.1–P2.5 gates all closed (liveness, PeerStore+presence, send, follower, harness),
  each with its own differential.

### `/bus-branch` smoke — recorded (real `ccs` fork, window + tmux)

Run from a live session's Bash tool (the production `/bus-branch` context), real bus:

- **Window leg** — the iTerm2 window **opened and stayed open** with a live
  `ccs`→`claude` fork (both PIDs captured running ≥90s — this directly refutes the
  earlier "session ended instantly"); launch command exact
  (`ccs personal --resume <sid> --fork-session <bootstrap prompt>`); env replicated
  (`personal` profile, cwd, `CLAUDE_CONFIG_DIR`); the launcher tmpfile **self-deleted**.
- **Tmux leg** — a `cc-branch` tmux window was created and ran the launcher, spawning
  the same live `ccs`→`claude` fork; launch + env replication identical (verified via
  the fork's `ps -wwE`: `CLAUDE_CONFIG_DIR=~/.ccs/instances/personal`, replicated PATH).
- **Child boots + joins the channel (d)** — USER-CONFIRMED: Carlos's manual `cbus-go
  branch` runs (normal-transcript context, both window and tmux surfaces) showed BOTH
  parent and child in `cbus list` — the child boots, joins, and registers.

**Diagnosis note (on record).** An earlier smoke attempt used a fast-exit *probe* as
the launch target; a probe exits immediately, so iTerm2 reported "a session ended very
soon after starting" — a **methodology artifact**, not a branch fault. With the real
`ccs`, the window stays open. Separately, forking *this* go-port coder session's own
(200+ turn) transcript made the child's full boot slow and token-heavy, so the live
forks were killed before completing the boot; **that slowness is parent-transcript
weight, not the port** — normal-sized parents (Carlos's manual runs) boot fast and
join. `ccs` is a real binary that resolves in the launcher's non-interactive bash, and
the launcher tmpfile lives in the caller's `$TMPDIR` (proven cross-process readable
here); pinning it to `/tmp` is a filed **P3 hardening candidate**, not a P2 fix.

## What changes at cutover, per machine

The cutover is a **pure binary swap** — no `settings.json` edit, no state migration.

1. Build + place the Go binary AS `cbus`:
   `go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" -o ~/.local/bin/cbus ./cmd/cbus`
   (mode-agnostically — `rm -f` first if the current `cbus` is a symlink).
2. The existing **SessionEnd hook `cbus hook-exit`** now resolves to the Go binary —
   nothing to rewire (confirmed: the live `settings.json` wires `cbus hook-exit`).
3. `cc-branch.sh` is no longer consulted (branch is a native `TerminalForker`); it can
   stay on disk harmlessly or be removed.
4. `cbus-go` (the side-by-side binary) can be left as-is or removed — it and `cbus`
   are now the same code.
5. Verify: `cbus --version`, `cbus list`, `cbus whoami`, and arm a `cbus tail`
   follower via the Monitor tool.

**Rollout order** (per the effort's standing plan): **MBP first** (richest usage),
then **NUC** (re-run its installer — copy-install does not auto-update), then
**logos/WSL** starts on the port directly. Each machine is independent — the shared
`$CBUS_DIR` and the relay are forward/backward compatible.

## Rollback procedure (per machine)

1. Restore bash `cbus`: copy `bin/cbus` over `~/.local/bin/cbus` (`install.sh` was
   retired at `de07cbe`; recover it from git history if the copy alone is not enough).
   The bash client stayed in-repo at `bin/cbus` until P3; it is gone from the
   tree now — recover it from git history too.
2. No state action needed — bash reads and operates on all cbus-go-written state
   (proven; the D3 `lastActivity` field is ignored by bash's tolerant reader).
3. The SessionEnd hook `cbus hook-exit` again resolves to bash. Done.

Rollback is safe at any time because the two clients were never forked off separate
state — they share `$CBUS_DIR` and the wire format throughout coexistence.

## What stays bash

**Nothing.** The sweep is clean across both platforms; every verb, the follower,
presence, send-gate, liveness, and the harness are ported and differential-verified.
The remaining bash artifacts (`bin/cbus`, `bin/cc-branch.sh`) are retired per the
[compat-deletion plan](compat-deletion-plan.md) at homogenization (P3), *after* the
last machine cuts over.

## Evidence bundle

| Gate | Result | Source |
|------|--------|--------|
| Class A/B sweep, MBP | 27/27 | `scripts/p26_sweep.sh` |
| Class A/B sweep, NUC (linux/amd64) | 27/27 | `scripts/p26_sweep.sh` via rsync-to-tmp + build + run + delete |
| Rollback-safety | all pass | `scripts/p26_rollback.sh` |
| `/bus-branch` smoke, window + tmux | live fork + launch + env replication recorded | real `ccs` fork (this session) |
| `/bus-branch` child boots + joins | user-confirmed (both peers in `cbus list`) | Carlos's normal-session manual runs (window + tmux) |
| Installer (fresh / M12 symlink / hook-check) | all pass | `install-cbus-go.sh` |
| `go test -race -count=1 ./...` | green | repo test suite |
| P2.1–P2.5 milestone differentials | closed | `detailed_changelog.md` |
