# Changelog (simple)

[2026-07-07 04:05:00 UTC] [CLI/Commands] New `cbus branch [target] [channel]` — join + channel-derive + fork with bootstrap prompt in one command. `/bus-branch` slims to two tool calls (branch, then arm Monitor); cc-branch.sh and its hardcoded path drop out of the skill's allowed-tools (helper resolved by cbus, override via CC_BRANCH).

[2026-07-07 03:40:00 UTC] [Docs] Corrected delivery semantics: Monitor events are push — an idle session wakes and acts autonomously (verified live: parent replied to a bus message with no user present); "turn boundary" only defers delivery on a busy session.

[2026-07-07 03:04:06 UTC] [Core] `send` now accepts a joined-but-never-armed peer (first arm replays the inbox); only a dead ex-listener is refused. Fixes the self-contradiction with the join→arm replay design.
[2026-07-07 03:04:06 UTC] [Core] Atomic alias claim on `join` — bare `mkdir` as the lock, so concurrent joins can't pick the same alias and truncate each other's inbox.
[2026-07-07 03:04:06 UTC] [Core] Re-arm no longer redelivers the whole inbox — `-n +1` only on first arm, `-n 0` after. `--force` to a dead ex-listener is now documented best-effort.
[2026-07-07 03:04:06 UTC] [Core] Reject `.`/`..` in channel/alias names everywhere (`valid_name` + `split_target`) — closes a self-inflicted `rm -rf` path escape.
[2026-07-07 03:04:06 UTC] [CLI] New `cbus bootstrap <channel> [parent]` prints the canonical fork-child prompt; `/bus-branch` consumes it so the prompt can't drift from CLI behavior.
[2026-07-07 03:04:06 UTC] [Commands] `/bus-branch` reverted to arm-before-fork: the "no completion record" note is unavoidable (child resumes the transcript at boot, after the parent armed) and the old ordering opened an announce race. Note documented as cosmetic; `/bus-listen` reply guidance now warns about unroutable `hostname-PID` senders.

[2026-07-07 00:20:44 UTC] [Core] Initial claudebus: file-based message bus between live Claude Code sessions, built on the Monitor tool + plain files.
[2026-07-07 00:20:44 UTC] [Core] Named channels — peers addressed as `channel/alias`; bare alias resolves within your own channel(s); `global` reserved as the machine-wide orchestrator bus.
[2026-07-07 00:20:44 UTC] [Core] Auto-recycled aliases — `cbus join` auto-picks `main`/`fork-N`, is idempotent, and prunes dead peers in the channel first (10-min grace for a joined-but-not-yet-armed peer).
[2026-07-07 00:20:44 UTC] [Liveness] Hardened listener liveness — trust a live pid only if its args reference the inbox (kills pid-recycling false-positives); record owning `claude` pid so a crash-orphaned tail reads `off`.
[2026-07-07 00:20:44 UTC] [CLI] Added `cbus active [channel]` / `cbus list --active` to list only currently-listening peers; `cbus channels` summarizes channels.
[2026-07-07 00:20:44 UTC] [Commands] `/bus-branch` forks before arming the parent's Monitor (no stale "no completion record" task in the child); `/bus-listen` + `/bus-branch` default the channel to the repo name.
