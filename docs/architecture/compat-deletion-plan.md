# Compat-package deletion plan (homogenization / P3)

The Go port carries a small set of **coexistence shims** that exist ONLY so the bash
`cbus` and the Go `cbus-go` can share one `$CBUS_DIR` and read each other as alive
during the side-by-side window. When bash `cbus` is fully retired — the P3
"structural liveness" homogenization, *after* every machine has cut over — these
delete in one commit. This is the inventory.

| # | What | Where | Why it exists | Deletes to |
|---|------|-------|---------------|------------|
| 1 | **Decision 2 re-exec** — the follower `syscall.Exec`s itself so its argv carries `--inbox <path>` | `follow.go` `ArmLocalTail`, `TailArgv`, `ParseTailFollower` | bash-era `meta_listener_alive` greps `ps -o args=` for the inbox path; a Go follower must put it in argv to read alive to bash | run the follower in-process (no re-exec, no `--inbox`); liveness moves to a structural registry (pidfile / lock) |
| 2 | **Raw inbox spelling** — `compatInboxPath` / `metaInboxNeedle` (bash `printf` concat, no `filepath.Clean`) | `follow.go`, `liveness.go` | the argv string bash greps must byte-match bash's `inbox_path()` under any `$CBUS_DIR` spelling (F1) | `filepath.Join` everywhere (the clean path) |
| 3 | **D3 `lastActivity` mtime fallback** | `liveness.go` `unarmedGraceElapsed` | bash never wrote `lastActivity`; the grace clock falls back to the meta mtime for bash-written peers | `lastActivity`-only (the mtime read drops) |
| 4 | **`CBUS_PYTHON` env line in `--help`** | `cmd/cbus/usage.go` | ported byte-for-byte from bash help; bash used python, cbus-go does not | drop the line (the last bash-ism in the help text) |
| 5 | **Self-id Option X oddity** — `cbus-go --help` prints `cbus`, errors point at `cbus --help` | `usage.go`, `main.go` unknown-command | Option X: match bash's frozen strings so cutover is a pure binary swap | **no code change** — the binary is renamed `cbus` at cutover and the oddity is gone |
| 6 | **bash artifacts** — `bin/cbus`, `bin/cc-branch.sh`, `install.sh` | repo root / `bin/` | the bash client + its fork helper + its installer | remove; `install-cbus-go.sh` (or a retargeted `install.sh`) is the sole installer, placing the Go binary as `cbus` |
| 7 | **A3/A6 frozen credential-store locations** | `internal/client/cred.go` | keychain / XDG paths frozen so no re-seed is needed across the bash↔Go boundary | may relax, but no reason to — keep frozen |

Notes:
- Items 1–3 are gated on **P3 structural liveness**, not on cutover: they must stay
  until *no* bash `cbus` process can arm a tail anywhere (all machines homogenized).
- Item 6 (bash retirement) is the cutover itself, per-machine; items 1–5 wait for the
  last machine.
- The `version` verb and the `-ldflags` stamp are **not** compat and stay.
