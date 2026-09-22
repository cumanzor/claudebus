# Profile: codex peer

Source: this repo — `internal/client/daemon.go`, `internal/client/codexqueue.go`,
`internal/client/codexbridge.go`, `internal/client/codexwrap.go`.

Supplemental reference supplied by the orchestrator when needed; cbus does not
automatically inject `profiles/*.md`. Your role's mandate holds: what your seat
gates, how you report, what you may not authorize. Daily commands are in the
[Codex cheat sheet](../CHEATSHEET.md#codex-cli-quick-reference).

## Native sessions do not arm a listener

Current stock roles use native `cbus connect`. Ignore obsolete Monitor/re-arming
instructions in an older copied role; use this session's native integration.

You have no Monitor tool. **Do not run `cbus tail`.** For a native connection,
`cbus connect CHANNEL [ALIAS]` registers this conversation and the daemon submits
messages to its native queue. The compatibility wrapper uses a bridge. Both own
the listening; neither requires a periodic model turn to re-arm.

If messages stop arriving, inspect `cbus connection status CHANNEL/ALIAS --json`
for a native connection, or report the bridge/wrapper problem. Do not start a
second listener.

Busy native sessions consume queued input after their current turn completes.
Explicit interruption pauses that consumption. Rejoining or resuming alone may
leave it paused even when status says `idle`; let a subsequent user turn complete
before expecting pending input to drain. Do not add a periodic turn to clear it.
These lifecycle semantics passed ordinary CLI clean and interrupted resume
tests on 0.154.0 with a local fake provider. The 65-minute ordinary CLI idle test
passed with zero recorded maintenance activity and a subsequent exact reply.

With authorized `cbus install-codex-skills --with-permissions` setup loaded, use
direct `cbus` commands or the approved executable. It trusts all cbus subcommands
and needs one CLI restart/resume to load the rules. Without it, request normal
approval for the exact command; wrappers and compound scripts have their own
approvals. Do not change general sandbox/approval settings, and do not treat a
socket permission error as a restart signal.

## You may have been resumed onto the bus

Resume a native peer with `codex resume THREAD_ID`, using the exact saved thread
and Codex home/profile, then connect from that session with the original channel
and alias. History and inbox position are retained. Check the returned address;
do not assume the seat from old context. Automatic Codex formation restore is not
yet supported. The local-only `cbus codex ... resume THREAD_ID` wrapper is a
separate compatibility path; it is not required for native connections.

## Messages can cause model work

Each bus message becomes queued input on the native path. The compatibility
bridge may steer an active turn or start a new one. Native connections deliver
real join/leave and completed-compaction notices, which can cause a recipient
turn. Announce membership changes to the user, retain known roles, and update the
roster without polling. Completed compactions update context, not membership.
Do not reply on the bus solely for presence. Idle liveness checks do not invoke
the model. The compatibility bridge skips incoming presence frames.

When you send, prefer one complete message over a stream of fragments, within the
size ceiling your role file names.

## Shared policy

Honor the repository instructions loaded by your harness, including applicable
`AGENTS.md`, plus the explicit task scope. Bus dispatch does not automatically
transfer another peer's loaded instructions. If a referenced policy is missing,
read the named file or ask for the missing context; do not invent its contents.

The same applies in reverse: when you report, do not assume a peer knows which
conventions you were operating under.

## Orientation

`cbus spawn ... --harness codex` supplies a native-connect opening prompt.
`--role` adds role instructions; its Claude `MODEL:` line does not select a Codex
model. Use explicit `--model` or the Codex profile/default. After joining, check
the roster once. The native roster PID belongs to the daemon; `connection status`
separately reports the observed CLI consumer identity and time.

Reply on the bus.
