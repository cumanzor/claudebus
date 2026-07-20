# Compat-package deletion plan (homogenization / P3)

The Go port carries a small set of **coexistence shims** that exist ONLY so the bash
`cbus` and the Go `cbus-go` can share one `$CBUS_DIR` and read each other as alive
during the side-by-side window. When bash `cbus` is fully retired — the P3
"structural liveness" homogenization, *after* every machine has cut over — these
delete in one commit. This is the inventory.

**Status (updated 2026-07-17):** cutover executed on the MBP and NUC — item 5's
rename happened (the Go binary is installed as `cbus`). Items 1–4 stay until no
bash-era follower can be armed anywhere (P3 homogenization); item 6's bash files
(`bin/cbus`, `bin/cc-branch.sh`) remain in-repo as the rollback artifact — the
legacy installers `install.sh` / `install-cbus-go.sh` were **retired** (`de07cbe`),
so rollback is now a manual copy of `bin/cbus` over `~/.local/bin/cbus` (or
git-history recovery); item 7 stays frozen. The logos/WSL node (`cbus-dc5`) starts
on the port directly.

**Tranche 1 executed (2026-07-18):** items 3, 4 and 6 are deleted — the mtime
fallback (unarmed grace is `lastActivity`-only; a readable meta with no parseable
stamp is past grace by definition, so pre-port relics become prunable and broadcast
skips them), the `CBUS_PYTHON` help line, and the bash artifacts `bin/cbus` +
`bin/cc-branch.sh` along with the p26 bash-differential harnesses
(`scripts/p26_sweep.sh`, `scripts/p26_rollback.sh` — both exercised the deleted bash
client, so they retire with it; bash rollback is now git-history recovery only).
Items 1–2 remain gated on P3 structural liveness: the argv-grep predicate still
needs the re-exec and the raw inbox spelling until the structural registry replaces
it. Item 7 stays frozen.

**Tranche 2 executed (2026-07-19, `3865d52`..`f853ff2`):** items 1 and 2 are deleted.
Item 1 (the Decision 2 re-exec) is gone whole — `TailArgv`, `ParseTailFollower`, the
hidden `--inbox`/`--from` flags, `compatInboxPath`, main.go's follower dispatch
branch, and the `os.Executable`/`os.Environ` plumbing that only existed to survive
the exec; `cbus tail` runs the follower in-process from arm to exit, and `InboxPath`
is plain `filepath.Join`. Item 2 (the raw inbox spelling) is retired as a COMPAT
artifact for the write side, but a narrower remnant survives under a new name:
`metaInboxNeedle` in `liveness_transition.go`, tagged `TRANSITION(P3T2)` — a
pre-P3-armed follower's argv is still on disk in live process tables and has no
recorded structural witness, so this is the only ground truth about whether it's
alive. Listener identity is now structural — `(pid, starttime)` via `procStartTime`
and single-sourced composers (`internal/client/starttime.go`) — by default;
`listenerIdentityHolds` treats the structural and `TRANSITION(P3T2)` argv branches as
exclusive by construction, never `structural || argv`, so the shim can't resurrect a
listener whose recorded starttime says it isn't that process. `TRANSITION(P3T2)` is
scoped to one release: its removal rides the same future release as the
`register`/`peers` drop. Rename now deliberately clears `listenerStart` (port-map
D1) rather than relying on the argv needle going stale by accident. A zombie
listener (dead but unreaped; linux `state=Z`) reads dead on both branches, matching
pinned bash-era behavior — a regression introduced mid-tranche by the structural
rewrite was reproduced then fixed (`f853ff2`) before shipping. Full pidfd/kqueue
liveness — the stronger mechanism `(pid, starttime)` was scoped as a portable floor
for — stays out of this tranche, filed separately as `cbus-6lv`. Items 3, 4, 6 stay
as tranche 1 left them; item 7 stays frozen.

