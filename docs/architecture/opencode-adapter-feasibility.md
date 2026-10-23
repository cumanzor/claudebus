# OpenCode adapter feasibility

2026-10-22. Measured against OpenCode **1.19.3** on macOS, with a source review
of the matching upstream tag (`v1.19.3`, commit `e3c4eb2`). This document
records what was tested, what was only read in source, and what the adapter
design has to account for. No cbus code has changed yet. The scope and the
three-way acceptance gate stay in
[cross-harness-daemon-scope.md](cross-harness-daemon-scope.md).

## Verdict

An adapter is feasible as an **in-process OpenCode plugin speaking the classic
session API**, with the cbus daemon owning holding, retries and liveness.
Driving OpenCode over HTTP from outside does not reach an ordinary TUI, and
the newer `/api/session` surface is not usable for a visible TUI conversation
in this release.

Evidence kinds are kept apart below:

- **measured**: run live against 1.19.3 in a scratch project with a real model
- **source**: read at the upstream tag, not reproduced
- **inferred**: a consequence drawn from the two, not tested

## Reaching a running session

**Source.** A plain `opencode` TUI binds no TCP port. It runs its server in a
worker and talks to it through an internal RPC transport (`cli/cmd/tui.ts`,
`cli/tui/worker.ts`). A listener exists only with an explicit `--port` or
`--hostname`, or with mDNS, which also changes the default bind to `0.0.0.0`.
No registry of a running TUI's endpoint or credentials exists. An external
process therefore cannot find, or authenticate to, a session the user already
has open.

**Source.** A plugin's `serverUrl` falls back to `http://localhost:4096` even
when nothing listens there. It is not evidence of an endpoint. The SDK client
handed to the plugin does work, through the in-process transport, and already
carries the server's auth.

**Measured.** `opencode serve` without `OPENCODE_SERVER_PASSWORD` warns that
the server is unsecured. Its API includes sessions, files and PTYs, so cbus must
not start or recommend an unauthenticated listener as its transport.

## Two session APIs

1.19.3 ships two prompt surfaces.

| | Classic `/session/{id}/...` | New `/api/session/{id}/...` |
| --- | --- | --- |
| Used by the TUI | yes | no |
| Caller-supplied ID | `messageID` (`msg` prefix) | `id` (`msg_` prefix) |
| Resend with the same ID | **measured:** 204 again and a second copy of the text part is appended to the stored message | **measured:** same ID and body returns the original record; a different body returns 409 |
| Receipt | the message exists at `GET /session/{id}/message/{messageID}` | durable `session.next.prompt.admitted` event with a per-session sequence, replayable from `/event?after=` |
| Busy delivery | `delivery` absent | `steer` or `queue` |

The new surface has the better contract and was evaluated first. It is not
usable yet:

- **Measured.** A prompt admitted through `/api/session` on a classic session
  is durable (it receives an admission sequence) but never appears in the
  classic message history that the TUI renders. **Source:** the two surfaces
  use separate tables and separate runners.
- **Measured.** The new runner could not resolve providers that the classic
  path uses (`ModelUnavailableError`). When a drain failed that way, inputs were
  still recorded as `session.next.prompted` and no step event was written, so
  `prompted` means removed from the queue, not seen by a model.
- **Measured.** `POST /api/session/{id}/wait` is in the published schema but
  returns 503 (`Session wait is not available yet`).
- **Source.** The new bash tool does not call the `shell.env` plugin hook, so
  the reply identity described below would not carry over.

Re-evaluate the new surface when the TUI moves onto it. Until then the adapter
uses the classic API, and the plugin must compensate for its lack of
idempotency.

## Plugin bridge

OpenCode has two plugin kinds. Both were exercised from a project-level
configuration (`.opencode/plugins/` for server plugins, `.opencode/tui.jsonc`
for TUI plugins), without touching the user's global configuration.

