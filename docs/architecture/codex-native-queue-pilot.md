# Codex CLI native queue pilot

2026-09-17. Tracking: cbus-6ij, cbus-6ij.11; inbox failure prerequisite cbus-6ij.7.

> **Historical: pilot record 2026-09-17; released as Codex v1 in v0.12.x.** This
> pilot's rollout gates are closed or explicitly scoped out for Codex v1; see
> [codex-v1-release-readiness.md](codex-v1-release-readiness.md) and
> [cross-harness-daemon-scope.md](cross-harness-daemon-scope.md) for what
> remains open.

## Decision and measured result

Carlos requires joining an existing CLI without restart whenever possible, with
exact-thread restart/resume only when the runtime cannot support that path.
Installed Codex CLI 0.154.0 supports this through its experimental native queue.
There is no need to relaunch every peer through `cbus codex` or to attach a second
thread writer.

Two independent runtime checks used temporary Codex homes and an offline fake
model provider at `127.0.0.1:9`, without real credentials or paid model calls:

1. An owning app-server and a separate queue sidecar: sidecar only initialized and
   enqueued. The owner emitted exact-thread `turn/started` after 9.939 seconds;
   the sidecar's loaded-thread list stayed empty. Retrying the same client message
   ID created another queue item, proving native enqueue is not idempotent.
2. An ordinary Codex CLI TUI: built cbus connected source=`cli` thread
   `01a0b1a3-6af8-7360-9459-57408f28addc`, submitted a bus message, and observed the
   framed payload and exact cbus client ID in that original transcript. The TUI
   retained PID 28114 throughout. cbus recorded one accepted item. After native
   dequeue, a new non-owning sidecar could read the persisted user-message client
   ID while still reporting no loaded threads.

The first TUI probe drove `cbus connect` from a subprocess with the TUI's exact
identity and home, simulating its shell tool environment. It did not ask a live
model to invoke the skill or send a reply. These results establish actual CLI
inbound delivery without restart, not a complete model round trip or permission
acceptance for an arbitrary user's sandbox.

An earlier source candidate also passed the expanded ordinary-CLI canary: 13
checks covered daemon stop, forced enqueue during downtime, daemon restart,
same-alias reconnect, retained registration/cursor, and exactly one receipt of
each of two messages despite repeated connects. Original TUI PID 51225 survived
daemon PID 51266 changing to 52186. Codex thread:
`01a0b1ac-1455-78f0-817c-7a123bdc4e18`. Candidate SHA-256:
`b02202cbce4f585103976f607e800329f6c52e9727cd50080ce2edafa937e359`.
All scratch processes were cleaned up and the control socket was removed.

A separate offline lifecycle canary passed all 17 checks on installed Codex
0.154.0 using a scratch app-server and a local deterministic model provider.
The busy turn stayed active for 11.05 seconds; its queued message remained pending
until that turn completed. Explicit interruption paused native queue consumption.
Both rejoining the live thread and resuming its persisted history in a fresh
app-server preserved that pause. The cold-resumed thread's public status was
`idle`, so that status alone does not mean its queue will drain. A normal user
turn had to complete before the pending queue was consumed in both cases.
The queue sidecar never loaded the thread; exactly eight expected local provider
requests occurred, and all test processes exited.

This lifecycle proof used source=`vscode` scratch thread
`01a0b1ba-753a-7423-826c-4df98d76491d`; it does not establish an interactive CLI
restart or desktop support. Evidence is
`/private/tmp/cbus-queue-lifecycle-rp6ocgny/result.json`; reproduce with
`python3 scripts/codex_queue_lifecycle_canary.py`.

CLI canary and suite evidence attached to cbus-6ij.11:

- `f16b2b31a688516b`: final CLI canary result with commands, identity, hash and checks.
- `c16355040d036df8`: `go test ./... -count=1`, all packages passed.
- `85e96a022581ffc0`: targeted delivery, lifecycle, queue and bridge race tests passed.
- Earlier TUI checks retained as `53c37adeb2852051` and `1484038536eba0a8`.

