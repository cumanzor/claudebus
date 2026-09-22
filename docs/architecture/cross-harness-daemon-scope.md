# Cross-harness daemon scope

2026-09-17, cbus-rtt and cbus-6ij. Scope approved by Carlos in D3-D4: the full
integration proceeds Codex CLI first, Claude Code's D2 incident stopgap kept
separate until its own adapter landed.

**Status (2026-09-22).** Both first-class adapters this doc scoped have shipped:
Codex CLI v1 in [v0.12.2](https://github.com/cumanzor/claudebus/releases/tag/v0.12.2)
(see [codex-v1-release-readiness.md](codex-v1-release-readiness.md)), native Claude
Code receive in [v0.13.0](https://github.com/cumanzor/claudebus/releases/tag/v0.13.0)
(see [docs/claude.md](../claude.md)). OpenCode and the six directed routes below
remain open; this doc is their live scope and adapter proposal.

## Scope rule

The design targets **Claude Code, Codex CLI, and OpenCode** as first-class peers.
**Harness desktop clients are deferred to v2.** Their discovery, attachment, UI
and transport ownership must not become requirements or blockers for v1. This
does not exclude desktop terminal hosts such as iTerm2: CLI peers inside them
remain in scope.
Other harnesses remain future extensions.

Codex CLI includes the existing `cbus codex` terminal workflow: a per-peer
app-server with the CLI TUI attached. A supporting app-server does not make this
a desktop integration. Shipped Codex v1 (above) supports no-restart connection
from an ordinary local CLI through its native queue. The existing wrapper stays
available as a compatibility path; restart/resume is only a fallback when the
running runtime cannot accept delivery. See [docs/codex.md](../codex.md).

## Terminal independence rule

cbus remains **terminal agnostic**. Preserve iTerm2 panes/windows/tabs and tmux
support as the cross-harness work proceeds. Do not require tmux for bus delivery,
daemon operation or a particular harness. Herdr is a possible future terminal
host to evaluate; no compatibility or automation API is assumed here.

Keep three responsibilities distinct:

| Component | Responsibility |
| --- | --- |
| Harness adapter | Start/resume a session, identify it, deliver messages, observe its lifecycle |
| Terminal backend | Create or attach a visible terminal surface and execute a launch specification |
| Placement policy | Select backend, window/tab/pane, anchor, split direction and focus behavior |

The current `ForkSpec`/`TerminalForker` seam in `internal/client/harness.go` is the
starting point. Backend choice and placement are partly mixed today: `Target`
accepts window/tab/tmux/pane, and pane selects tmux before iTerm2 from the caller's
environment. Evolve that seam rather than embedding terminal commands into the
message-delivery adapters. Preserve existing command compatibility while
separating explicit backend selection from surface/layout preference.

**Daemon-specific requirement:** capture terminal context at the requesting CLI
and pass it with each launch request. A long-lived daemon must not use its own
startup `TMUX`, `TMUX_PANE` or `ITERM_SESSION_ID` to infer the caller's destination.
Keep terminal surface identifiers separate from harness session IDs. Validate
that an anchor still belongs to the intended backend/session before using it;
do not treat focus or the frontmost window as peer identity.

Peer-creation and placement polish stays in scope as a separate work package:
explicit caller/group anchors, predictable automatic placement, visible launch
failure, and documented fallback when the requested surface is unavailable.
Retain explicit split/layout preferences and avoid silently rearranging unrelated
peers or stealing focus. The existing iTerm2 tab path can fall back to the current
window on a stale anchor; that behavior needs an explicit decision in this work,
not a claim that every current launcher already meets the proposed policy.

Terminal independence does not require identical UI features. The current live
layout implementation is tmux-specific. Backends should advertise supported
operations, and unsupported placement/rearrangement should be reported clearly
without changing message transport or corrupting peer registration. Include a
plain-terminal/manual attach path for hosts without automation support. Define
session lifetime separately from UI attachment, so a layout or focus change
does not change bus identity; explicit session exit still releases ownership.

Adding a future Herdr backend should affect terminal launch/placement code, not
the bus protocol or three harness adapters. Its capability assessment can wait
until it is selected for a prototype.

## Proposed adapter boundary

Keep one bus contract for addressing, durable enqueue, message IDs, retries,
receipts and session ownership. Each harness adapter supplies its own session
discovery, wakeup, busy-session policy and lifecycle. Process health, session
availability and message receipt are separate states.

| Harness | Initial candidate | Important boundary |
| --- | --- | --- |
| Claude Code | **Shipped, v0.13.0:** a per-session native messaging socket bound at `cbus connect`, verified against the caller's actual process, socket and session transcript; receipt comes from the bound transcript, not a channel poll | Authentication/policy restrictions and exact-session idle wake need live proof |
| Codex CLI | Native durable queue via a non-owning sidecar and `cbus connect`; existing wrapper remains compatible | Exact thread and local storage context; preserve TUI ownership; reconcile ambiguous enqueue because native client IDs do not deduplicate |
| OpenCode | Session API and event stream, with a small plugin for session registration and shell identity | Runtime endpoint and session ID must both be explicit; a shared server can contain multiple sessions |

The daemon owns idle waiting and retries without model turns. Adapters report
capabilities such as idle wake and delivery while busy; a common interface must
not pretend that all harnesses support identical turn semantics. A baseline
queue-until-idle policy can work across adapters; supported steering can remain an
optional capability.

## OpenCode as a first-class target

The installed CLI reports **1.18.31**. Read-only local checks confirmed
`opencode serve` and `opencode attach <url> --session <id>`. The documented server
exposes session prompts, message lookup, status and SSE events. This provides an
adapter candidate without a model-managed Monitor. An independently started
`serve` process is a new server, so launching one does not establish attachment
to the user's existing TUI. [Server API](https://opencode.ai/docs/server/)

For daemon-owned launches, explicitly register the server endpoint, project
directory and exact session ID; attach the TUI to that session. For an existing
OpenCode session, a plugin can be the registration bridge. Evaluate the plugin's
session-aware `shell.env` hook for `CBUS_SESSION_ID` so replies use the correct
identity; never set one process-global session ID for all sessions.
The hook's session ID is optional: a missing ID must not be replaced by a guess
from cwd or whichever session was last active. [Plugins](https://opencode.ai/docs/plugins/),
[v1.18.31 hook types](https://github.com/anomalyco/opencode/blob/v1.18.31/packages/plugin/src/index.ts).

Start by queuing bus messages until the target session is idle. Admission of a
prompt while busy must not be described as guaranteed steering, ordering or
completion until measured. Likewise, HTTP 204 from `prompt_async` records
submission, not proof that the model consumed the message. Correlate message IDs
with persisted messages/events and distinguish that from completed work.
The v1.18.31 source persists new input before entering the session loop, and joins
an existing running loop when busy. That supports testing non-interrupting
admission; it does not establish a per-message completion contract.
[Prompt source](https://github.com/anomalyco/opencode/blob/v1.18.31/packages/opencode/src/session/prompt.ts),
[runner source](https://github.com/anomalyco/opencode/blob/v1.18.31/packages/opencode/src/effect/runner.ts).

## Acceptance: the OpenCode / three-way gate

Claude Code and Codex CLI shipped as first-class peers without OpenCode
(status block, above), so this list is now the gate for adding OpenCode as the
third peer and proving all six directed send/reply routes across all three
harnesses. The two shipped adapters already covered idle delivery, busy-session
messages, restart, resume, presence and permissions for their own two harnesses
(`codex-v1-release-readiness.md`, `claude-native-review.md`), tagged below;
OpenCode and the three-way combination remain open. Verify all six directed
send/reply routes among them, using exact session identity, plus:

- An unattended idle period with no housekeeping model calls, followed by a real
  message and recipient reply. (Claude/Codex: covered.)
- Busy-session messages and bursts, with observed ordering and no lost input.
  (Claude/Codex: covered.)
- Adapter/daemon restart, ambiguous submission results, retry and deduplication.
  (Claude/Codex: covered.)
- Session exit, resume, alias replacement and stale-adapter fencing.
  (Claude/Codex: resume covered; session-exit/alias-replacement fencing not
  separately confirmed.)
- Peer-visible join/leave/session-exit presence parity with Claude Code
  (`cbus-6ij.12`). Track the actual client independently of daemon lifetime and
  terminal detach/moves. Keep recurring liveness checks outside the model and
  choose how each harness surfaces real presence events explicitly.
  (Claude/Codex: covered.)
- Replies under each harness's intended permissions, without silently widening
  them to make the test pass. (Claude/Codex: covered.)
- Launch/resume coverage across the three CLI harnesses on both iTerm2 and tmux,
  including tmux inside iTerm2, explicit anchors and different callers sharing
  one daemon. Check actual child cwd, environment and bus identity.
- Placement failure, stale anchors, focus changes and UI detach/reattach without
  wrong-window launches, duplicate peers or stale alias reservations. Validate
  a manual launch/attach path outside either automated terminal backend.

For OpenCode, test multiple sessions sharing one runtime early: no cross-session
delivery, alias leakage or process-global identity. Do not gate this pilot on
desktop-client support. No model sessions or live adapter acceptance were run as
part of this scope update.

The earlier stopgap/delivery assessment remains attached to cbus-rtt as
`eb65ad39fda9f9b5`, plus private notes in the internal knowledge base (not
reachable from this repo). The
[July exploration](multi-harness-exploration.md) is historical research, not the
current implementation status.