**Measured, server plugin.** It runs inside the server process. `chat.message`
receives the session ID and the caller's `messageID`. `shell.env` receives
`{cwd, sessionID, callID}` for each agent shell command; an environment
variable set there reached the bash tool's output with the right session ID,
including when two sessions on one server ran shell commands at the same time.
**Source:** PTYs opened inside OpenCode get only `cwd`, so only the agent's
own shell commands carry session identity.

**Measured, TUI plugin.** `tui(api)` runs on the main thread of the TUI
process. `api.route.current` names the session the pane shows (`home` when
none). `api.client.session` offers `promptAsync` and `message`. The plugin
bound a Unix socket with mode 0600, and `LOCAL_PEERPID` on a connection to it
reported the TUI's pid. `api.lifecycle.onDispose` runs on a clean exit. No
route subscription exists (`api.route` exposes `register`, `navigate` and
`current`), so pane changes must be polled; a 50 ms poll worked.

**Measured, end to end.** In an interactive TUI, a message written to the
plugin socket was submitted with `promptAsync` into the session the pane
showed. It appeared in the visible conversation and the model replied. A
resend with the same ID was caught by looking the ID up first, and a request
naming a session other than the one shown was refused.

## Busy sessions

**Measured.** With no holding, two messages delivered while the session ran a
tool were stored immediately and folded into the running turn. The turn's
final answer replied to them, and the request the user had typed before them
never got its answer. Delivery while busy can override the person at the
keyboard.

**Measured.** With hold-until-idle (hold while `session.status` is busy,
re-read status immediately before submitting, release one message per idle),
the user's turn finished first and each held message then got its own turn, in
order, after 8.6 s and 9.9 s. A resend while held was caught.

**Inferred.** The classic API has no "submit only if idle". If the user
presses Enter between the status re-read and the submit, the message joins the
user's turn. The window can be narrowed, not closed.

**Source.** An input stored just as a run finishes could be accepted with no
run scheduled to answer it. **Measured:** a probe fired at a turn's
`step-finish` with delays of 0, 10, 50 and 200 ms, five runs each, gave 20 of
20 replies. Ten runs joined a still-busy loop; three at 50 ms saw the session go
idle 1 to 18 ms after the fire while the submit was in flight, and a new run
started each time. This characterizes the window and does not rule it out. The
plugin's lookup before each submit adds roughly 10 to 50 ms and may shift where
probes land. cbus should keep "stored but unanswered" as a reportable state.

## Several sessions, one server

Measured with one `opencode serve` and two `opencode attach` TUIs, each loading
the TUI plugin with its own socket:

- Each socket reported only its own pane's session. Concurrent deliveries and
  shell commands stayed in their own sessions, and each plugin released only
  its own session's held messages, although server events reach both.
- Hold state was per session: with one session busy, its message was held
  while a message to the other was delivered at once.
- **Hazard.** With both panes on the **same** session, sending one message ID
  to both sockets at the same moment duplicated the stored text in 8 of 8 runs
  (one reply each time). The lookup before submit is per process and not atomic
  across plugins. A first, sequential attempt hid this, because the shell
  construct used did not actually run the sends concurrently.

The daemon must therefore allow **one bound endpoint per OpenCode session ID**
and refuse a second binding, the same exact-identity rule it applies to Claude
Code and Codex.

## Crash and restart

Measured with `kill -9` on a TUI in the middle of a turn, with one message held
in the plugin:

- The socket file remained and a connection was refused (`ECONNREFUSED`), a
  clear signal that the endpoint is dead.
- The held message was lost; it existed only in plugin memory.
- The agent's bash child outlived the TUI and finished on its own. The
  assistant message never completed: its tool part stayed `running` in storage
  and the TUI still showed it spinning after `opencode -s <session>`. That is
  OpenCode's own crash recovery, outside cbus.
- After the relaunch the plugin rebound the socket under the new pid, the
  session reported idle, and a new delivery worked.
