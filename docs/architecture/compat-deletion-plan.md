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

| # | What | Where | Why it exists | Deletes to |
|---|------|-------|---------------|------------|
| 1 | **Decision 2 re-exec** — the follower `syscall.Exec`s itself so its argv carries `--inbox <path>` | `follow.go` `ArmLocalTail`, `TailArgv`, `ParseTailFollower` | bash-era `meta_listener_alive` greps `ps -o args=` for the inbox path; a Go follower must put it in argv to read alive to bash | run the follower in-process (no re-exec, no `--inbox`); liveness moves to a structural registry (pidfile / lock) |
| 2 | **Raw inbox spelling** — `compatInboxPath` / `metaInboxNeedle` (bash `printf` concat, no `filepath.Clean`) | `follow.go`, `liveness.go` | the argv string bash greps must byte-match bash's `inbox_path()` under any `$CBUS_DIR` spelling (F1) | `filepath.Join` everywhere (the clean path) |
| 3 | **D3 `lastActivity` mtime fallback** | `liveness.go` `unarmedGraceElapsed` | bash never wrote `lastActivity`; the grace clock falls back to the meta mtime for bash-written peers | `lastActivity`-only (the mtime read drops) |
| 4 | **`CBUS_PYTHON` env line in `--help`** | `cmd/cbus/usage.go` | ported byte-for-byte from bash help; bash used python, cbus-go does not | drop the line (the last bash-ism in the help text) |
| 5 | **Self-id Option X oddity** — `cbus-go --help` prints `cbus`, errors point at `cbus --help` | `usage.go`, `main.go` unknown-command | Option X: match bash's frozen strings so cutover is a pure binary swap | **no code change** — the binary is renamed `cbus` at cutover and the oddity is gone |
| 6 | **bash artifacts** — `bin/cbus`, `bin/cc-branch.sh` (the installers `install.sh` / `install-cbus-go.sh` were retired at `de07cbe`) | repo root / `bin/` | the bash client + its fork helper; distribution is now `get.sh` + `cbus selfupdate` | remove `bin/cbus` + `bin/cc-branch.sh` at P3 |
| 7 | **A3/A6 frozen credential-store locations** | `internal/client/cred.go` | keychain / XDG paths frozen so no re-seed is needed across the bash↔Go boundary | may relax, but no reason to — keep frozen |

**Grep-driven sweep:** shims #1–#4 carry a source token — `grep -rn 'COMPAT(P3' internal/ cmd/`
enumerates them (#1 re-exec, #2 raw spelling ×2 surfaces, #3 mtime fallback, #4 the
`CBUS_PYTHON` help line). #5 needs no code change (rename), #6 is bash files, #7 stays.

Notes:
- Items 1–3 are gated on **P3 structural liveness**, not on cutover: they must stay
  until *no* bash `cbus` process can arm a tail anywhere (all machines homogenized).
- Item 6 (bash retirement) is the cutover itself, per-machine; items 1–5 wait for the
  last machine.
- The `version` verb and the `-ldflags` stamp are **not** compat and stay.