The tested source also builds for Windows/amd64; native connect/daemon execution
still explicitly refuses that platform in this pilot. The skill validator passed.
Reproduce the offline runtime check with
`CBUS_TEST_BINARY=/path/to/cbus python3 scripts/codex_native_queue_canary.py`.

The native watcher is installed in the 0.154.0 queue extension and checks shared
SQLite changes at a ten-second interval. Queue removal means a turn started,
not that it completed. User interruption pauses automatic queue consumption
until a subsequent user turn completes; resume alone did not clear it in the
controlled lifecycle test.
The API is experimental and must be capability-checked. Public transport context:
[Codex App Server](https://learn.chatgpt.com/docs/app-server).

## Live session and sandbox boundary

The first real model-driven attempt read the skill and ran `cbus connect`, but
the normal sandbox denied access to the local Unix control socket with `EPERM`.
The command treated the failed health check as a missing daemon and attempted a
duplicate bootstrap, obscuring the original permission error. The CLI now
preserves wrapped `EPERM`/`EACCES` from health checks and asks for exact-command
approval instead of starting a duplicate. Four focused regression tests passed.

A fresh ordinary Codex CLI then passed the real model round trip, preserving its
normal `workspace-write`, network-restricted sandbox and `on-request` approval
policy. The model read the repo-local skill and called connect. Initial daemon
creation was sandbox-blocked; the model read the log and requested the usual
approval for that exact command. One-time approval allowed bootstrap without
adding a persistent permission rule. READY and both PONG sends subsequently
succeeded inside the unchanged sandbox, using an isolated writable `/tmp` bus.

The exact thread was `01a0b1be-09f1-7353-b607-6aed96e843f2`, Codex 0.154.0,
CLI launcher PID 8409. Its transcript received each framed PING once and the
verifier inbox recorded each exact PONG once from `native-live/cli-test`.
The second message was force-queued during daemon downtime; daemon PID 11085
became 19284, while the CLI process and thread stayed unchanged. The journal
recorded exactly two accepted messages. Thirteen live assertions passed, with
only bootstrap and the two expected message turns observed. This was a short
test, not the greater-than-30-minute idle soak.

Initial round-trip candidate SHA-256:
`09e7806110c06b4be8c8a106d07464db17d57a28e48a673e44540f589c3c1ef3`.
After clarifying capability wording in the CLI and source skill, the restart
used final candidate SHA-256:
`111f90969e6a68d7195b28818fe588282826a421a5378626be5b3243769fe5a0`.
Evidence: `/tmp/cbus-live-20260917-retry/result.json`,
`transcript-evidence.json`, and `verifier-inbox-evidence.jsonl`. The first failed
attempt is retained under `/tmp/cbus-live-20260917-01a0b1af/result.json`.
Result-only evidence is attached to cbus-6ij.11 as `3c88d76417052dda`, including
artifact hashes, live/lifecycle check counts, permission outcomes and remaining
gates. Raw transcript upload was blocked by automatic approval review; the raw
artifacts remain local. The final `go test ./... -count=1` run passed all packages
(`/tmp/cbus-native-live-final-tests.log`).

An independent socket check reproduced `connect: operation not permitted` from
the normal sandbox against an already-running daemon whose entire bus directory
was under `/tmp`. The temporary daemon started and stopped through approved
commands and never had a connected session. A writable bus directory therefore
does not establish socket permission. Use the session's normal approval mechanism
for the exact cbus command when required. Default-home replies also need writes
to `~/.claude-bus` outside the workspace. Keep sandbox and approval settings
unchanged during acceptance testing.

## 65-minute idle and default-directory acceptance

The subsequent ordinary CLI test passed both gates on 2026-09-17 local time,
using the actual default `~/.claude-bus` and an isolated test channel. The measured
window ran from 2026-09-18 00:10:35.768Z to 01:15:35.800Z: 3,900.032 seconds of
awake monotonic time and 3,900.014 seconds of wall time. There were no recorded
maintenance turns, token-usage records, assistant responses, tool calls, or new
inbox messages. The native CLI, launcher and daemon retained their process start
identities. The transcript and acceptance journal remained unchanged.

After that quiet window, one PING reached the same exact conversation and one
matching PONG landed in the verifier inbox after 9.45 seconds. A further
21-second duplicate guard found no duplicate. No approval prompt appeared for
the reply. Thread: `01a0b1cf-ec84-7440-a4e1-8211523a2843`; candidate SHA-256 remains
`111f90969e6a68d7195b28818fe588282826a421a5378626be5b3243769fe5a0`.

Permission context matters: bootstrap received one-time approval, while the
existing `cbus send` allow rule permitted the reply command outside the command
sandbox. Session policy remained `workspace-write` / `on-request`, and hashes of
the global configuration and rules files were unchanged. No new permission rule
was added. This establishes unattended default-directory replies under Carlos's
existing rule, not under a fresh installation with no send allowance.

The initial executable preflight caught `.zshenv` prepending `~/.local/bin`, even
with `login:false`. The same CLI continued with the candidate's exact absolute
path, whose basename `cbus` matches the existing rule in Codex 0.154.0's executable
resolution. It was not necessary to restart the CLI or change global PATH.

An external Python observer sampled local evidence without invoking the model.
The result proves zero recorded maintenance activity for this session; rollout
usage records are not exhaustive API-request or billing telemetry. Test-owned
processes exited, the control socket and test aliases were removed, and the
production binary was not replaced. Local evidence and the frozen observer are
under `/tmp/cbus-soak-20260917`; `result-only.json` omits raw transcripts, local
paths and session identifiers. It is attached to cbus-6ij.11 as
`a4db08c4de055a9e`. An independent artifact audit confirmed 390 idle samples,
a maximum 10.682-second sample gap, unchanged process identities and configuration,
and the exact post-idle reply. No blocker invalidated the observed run.

## Implemented first slice

```text
Existing Codex CLI -> session-side skill -> cbus connect
                                            |
                               user-local cbus daemon
                                            |
                       inbox + durable attempt/acceptance journal
                                            |
                          non-owning Codex queue sidecar
                                            |
                              existing CLI consumes input
```

- One local cbus supervisor, private Unix control socket, persisted registrations.
- Exact native thread identity, standard local Codex home/configuration, recorded
  CLI-source check, positive desktop-ancestor refusal. No inference from cwd,
  session name, frontmost terminal, or most recent transcript.
- `connect`, `connection status/disconnect`, and daemon lifecycle commands.
- Session-side `cbus-connect` skill with SHA-protected embedded installation.
- Native queue delivery without `thread/start`, `thread/resume`, or `turn/start`
  in the sidecar; no model maintenance turns or presence-message turns.
- Durable intent before submission; acceptance and inbox offset saved together.
  Lost acknowledgments reconcile by client ID in queue/history. Unproven outcomes
  stay visible and blocked rather than risking an automatic duplicate.
- Pending inbox preserved through daemon downtime and same-thread reconnect.
  Alias epochs and shared lifecycle locks fence stale connections; legacy prune,
  tail takeover, rename, close, and alias replacement cannot take over a managed
  peer. Explicit unregister/leave still remove it.
- Existing LocalSend now reports inbox open/write/close failures. Existing bridge
  turn tracking only accepts notifications with its exact thread ID.

## Delivery observations and operator recovery (2026-09-18 UTC)

`connection status` now separates cumulative native acceptance from a timestamped
observation of the latest accepted message. `connection reconcile CHANNEL/ALIAS`
performs an on-demand read-only lookup: native queue presence is `queued`, an
exact client ID in the exact thread's user-message history is `received`, and
absence is `unknown`, never evidence of rejection. History receipt proves
neither completed inference, a reply, nor current runtime liveness. No idle
receipt polling or synthetic model probe was added. Old journals remain readable
and report unverified receipt until a newly tracked message provides evidence.

An unresolved submission still blocks automatic replay. The explicit operator
command `connection abandon CHANNEL/ALIAS --pending CLIENT_ID --reason TEXT`
releases that one local block, with ownership, inbox identity/content and pending
ID checks. It durably retains an audit record and a separate abandonment count.
The original may have arrived or may still arrive; this command does not cancel
or resend it. It works when the backend is unavailable, and preserves an explicit
disconnection. Positive evidence retained after a journal-save failure cannot be
abandoned as unknown. Stale repeated commands cannot advance the next message.

Review also found that internal Codex RPC errors can follow successful storage.
They now retain the same uncertainty fence as a lost response. Only the verified
pre-enqueue invalid-request paths (`-32600`) permit automatic retry; other RPC
codes remain conservative. Focused tests cover recovery after storage failures,
queue/history distinctions, stale or replaced aliases, changed inboxes,
disconnected recovery, and ambiguity across daemon reload.

## Actual CLI exit and exact-thread resume (2026-09-18 UTC)

`scripts/codex_cli_resume_canary.py` passed **27/27 checks** against Codex 0.154.0
and frozen cbus candidate SHA-256
`12cfcf01e46e403d25498566f4d273dfb911295ed0e7330abe4318aad2ec029b`.
The actual ordinary CLI completed its initial and first bus turn, exited cleanly
via `/quit`, and a new native CLI process resumed the exact UUID without a new
prompt. The same daemon and connection remained active throughout. A message
sent while the CLI was absent stayed in native storage for more than one watcher
interval (11 seconds), then appeared exactly once in the resumed thread, with
its earlier conversation input included in the resumed provider request.

The new reconciliation command reported that exact client ID as `queued` while
the CLI was absent and `received` after resume. The receipt's item ID matched the
user-message event in the exact rollout. Acceptance stayed at two bus messages;
reconciliation added no provider request. The read-only observer never loaded a
thread. Three recipient requests and three automatic title requests all went to
the local fake Responses provider; this test used no paid inference. Strict
complete-record JSONL parsing and all five process/socket cleanup checks passed.

Evidence: `/private/tmp/cbus-cli-canary-ecp6b98w/result.json`. The inspected,
result-only tracker artifact is `/tmp/cbus-reliability-result-only.json`; raw
transcript, request and identity details stay local. Tracker attachment
`d487c57e82690c29`, review `365da454947a1377`, is linked from cbus-6ij.11.
Earlier failed harness
attempts are retained: they exposed title-request accounting, PTY paste timing,
and the CLI update prompt. The final isolated config disables only that startup
update prompt; no user configuration changed.

The full Go suite, client/CLI race suite and Windows cross-build passed. An
independent reviewer checked the recovery logic and runtime artifacts. This
proves clean exact-UUID CLI resume and queue-to-history receipt, not interrupted
ordinary-CLI resume, picker/`--last` selectors or production-provider permissions.
The earlier long-idle and real reply evidence remains a separate proof layer.

## Gates before general rollout

- Document permission setup for fresh installations and other deployment
  profiles. Default `~/.claude-bus` passed unattended reply acceptance with the
  existing `cbus send` allowance and unchanged session permissions.
- Prove the *running* CLI supports native consumption. The installed executable
  and persisted `cliVersion` cannot establish an old running process's version.
  `queue-ready` establishes sidecar access to queue storage, not consumer
  readiness. Prefer real-message transcript evidence or a verified association
  with an existing capable backend; do not enqueue housekeeping probes.
- Alternate profile/storage overrides and remote backends require explicit
  configuration binding; current capture is binary/home/cwd only.
- CLI-source metadata and absence of desktop ancestry do not prove every detached
  frontend's identity. Desktop attachment remains outside scope.
- Multi-peer fairness and supported terminal launch/placement acceptance.
  Clean exact-UUID CLI restart/resume now passes the dedicated canary above;
  other resume selectors and interrupted ordinary-CLI resume remain untested.
  The 65-minute idle and default-directory test passed above; busy/interrupted
  semantics passed the controlled app-server test.
- Replace serialized supervisor delivery with independent bounded adapter work
  before broad multi-harness operation; add relay subscriptions and lifecycle
  management. Automatic launch at login is not installed by this pilot.
- Claude Code and OpenCode adapters plus all six directed harness routes remain
  epic work. The existing CC monitor incident mitigation is unchanged.

The repo-local `.agents/skills/cbus-connect` was temporarily installed and then
removed after verifying its content was unchanged. Test-owned CLI processes,
daemons and sidecars were stopped; evidence remains. The global user binary,
global skills, and Codex configuration remain unchanged. This is an experimental
local pilot, not a completed cross-harness rollout.