- **Hazard.** A second TUI started with the same socket path unlinked the live
  socket and rebound it. The first TUI kept running, unreachable.

## Presence

| Event | Plugin signal | Measured latency |
| --- | --- | --- |
| Pane opens a session | route poll; `session.created` for a new session | under 150 ms |
| `/new` or leaving the session | route poll only | poll interval |
| ctrl-c | `onDispose` | 37 ms |
| SIGHUP or SIGTERM | `onDispose`, exit code 0 | about 35 ms |
| Session deleted through the TUI's server (attach mode) | `session.deleted`, pane returns home | 34 ms |
| Session deleted by another process (`opencode session delete`) | none; the pane keeps showing it | never |
| Server killed under an attach TUI | none; the TUI stays up and status reads stale "idle" | never |
| Server restarted | `server.connected` | about 1 s |
| `kill -9` | none | never |

The SIGHUP and SIGTERM figures come from a probe that registered its own
handlers for those signals, which can change Node's default handling. An
earlier server-kill run showed the attach TUI receiving SIGHUP, which turned
out to be the terminal multiplexer tearing down, not OpenCode; only the rerun
with the server outside the multiplexer is counted.

Follow-on failure modes, measured:

- A delivery into a session deleted elsewhere fails with `Session not found`.
- A delivery during a server outage fails with `Unable to connect`, before any
  request is sent, so the message was definitely not delivered. Nothing
  re-triggers it when the server returns.

## Design requirements

From the findings above, for the first adapter milestone:

1. **Endpoint.** The TUI plugin binds a per-process Unix socket (mode 0600,
   path keyed by pid and process start time). It unlinks an existing path only
   after a probe connection is refused. Socket paths must fit the 104-byte
   macOS limit.
2. **Binding.** `cbus connect` from inside OpenCode binds the exact session the
   pane shows and the serving pid, checked with the peer credential on every
   connection. One endpoint per session ID; a second binding is refused.
3. **Holding.** The daemon owns the queue and hold-until-idle. The plugin
   reports busy, idle and route changes and submits only when told, so a crash
   loses nothing that was waiting.
4. **Submission.** Use classic `promptAsync` with the bus message ID as
   `messageID`. Before every submit, and before any retry, look the ID up and
   compare content; a hit is the receipt. Never resend after a hit. A failure
   before the request is sent is retryable; anything else is uncertain and goes
   to reconcile.
5. **Receipt.** Receipt is the stored user message with the bus message ID.
   It does not prove a reply or completed work.
6. **Presence.** Leave on `onDispose` and when the pane leaves the bound
   session. The daemon covers what the plugin cannot see: pid exit, refused
   connections, `Session not found`, and server outages, retrying held
   delivery on `server.connected`.
7. **Reply identity.** The server plugin sets the cbus identity in `shell.env`
   from the hook's own session ID, never from cwd or the last active session.
   A call without a session ID gets no identity.
8. **No unauthenticated listener.** Do not depend on `--port`, mDNS or an
   unsecured `serve` for delivery.

## Status

The adapter is implemented as described above; user setup is in
[docs/opencode.md](../opencode.md). Identity for `cbus send` turned out to need
no credential: the server plugin sets `CBUS_SESSION_ID`, which cbus already
resolves against the registration. A live three-way run (Claude Code, Codex CLI
0.163.0, OpenCode 1.19.3, one scratch daemon) delivered all six directed routes,
each read in the recipient's own conversation, with no attempt left pending.

## Still open

- Formation launch and resume for OpenCode, on iTerm2, tmux and a plain
  terminal.
- Windows: named pipe in place of the Unix socket, and the peer check.
- Bursts and ordering beyond the two-message hold test.
- The Linux runtime gate for the OpenCode process-state code (peer
  credentials, start times); the code builds for Linux but has only run on
  macOS.
- Re-checking all of the above on each OpenCode upgrade, since both plugin
  APIs and the new session surface are moving.
