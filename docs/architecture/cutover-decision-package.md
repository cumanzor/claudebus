# Cutover decision package — bash `cbus` → Go `cbus-go`

Prepared at P2.6 (cutover **readiness**; zero cutover executed). This is the summary
to decide the per-machine binary swap. **Cutover is user-gated** — nothing here has
been executed.

## Recommendation

**GO**, pending the one live gate below. Every hermetic and cross-platform gate is
green; cbus-go is a byte-faithful, rollback-safe replacement for bash `cbus`.

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

### One live gate still open (Carlos-run)

The **real `/bus-branch` smoke (window + tmux)** forks a live session and opens real
terminals, so it cannot be hermetic and is not run autonomously. Exact commands +
expected observations are staged (below / with the orchestrator). Cutover should wait
on a green branch smoke.

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

1. Reinstall bash `cbus`: `./install.sh` (restores `bin/cbus` → `~/.local/bin/cbus`).
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
| Installer (fresh / M12 symlink / hook-check) | all pass | `install-cbus-go.sh` |
| `go test -race -count=1 ./...` | green | repo test suite |
| P2.1–P2.5 milestone differentials | closed | `detailed_changelog.md` |
