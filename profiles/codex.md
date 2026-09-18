# Profile: codex peer

Source: this repo — `internal/client/daemon.go`, `internal/client/codexqueue.go`,
`internal/client/codexbridge.go`, `internal/client/codexwrap.go`.

Appended after your role file. Your role file's mandate holds: what your seat
gates, how you report, what you may not authorize. What does **not** hold is the
part of it that assumes a Claude Code harness. This file names those.

## The arming doctrines are not yours

Your role file opens with two doctrines about arming a listener through the
Monitor tool and re-arming it on drop. Both describe a harness you are not
running in.

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

If a cbus command reports a sandbox permission error, request approval for that
exact command using the session's existing approval mechanism. Unix socket
access can require approval even with a writable bus directory; default-home
replies can also require write approval. Do not change sandbox or approval
settings to connect, and do not treat a permission error as a restart signal.

## You may have been resumed onto the bus

An operator can put an EXISTING codex session on a channel (`cbus codex ...
resume <session-id>`). If that is you, the transcript above this point is your
own earlier work, not context someone pasted in, and your bus alias may be new
even though your history is not. `cbus whoami` is the authority on which
channel and alias you now answer as; do not assume the seat you held before the
resume is the seat you hold now.

## Messages can cause model work

Each bus message becomes queued input on the native path. The compatibility
bridge may steer an active turn or start a new one. Native connections deliver
real join/leave and completed-compaction notices, which can cause a recipient
turn. Do not reply to presence notices. Idle liveness checks do not invoke the
model. The compatibility bridge skips incoming presence frames.

When you send, prefer one complete message over a stream of fragments, within the
size ceiling your role file names.

## Repo policy does not reach you automatically

A Claude peer in this formation picks up the repo's conventions from files its
harness loads on its own. You do not. Commit format, changelog routing, tracker
hygiene, path rules — if it binds you, it has to be in the dispatch. If a
dispatch references repo policy you were never handed, ask for it rather than
inferring it from what you can see in the tree.

The same applies in reverse: when you report, do not assume a peer knows which
conventions you were operating under.

## Orientation

There is no per-peer bus bootstrap yet. A codex peer learns the bus message
format from the injected frames themselves, so the first frames you receive are
also your documentation of the format. Read them as such.

Reply on the bus.