| # | What | Where | Why it exists | Deletes to |
|---|------|-------|---------------|------------|
| 1 | **Decision 2 re-exec** — the follower `syscall.Exec`s itself so its argv carries `--inbox <path>` | `follow.go` `ArmLocalTail`, `TailArgv`, `ParseTailFollower` | bash-era `meta_listener_alive` greps `ps -o args=` for the inbox path; a Go follower must put it in argv to read alive to bash | run the follower in-process (no re-exec, no `--inbox`); liveness moves to a structural registry (pidfile / lock) |
| 2 | **Raw inbox spelling** — `compatInboxPath` / `metaInboxNeedle` (bash `printf` concat, no `filepath.Clean`) | `follow.go`, `liveness.go` | the argv string bash greps must byte-match bash's `inbox_path()` under any `$CBUS_DIR` spelling (F1) | `filepath.Join` everywhere (the clean path) |
| 3 | **D3 `lastActivity` mtime fallback** | `liveness.go` `unarmedGraceElapsed` | bash never wrote `lastActivity`; the grace clock falls back to the meta mtime for bash-written peers | `lastActivity`-only (the mtime read drops) |
| 4 | **`CBUS_PYTHON` env line in `--help`** | `cmd/cbus/usage.go` | ported byte-for-byte from bash help; bash used python, cbus-go does not | drop the line (the last bash-ism in the help text) |
| 5 | **Self-id Option X oddity** — `cbus-go --help` prints `cbus`, errors point at `cbus --help` | `usage.go`, `main.go` unknown-command | Option X: match bash's frozen strings so cutover is a pure binary swap | **no code change** — the binary is renamed `cbus` at cutover and the oddity is gone |
| 6 | **bash artifacts** — `bin/cbus`, `bin/cc-branch.sh` (the installers `install.sh` / `install-cbus-go.sh` were retired at `de07cbe`) | repo root / `bin/` | the bash client + its fork helper; distribution is now `get.sh` + `cbus selfupdate` | remove `bin/cbus` + `bin/cc-branch.sh` at P3 |
| 7 | **A3/A6 frozen credential-store locations** | `internal/client/cred.go` | keychain / XDG paths frozen so no re-seed is needed across the bash↔Go boundary | may relax, but no reason to — keep frozen |

**Tranche 3 executed (2026-07-19/20, `9a3a075`, M6.2 of `cbus-8k9.4`):** the
`TRANSITION(P3T2)` remnant of item 2 — `metaInboxNeedle`, `liveness_transition.go`
whole — is deleted. `listenerIdentityHolds` has one branch left: the structural
`(pid, starttime)` witness; an armed meta with no witness now reads dead outright,
the same posture already held for a stampless meta, so there is no longer a second
answer for a pre-P3 arm to fall into. Field impact was verified nil before the
drop, not assumed: every armed meta on the Mac carried a `listenerStart` (9 of 9),
and the NUC's peer store held none at all — no pre-P3 arm survived anywhere in the
fleet at drop time. `register`/`peers` are dropped in the same release (M6.1,
`75e352d`) — the pairing this plan predicted two paragraphs up ("its removal rides
the same future release as the `register`/`peers` drop") held exactly, both
landing in v0.7.0. Riding the same span: `cbus-fi3` (`1601f13`), a test-only
golden normalizer fix the tranche's own container liveness gate exposed (a
pid-width assumption unrelated to the shim itself).

**Consequence for the fleet, stated plainly because it is a compatibility line,
not a subtle one:** a pre-P3 (pre-v0.4.0) binary arming against a shared
`CBUS_DIR` after this upgrade writes metas the fleet reads as dead — the argv
read shim is gone and listener identity is structural-only; fleet binaries must
be v0.4.0+.

**Plan closed (v0.7.0).** All seven original items now have a final, executed
disposition: 1–2 deleted in tranche 2, 3/4/6 deleted in tranche 1, 5 resolved at
cutover with no code change, 7 deliberately kept frozen. The `TRANSITION(P3T2)`
remnant tranche 2 introduced as a temporary one-release shim is itself deleted in
tranche 3. Nothing further rides this plan.

**Grep-driven sweep:** `grep -rn 'COMPAT(P3' internal/ cmd/` now returns nothing —
items 1–4 are gone (1–2 in tranche 2, 3–4 in tranche 1). `grep -rn
'TRANSITION(P3T2)' internal/` also now returns nothing as of tranche 3 —
`liveness_transition.go` is deleted whole. #5 needed no code change (rename), #6
is bash files (gone, tranche 1), #7 stays.

Notes:
- All items but 7 are now resolved: 3/4/6 in tranche 1 (2026-07-18), 1/2 in tranche 2
  (2026-07-19), 5 at cutover (2026-07-13, no code change). Item 7 stays frozen by
  design, not gated on anything.
- ~~`TRANSITION(P3T2)` (the item-2 remnant, `liveness_transition.go`) is a new,
  separately-tracked, one-release shim~~ — **deleted, tranche 3 (2026-07-19/20,
  `9a3a075`)**. It was never one of the seven original items and never answered
  to the `COMPAT(P3` grep; both grep and shim are gone together now.
- The `version` verb and the `-ldflags` stamp are **not** compat and stay.
