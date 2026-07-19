# claudebus — commit timeline

Extracted from the LLM-tier architecture doc during the docs consolidation
(knowledge -> repo fold-in). Two tables: the bash-era history through the
Go-port cutover, and everything shipped since.

## 1. Commit timeline (all 24, condensed)

| SHA | What |
|---|---|
| 4f62917 | Initial: channels, recycled aliases, hardened liveness, 10-min grace |
| 04d3e2a | Review fixes: send accepts unarmed peers, atomic mkdir join, re-arm-from-end, name validation closes rm-rf escape, `cbus bootstrap`, arm-before-fork revert |
| 2219679 / e8b59cf | Doc corrections: push delivery verified live; teammate mailbox closed by design |
| b15ce12 | `cbus branch` one-shot; channel derivation into the binary; CC_BRANCH |
| f64753e | Relay daemon: Maildir, subprotocol auth, displacement, heartbeat (gpt-5.5 review: 1 HIGH fixed; fsync + seq-first names declined) |
| b8b3f78/40d9759/7692d8d | prior-art doc |
| 692af95 | Client remote channels `<ch>@<host>/<al>`; registry build cut as over-engineering |
| 485740e | `cbus rename` + /bus-rename; TUI-title feature dropped (not settable, verified) |
| c73373a | Creds out of argv (`curl -K -`, `security -i`) |
| 5db94ff | Session-scoped markers (live impersonation bug) |
| 9115a33 / d727e3e / 7c09c58 / 4e64c6a / 43511f8 | Security model README; cheatsheet; /bus-listen→/bus-join; auth-seed docs; 1006 re-arm docs |
| a38999b | Local reframing vs measured 500-char cap; python follower replaces `tail -F` |
| e6a2d76 | Relay-side reframe + ~3000 ceiling discovery + ⚠truncated + deploy restart fix |
| 71348a7 / 8bc473b | Presence + concurrency hardening (adversarial peer review over the bus) |
| 5aa7fa7 | "Monitor-only, never Bash" hint hardening |
| f213e26 | `hook-exit` SessionEnd departure announce (HEAD) |
| go-port (epic cbus-8k9) | P0 shared core → P1 remote verbs (side-by-side `cbus-go`) → P2 local transport/follower/native branch → P2.6 readiness (27/27 both platforms) → **cutover executed 2026-07-13 (MBP + NUC)** |

## 2. Post-cutover commit timeline (Go-native, no bash anchor — 2026-07-14 → 2026-07-18)

None of this is port work — every row below is a feature that never existed in `bin/cbus`.

| Date | SHA(s) | What |
|---|---|---|
| 07-14 | `3b15a66` | `cbus spawn` + `/bus-spawn`: fresh blank-transcript session joined to a channel (cbus-ijx.2) |
| 07-14 | `72c19ff`, `37b621d`, `94c449c` | `--model` flag; `--name` flag; children titled by their bus alias (parent-side reservation) — on both spawn and branch |
| 07-14 | `eaa12e6` | Relay-host resolution generalized: env-only `CBUS_SITE_<HOST>_URL`, built-in `nuc` table dropped |
| 07-14 | `20bdb46` | Server-side relay prune: `cbus prune [<ch>]@<host>` |
| 07-14 | `6872c8b` | Remote presence MVP: join/departed cross the relay (cbus-ijx.5) |
| 07-16 | `f31d772`, `ac19f16` | Role prompts for cbus formations (orchestrator/coder/reviewer/documenter); `spawn --role` |
| 07-16 | `85de4b3`→`1262259` | Formations v1 M1-M6: envelope (M1) → list/show/rm (M2) → save (M3) → apply plan (M4) → apply launch+converge (M5) → bootstrap verb + `/bus-formation` skill (M6) |
| 07-17 | `641af1c`,`09f5515`,`fbcc505` | meta.json birth-record (M7): record how a peer was born; save captures it; apply stamps it |
| 07-17 | `dc1f164` | Formations M8: the `dev-trio` starter template — formations v1 complete |
| 07-17 | `5dd3a36` | README/CHEATSHEET updated for formations v1 + spawn --role + birth-records (repo native-docs tier only — **not** `docs/architecture/`) |
| 07-18 | `5b604e0`→`de07cbe` | Distribution v0.1.0 M1-M5: release Makefile+repo-slug (M1) → embed/install `/bus-*` skills+roles (M2) → selfupdate (M3) → opt-in update-available hint (M4) → `get.sh` bootstrap installer (M5, distribution complete) → legacy installers (`install.sh`, `install-cbus-go.sh`) retired (`cbus-7sg`) |
| 07-18 | `19dd20b`,`4dd092d` | `cbus hook-compact <pre\|post>` (`cbus-zig`): PreCompact/PostCompact hooks broadcast `compact-pre`/`compact-post` presence, local-only, registration untouched |
| 07-18 | `f246e3b`,`ff7ceef` | `pane`, a 4th fork target (splits a surface instead of opening a new one); `tab` fixed to target the window OWNING the caller's session instead of iTerm2's frontmost `current window` |
| 07-18 | `9c13055` | `formations/dev-trio.json` starter flipped all four peers `target: tab` → `pane` (Carlos-ruled) — tagged **v0.2.0** |
| 07-18 | `665ea69`→`e1b7153` (round 2) | Chain-split pane anchoring in `formation apply` (largest-area, ties to newest teammate) + envelope `split: auto\|right\|down`; new `cbus close` teardown verb; fixed the ownerPid argv[0]-vs-`comm` defect this round's own `close` testing exposed |
| 07-18 | `40eaec2` | `procZombie` implemented for linux (`/proc` stat parse) — `close.go` broke the linux leg of `make dist`, darwin being the only platform every prior gate had run on — tagged **v0.3.0** |
