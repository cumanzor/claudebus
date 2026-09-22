# Codex sessions as peers

## Connect an existing CLI session without restarting

Native Codex CLI connections have shipped since cbus v0.13.0, on macOS/Linux,
alongside [native Claude receive](claude.md). Codex's queue API remains experimental;
release field checks used Codex 0.155.1 on macOS and 0.154.0 on Linux. See the
[quick reference](../CHEATSHEET.md#codex-cli-quick-reference) for daily commands and
the [release validation](https://github.com/cumanzor/claudebus/releases/tag/v0.13.0)
for tested versions and limits.

For the same trusted-command experience as Claude Code, opt in once from the
installed cbus binary:

```sh
cbus install-codex-skills --with-permissions
```

This explicitly trusts all cbus commands, both bare `cbus` resolved through PATH
and the installed absolute executable, including spawn, updates and administration.
It leaves general sandbox and approval settings unchanged. Start a new Codex CLI
session, or restart/resume an existing one once to load the rules. Subsequent
channels and aliases need no additional permission rules. Use direct cbus commands;
unrelated shell wrappers and compound scripts retain their own approval behavior.
Use plain `cbus install-codex-skills` to install only the skill and retain normal
command approvals.

Inside the Codex CLI conversation, invoke `$cbus-connect` with a channel
and optional alias, or have the session run:

```sh
cbus connect feature-updates advisor-main --json
cbus list feature-updates
cbus connection status feature-updates/advisor-main --json
```

After joining, the skill checks `cbus list CHANNEL` once and reports the other
listening peers. Relay joins use the exact `CHANNEL@HOST` for the list and retain
`@HOST` in status/reply addresses. Relay `listen` means a connected subscription;
it does not confirm the native session is online. Roles come from explicit
assignments or an identified matching formation (`cbus formation show NAME`);
saved roles are intended assignments. Live peer lists do not advertise roles,
so absent role information is reported as unknown.

The local roster's PID (`listenerPid` in JSON) belongs to the daemon, not the
Codex process. `connection status --json` supplies the exact `threadId` and
observed `consumer.pid`, `consumer.state`, `consumer.startToken` and
`consumer.observedAt`. A retained PID or historical receipt alone is not current
liveness. Inspect with `cbus connection status`; `cbus connect status` instead
joins a channel literally named `status`.

This starts one local cbus supervisor as needed. It registers the exact native
`CODEX_THREAD_ID` with the caller's `HOME`, `CODEX_HOME`, working directory,
native executable and actual open queue database. The open database witness
resolves profile and command-line SQLite overrides; cbus does not guess from the
daemon's environment. If process inspection is unavailable, an explicit
`--codex-sqlite-home ABS_PATH` is a caller-declared fallback, not independent
proof of consumer ownership. Every join or reconnect requires a verified current
interactive CLI holding this exact thread's writable rollout and queue store;
automatically captured process identity must match that writer. Unknown or
ambiguous ownership is refused with a process-inspection diagnostic. Historical
thread origin is not the current frontend: a thread labeled `source="vscode"`
can join when it is running in CLI. Running desktop clients and spawned subagent
threads remain unsupported. Already registered peers still retain queued mail while the CLI is
offline, including across daemon restart. No terminal is opened or moved. iTerm2 and tmux work the same way; terminal placement is a
separate integration concern. Desktop harness clients and remote execution
backends are outside v1. Remote bus channels are supported separately, through
the relay described below.

The daemon reads the existing local inbox and calls `thread/queue/add` through a
non-owning app-server sidecar. It never starts or resumes the recipient's thread.
The running CLI consumes its own queue when idle. The measured cross-process
watcher interval is about ten seconds, and busy sessions wait for their current
turn to complete. Explicit interruption pauses automatic consumption. In the
tested 0.154.0 lifecycle, rejoining or cold-resuming the thread alone preserved
that pause, even with public status `idle`; a subsequent user turn had to complete
before queued input drained. No model calls are used to maintain an idle
connection. Real peer join, disconnect, exit and resume events become
model-visible presence notices. Codex briefly tells the user who joined, left,
departed or was renamed and updates its known roster and role assignments. It
does not send bus acknowledgments or greetings for these events, or repeat roster
reads after each event. These notices can incur a recipient turn; they are not
periodic maintenance calls. Queued notices retain their event timestamps and
describe observed transitions, not a fresh liveness check. Completed-compaction
notices update context without changing membership or requiring a standalone
user-facing reply.

The offline lifecycle canary passed 17 checks using a scratch app-server and a
local fake provider, including a busy turn held for 11.05 seconds and both live
and cold resume after interruption. That source=`vscode` test establishes queue
semantics, not desktop support or actual CLI restart acceptance. See the
[pilot evidence (historical)](architecture/codex-native-queue-pilot.md).

Ordinary CLI tests verify clean exit and resume by exact UUID, `--last`, name,
and picker: the replacement process retains prior conversation input and
consumes mail queued while it was closed exactly once. The same daemon and
connection survive. On-demand reconciliation observes `queued` before resume
and `received` afterward without starting another turn. A separate interrupted
CLI test verifies that the pause survives cold resume and queued input drains
after an explicit user continuation completes. These use isolated local fake
providers; they establish CLI and queue mechanics, not live-model quality.

`queue-ready` means the sidecar can access this thread's native queue storage.
It does not prove the running recipient can consume that queue. An accepted
queue item is not a completed turn or a recipient acknowledgment.
`recordedVersion` is persisted thread metadata, not proof of the currently running
process version. `connection reconcile CHANNEL/ALIAS` can establish receipt by
finding the exact message ID in that thread's user history. This is historical
receipt evidence, not proof of a completed turn, reply, or currently live CLI.
A real reply canary remains necessary to establish the return path.
Upgrading the installed binary alone does not upgrade an already running CLI.

Prefer this path without restart. If the running runtime actually cannot consume
native queued input, update it and resume the **same exact thread** with
`codex resume THREAD_ID`, then connect from inside the resumed conversation.
Permission errors, remote state, or a brand-new thread with no persisted rollout
are separate failures and must not be presented as requiring a restart. No
automatic restart or sandbox relaxation is performed.

### Resume an ordinary native peer

After a normal CLI exit, run `codex resume THREAD_ID` in your terminal with the
same Codex home and profile; use the exact `threadId` from the saved connection.
Inside that resumed conversation, run the original connect command again:

```sh
cbus connect feature-updates advisor-main --json
```

This retains the alias, inbox and delivery position. A previously interrupted
turn still needs explicit user continuation as described above. The separate
`cbus codex ... resume` wrapper below is not required for native connections.

### Sandbox approvals

With trusted bus setup loaded, use direct `cbus ...` calls or its approved
absolute path. If you have not opted in, use normal approval for the exact
command; shell wrappers and compound scripts can require their own approval.

The normal Codex shell sandbox can deny the daemon's Unix socket with
`operation not permitted`, even when `$CBUS_DIR` is writable and the daemon is
already running. Request approval for the exact `cbus connect`, status, or
lifecycle command through the session's normal approval mechanism. Starting the
daemon also creates a persistent local process and bus state. Replies through
`cbus send` write peer locks and inboxes; the default `~/.claude-bus` location may
require command approval because it is outside the workspace.

Keep the session's sandbox and approval settings unchanged. A permission failure
does not require another Codex session or a listener. The live model/skill test
passed with one-time bootstrap approval and sandboxed replies through a writable
`/tmp` bus. The same CLI also replied after daemon restart and queued downtime
mail. A subsequent 65-minute idle test also passed against default
`~/.claude-bus`: no recorded maintenance activity, then one unattended exact
reply. That reply used the existing `cbus send` allow rule, which permits that
command outside the command sandbox. No global setting or rule changed. Fresh
installations need an explicit permission setup; do not infer that an unapproved
workspace sandbox can write the default bus. See the [pilot evidence](architecture/codex-native-queue-pilot.md).

### Explicit optional reply permissions and upgrades

`cbus codex-permissions --scope bus` previews the complete command-namespace
trust used by `install-codex-skills --with-permissions`; add `--install` to opt in.
Rules are installed in the active `$CODEX_HOME/rules` (default `~/.codex/rules`),
even when skills use a custom `--path`. A skill install's `--force` does not
overwrite edited permission rules. To replace those deliberately, use the
permission helper's own `--force`.

`cbus codex-permissions --binary /absolute/path/to/cbus` previews a rule allowing
only that literal executable path followed by `send`. Add `--install` to write it
deliberately under the active Codex home's `rules/cbus.rules`; existing local
edits are preserved unless `--force` is explicitly supplied. The rule permits
that command outside the command sandbox. It does not authorize connect,
recovery, daemon management, or arbitrary shell commands. New CLI sessions load
rules at startup; existing sessions can use their usual exact-command approval.

Use the printed absolute path for replies when using this optional rule. A bare
`cbus`, a different symlink path, or a shell wrapper is a different command prefix.
Ordinary skill installation and selfupdate never grant or broaden permissions;
selfupdate leaves an existing bus opt-in in place. General approval/sandbox
settings are unchanged in both scopes.

Codex skill installation stores a content receipt. Upgrades replace an unchanged
previously shipped skill, preserve edited or untracked skills, and report skips.
The installer and new selfupdate path include Codex skills. When upgrading from
an older updater that only refreshes Claude assets, run `cbus install-codex-skills`
once after upgrading the binary.

An installed binary cannot silently reuse a daemon with a different version or
protocol. `cbus daemon restart` stops the observed process using its PID/start
fence, waits for socket closure **and** lock release, and starts this executable.
Registrations, offsets, uncertain attempts and receipt observations remain in
the journal. An older pilot daemon without the fencing protocol requires explicit
`cbus daemon stop`, then `cbus daemon start` after it exits. A timeout never means
shutdown succeeded. `dev` builds share a version label; release acceptance must
still identify exact bytes by hash.

### Lifecycle and delivery failures

```sh
cbus daemon status --json
cbus daemon restart
cbus connection disconnect feature-updates/advisor-main
```

Disconnect retains the inbox and delivery journal, and stops future submissions.
Items already accepted by Codex remain in its queue. Connecting the same thread
and alias again retains its delivery position. Daemon restart also preserves
pending mail; while it is down, ordinary sends fail visibly and `send --force`
explicitly queues for later. Managed aliases cannot be pruned, stolen by `tail`,
renamed, or reclaimed by a legacy launcher. `leave`/`unregister` explicitly remove
the alias; the daemon then fences that binding and stops delivery.

Native enqueue does **not** deduplicate `clientUserMessageId`. Before submitting,
cbus persists an attempt. A successful response advances the inbox position with
the acceptance record. If the response is lost, cbus looks for the same client ID
in the queue and stored user messages. An internal RPC error can also occur after
storage; only verified pre-enqueue request rejections are automatically retried.
An unresolved outcome remains `uncertain` and blocks later inbox messages; cbus
does not blindly replay it.

Use an on-demand read-only lookup to refresh evidence:

```sh
cbus connection reconcile feature-updates/advisor-main --json
```

`accepted` counts native acceptance, not completed turns. `lastAccepted` describes
only the latest accepted message: `accepted` means an enqueue acknowledgment;
`queued` means observed in native storage; `received` means its exact client ID
was observed in that thread's user history; `unknown` means it was not found at
the observation time, without disproving earlier acceptance. `observedAt` makes
the age of that evidence explicit. `status` reads saved observations; it does not
poll Codex or send a probe. Historical receipt stays recorded even if later
history is pruned. Older journals without `lastAccepted` report receipt unverified.

If reconciliation cannot resolve an uncertain attempt and an operator chooses
to unblock later mail, use the exact `pending.clientId` from status and record why:

```sh
cbus connection abandon feature-updates/advisor-main \
  --pending CLIENT_ID --reason 'Operator accepts unresolved delivery to unblock later mail'
```

This skips **only that local attempt** and retains its identity, inbox bytes and
reason in the journal. It neither cancels the native queue item nor retransmits
it: the original may already have arrived or may still arrive. `abandoned` is
separate from `accepted`. A stale client ID, changed inbox or replaced alias is
refused. Reconciliation and abandonment preserve an explicit disconnection.
There is no automatic resend action for an unknown outcome.

State lives in `$CBUS_DIR/.daemon`; `daemon.log` holds supervisor diagnostics.
The Unix socket is private to the user. This implementation supports macOS/Linux
Codex CLI peers, with local or relay-backed bus channels. The daemon also ships
[native Claude receive](claude.md). OpenCode, desktop harness clients, managed
rename, and automatic launch-at-login remain separate work.
Connections have independent operation lanes and a bounded shared worker pool.
A slow sidecar does not hold the control/status lock or serialize all peers.
Status separates desired connection/queue state, observed CLI consumer ownership,
and timestamped historical receipt evidence.

## Remote bus channels

After configuring the existing relay endpoint and credentials, connect from the
ordinary CLI session with an explicit alias:

```sh
cbus connect dev@server advisor --json
cbus connection status dev@server/advisor --json
cbus send dev@server/worker 'Please review the change'
```

The daemon requires the additive `/tail/durable-v1` relay endpoint. Upgrade the
relay before using native remote connections; an old server is refused instead
of falling back to a stream without durable acknowledgments. Existing Monitor
clients continue using `/tail`.

The relay retains each message until cbus has durably stored it locally. The
daemon then uses the same native queue and recovery journal as a local channel.
A transport acknowledgment does not establish native receipt or a completed
turn. Reconnects deduplicate stored message IDs; a different active consumer
cannot silently take over the same remote mailbox. Consumer presence follows
the actual CLI lifecycle, independently of transport reconnects. Remote
compaction notices remain outside v1. See [relay setup](relay.md).

## Presence, compaction and terminal placement

The daemon observes the exact native CLI's writable rollout, queue store and
process-start identity. `consumer.state` is `online`, `exited`, `unknown`, or
`disconnected`, independently of queue storage and prior receipt. An inconclusive
probe does not produce a leave/rejoin event. Native exit releases consumer
ownership while retaining the alias, inbox and pending delivery for exact-thread
resume; explicit unregister releases the alias. Daemon restart/repeated connect
must not manufacture new presence events. Closing a tmux client or moving/focusing
a terminal is not CLI exit.

Codex v1 reports **completed** compactions to local channel peers using durable
completion records. It does not promise an advance checkpoint window. Pre-compact
hooks are deferred by explicit scope decision. Existing historical compactions
are baselined at attachment; transcript text and summaries are never broadcast.
Compaction notices remain local-only.
The compatibility wrapper and standalone bridge also observe the exact thread's
persisted completion records, obtained through read-only `thread/read`. This
avoids relying on item notifications that a late connection may not receive.
Their notices use the existing best-effort local presence path, with no durable
notice outbox. Historical completions are baselined when that bridge attaches.

`cbus spawn pane CHANNEL --harness codex --name worker` starts an ordinary Codex
CLI whose opening prompt connects its own thread. Harness selection is separate
from the existing terminal interface: `pane` prefers tmux when `$TMUX` is set, including
tmux inside iTerm2; otherwise it splits the caller's iTerm2 session. `tmux` creates
a window in the caller's own tmux session; `window` and `tab` use iTerm2. A stale
known anchor fails instead of selecting an unrelated focused window.

Use `--profile NAME` to select a Codex profile explicitly and `--model MODEL`
to override its model. `--role reviewer` appends the role instructions and supplies
an omitted name, but its Claude `MODEL:` line is not used for Codex: without
`--model`, the Codex profile/default applies. The child gets the
caller's Codex home, bus directory, PATH and cwd, with parent session identity and
remote-executor variables removed. Arbitrary parent CLI flags are not inferred.
From any other terminal, start `codex` normally and invoke `$cbus-connect` inside
it. Desktop harness clients remain v2; this does not restrict terminal choice.

Saved formations preserve Codex harness/backend identity. Automated Codex formation
restore currently refuses with manual exact-thread resume/connect guidance;
it never substitutes a fresh Claude session when a Codex transcript is absent.

## Existing launch and bridge compatibility

The bus is harness-neutral: a Codex CLI session can hold a channel alias the
same as a Claude Code one, and the differences are absorbed by cbus rather
than pushed onto peers. Codex has no equivalent of the `Monitor` tool, so a
codex peer never runs `cbus tail` — cbus does the listening for it, three
compatibility ways depending on how codex is running:

- **`cbus codex [--channel CH] [--alias AL] [--thread ID] [codex args...]`** — the one-command
  interactive path. It stands up a per-peer `codex app-server`, launches a
  `codex --remote` TUI against it, learns the TUI's thread id from a passive
  server connection (hooks do not fire in this topology, so discovery is a
  protocol notification, not a hook), joins the bus as that thread, and runs a
  bridge that turns each bus message into a codex turn — steering an active
  turn when one is in flight, opening one when idle.
- **`cbus codex-bridge <ch>/<alias> --sock PATH [--thread ID] [--no-resume]`** — the bridge
  alone, for wiring up an app-server you manage yourself. It arms as the
  alias's listener (so liveness is real, same as any peer) and skips presence
  frames deliberately: a codex injection costs a full model turn, too much for
  join/leave ceremony.
- **`cbus codex-stop-hook`** — the fallback for plain `codex exec` workers,
  where there is no app-server but hooks do fire: on each Stop event it
  long-polls the inbox under codex's hook timeout and, when traffic arrived,
  returns a block decision that codex injects as a continuation turn. Silence
  lets the stop through — a timeout is treated as a failure, never a signal.

### Resuming through the compatibility wrapper

For this local-only wrapper, args pass through to codex, so an existing session joins the bus by resuming it:

```sh
cbus codex --channel cbus-winport --alias advisor resume 01a06968-3620-7a63-a5ba-1b25c394ccbd
cbus codex --channel cbus-winport --alias advisor resume --last
cbus codex --channel cbus-winport --alias advisor resume            # picker
```

The peer joins under the resumed session id, which is the thread id, so the alias comes back
carrying the history it had. Two things differ from a fresh launch, both forced by the
app-server's behaviour rather than chosen:

A resumed thread never emits `thread/started`. It announces itself through
`thread/status/changed`, so on a resume the wrapper accepts any notification that names a
threadId. A fresh launch emits nothing else in that window, so the cwd hard-check the fresh
path relies on is untouched. cwd is not an identity check on a resume anyway: the session
carries the cwd it was recorded in, which is legitimately not where you are resuming it from.
When the command line names a session id, that id is checked against the thread the server
announces and a mismatch is refused.

The TUI must retain the thread's writer ownership. The wrapper waits for the
server to name the thread before starting the bridge. For a resumed TUI,
the bridge attaches without resuming it again. `--no-resume` provides the same
ownership protection for a standalone bridge. A late connection may receive
status without the full item stream; compaction observation therefore uses
persisted records rather than depending on those notifications.

One consequence worth knowing: an app-server that outlives its wrapper keeps that lock, and the
next resume of the same session is refused until it dies. The wrapper catches SIGTERM and
SIGHUP and tears the group down before exiting, so `pkill`, a closed window and `tmux
kill-session` are all safe (measured: zero survivors, lock released). `kill -9` is not, and
nothing can make it so.

### Launching the compatibility wrapper from another session

`cbus codex` is an interactive TUI: it takes over the terminal it is called from and blocks
until that TUI exits. A model driving a harness must not run it as a tool call, where the TUI
gets no terminal, exits on stdin EOF, and the wrapper dies without ever joining. It needs a
window of its own:

```sh
tmux new-window -c "$PWD" 'cbus codex --channel <ch> --alias <al> resume <session-id>'
tmux new-session -d -s codexpeer -c "$PWD" 'cbus codex --channel <ch> --alias <al>'
```

`-c` is load-bearing, not cosmetic: the window's cwd is the peer's cwd, and it is what `resume
--last` filters on. With no tmux the command goes to a human to run in a new terminal. The
`/bus-codex` skill carries this whole flow, including connecting the launching
Claude session natively first; no parent Monitor is needed. Use
`cbus spawn pane CHANNEL --harness codex --name ALIAS` for
the ordinary CLI integration described above.

A dead codex peer comes back with its history by resuming its own recorded id: a codex peer's
`sessionId` in `cbus list --json` IS its codex session id.

`cbus hook-join` rounds it out: a harness-neutral SessionStart hook that
auto-joins `$CBUS_CHANNEL`, so any harness with hooks can arrive on the bus
without a human typing the join. A committed `profiles/codex.md` carries the
peer-side doctrine (chiefly: never run `cbus tail`; the bridge listens for you).
