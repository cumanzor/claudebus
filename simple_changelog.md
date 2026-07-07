# Changelog (simple)

[2026-07-07 00:20:44 UTC] [Core] Initial claudebus: file-based message bus between live Claude Code sessions, built on the Monitor tool + plain files.
[2026-07-07 00:20:44 UTC] [Core] Named channels — peers addressed as `channel/alias`; bare alias resolves within your own channel(s); `global` reserved as the machine-wide orchestrator bus.
[2026-07-07 00:20:44 UTC] [Core] Auto-recycled aliases — `cbus join` auto-picks `main`/`fork-N`, is idempotent, and prunes dead peers in the channel first (10-min grace for a joined-but-not-yet-armed peer).
[2026-07-07 00:20:44 UTC] [Liveness] Hardened listener liveness — trust a live pid only if its args reference the inbox (kills pid-recycling false-positives); record owning `claude` pid so a crash-orphaned tail reads `off`.
[2026-07-07 00:20:44 UTC] [CLI] Added `cbus active [channel]` / `cbus list --active` to list only currently-listening peers; `cbus channels` summarizes channels.
[2026-07-07 00:20:44 UTC] [Commands] `/bus-branch` forks before arming the parent's Monitor (no stale "no completion record" task in the child); `/bus-listen` + `/bus-branch` default the channel to the repo name.
