# Multi-harness exploration: Codex CLI, Grok Build, OpenCode as cbus peers

*Exploration, 2026-07-18. Not a commitment — a map of what each harness offers and what
cbus would need. Simplicity rule in force: file-based / relay-based approaches only, no
new daemons unless a harness leaves no alternative.*

Research basis: cbus source audit (this repo, HEAD); `openai/codex` 0.144.1 (npm binary
probed locally + developers.openai.com docs); `xai-org/grok-build` (Rust, open-sourced
2026-07-16, docs.x.ai/build); `sst/opencode` 1.18.3 source + live probes against 1.17.18.
Detailed findings are summarized inline; anything marked *unverified* needs a prototype
before building on it.

## 1. Where cbus is actually coupled to Claude Code

The data plane is already harness-neutral. A foreign agent that can run shell commands
can `cbus send` today, and one that can run a killable background process capturing
stdout can run `cbus tail` today. The coupling concentrates in four places:

| # | Coupling | Where | Severity |
|---|---|---|---|
| 1 | Session identity is exactly `os.Getenv("CLAUDE_CODE_SESSION_ID")` | `internal/client/identity.go:26` | The one real blocker. Absent → peer registers with empty sessionId and can never resolve itself again (`whoami`/`leave`/`rename`/`branch` fail; `send` from falls to `$CBUS_ALIAS` or unroutable `host-PID`) |
| 2 | ownerPid walks ancestors for a process named `claude`/`claude-*` | `internal/client/marker.go:60-80` | Fails open — no match writes `ownerPid: null` and liveness short-circuits alive (`liveness.go:96-98`). Foreign peers arm and read `listen` today; they only lose the crash-orphan prune guard |
| 3 | Launch argv is pure Claude CLI (`claude --resume <sid> --fork-session --model … --name …`, or `ccs <profile>` prefix) | `harness.go:234-262`, `spawn.go:151`, `formation_apply.go:295` | Blocks `spawn`/`branch`/`formation apply` for foreign peers, nothing else. `TerminalForker` takes a plain `Argv []string`, so a per-harness launcher slots in cleanly |
| 4 | Bootstrap prompts hardcode "Claude Code session", "arm the Monitor tool", ws arm-spec | 8 string constants: `bootstrap_prompt.go`, `spawn.go:15-24`, `formation_kickoff.go:15-19` | Prompt text only. "Monitor" appears in zero code paths — the follower is a plain stdout streamer |

Also relevant: hook stdin decoding assumes Claude's `{session_id, trigger}` JSON
(`harness.go:101-104`); transcript discovery assumes `~/.claude/projects/*/<sid>.jsonl`
(`transcript.go:29-62`, only blocks formation resume/fork); the Monitor wrap constants
(500/440/2800 in `core/frame.go`) are measured Claude properties — wrong on another
harness is cosmetic, not protocol. The relay is fully agnostic: bearer-subprotocol ws,
channel/alias addressing, presence from connection lifecycle. The remote path is
*more* portable than the local one.

~~Liveness note for hand-rolled followers: `MetaListenerAlive` requires the listener
pid's argv to contain the raw inbox path.~~ **Obsolete since M6.2 (9a3a075,
2026-07-20):** liveness is purely structural now — (pid, proc-start-time)
witness plus non-zombie, no argv or comm clause. A foreign follower reads
alive with no argv requirement at all.

## 2. Grok Build — near drop-in

xAI's official CLI (`github.com/xai-org/grok-build`, Rust, binary `grok`). Not a fork;
ports individual tool impls from codex and opencode, and scans Claude Code's config
surface for compatibility on purpose.

**Receive: its `monitor` tool is functionally identical to Claude's Monitor.**
Persistent background command, each stdout line becomes a notification injected into
the conversation, can wake an idle session for a new turn, stopped by killing the
process. The docs' own example is `tail -f | grep --line-buffered`. `cbus tail` should
run under it unchanged. One risk: an undocumented volume governor auto-stops monitors
that emit too much — the follower's batched multi-line frames are probably fine, but
this is the first thing to test. Like Claude, the model must be told to arm it
(bootstrap prompt), which is already cbus's flow.

**Identity:** `GROK_SESSION_ID` is confirmed in hook env; *unverified* whether the
bash tool exposes it to agent-run shell commands. If not, `[session] load_envrc`
(default on) can inject it via `.envrc`, or hooks can do the join (see below).
Session ids are UUIDv7; `-s <uuid>` sets one at create (create-only, Claude-style
anti-overwrite), `-r`/`-c` resume, `--fork-session` exists — so even `branch` maps.

**Liveness:** binary comm is `grok` (`xai-grok-pager` for from-source builds) — a
two-entry addition to the ownerPid name predicate. Bonus primitive: `~/.grok/leader.lock`
holds the leader pid as plain text.

**Commands/config compat is zero-config and broad:** it reads `~/.claude/settings.json`
hooks, CLAUDE.md, Claude skills/commands/MCP directly. The installed `/bus-*` commands
in `~/.claude/commands` likely appear in Grok as-is (*unverified*). Its hook stdin is
camelCase (`sessionId`) vs cbus's expected `session_id`, so `hook-exit`/`hook-compact`
need a lenient decoder or a `--session-id` flag even though the hook *wiring* is shared.

**Spawn:** headless `grok -p … --output-format json` prints the sessionId; interactive
launch with a positional first prompt needs verification. Accepts Claude flag aliases
(`--allowedTools`, `--dangerously-skip-permissions`).

**Verdict: cheapest integration by far.** Env-var seam + name predicate + launcher
argv + prompt template, and possibly nothing else.

## 3. OpenCode — small plugin, best delivery semantics

`sst/opencode`, TypeScript, single process (comm `opencode`; the Go TUI is gone —
the TUI is a worker thread, so there is exactly one pid).

**Receive: no monitor-like tool, but the plugin system covers it fully** (confirmed
end-to-end by live probe). A plugin in `~/.config/opencode/plugin/` gets a complete
in-process SDK `client`, can run background work for the process lifetime, subscribe
to the event bus (`session.created`, `session.idle`, `session.status`), and inject
messages via `client.session.prompt`. The killer semantic: **prompting a busy session
queues at the next step boundary** — the run loop re-reads the message log every
iteration, so a mid-turn delivery is absorbed into the same run. That is *better*
than Claude's idle-wake-or-wait-for-step model. `noReply: true` drops a note into
context without triggering a turn.

Proposed shape (keeps the follower and its liveness/framing semantics intact): the
plugin spawns `cbus tail <ch>/<al>` as a child process and forwards each framed
message via `client.session.prompt`. Follower pid is real, its argv carries the inbox
path, all three liveness clauses pass. Roughly 60 lines of TS, shipped in this repo
(e.g. `integrations/opencode/cbus.ts`) and installed by a new `install-plugin` verb.

**Identity: solved cleanly by the `shell.env` plugin hook** (confirmed live) — three
lines inject `CBUS_SESSION_ID=<sessionID>` into every shell command the agent runs,
so `cbus send`/`join` self-identify with no registry lookup. This is the strongest
argument for making cbus's `SessionID()` honor a generic `CBUS_SESSION_ID` first.

**Caveats:** plugins must use the in-process `client`, not `serverUrl` (which lies
when no port is bound — the default TUI binds *no* TCP port; HTTP requires `--port`).
Plugin dir glob is top-level only, no subdirectories. Sessions live in SQLite
(`~/.local/share/opencode/opencode.db`), not greppable files.

**Commands:** `~/.config/opencode/command/**/*.md`, `$ARGUMENTS`/`$1` args, shell
interpolation — the `/bus-*` skills port almost verbatim; another `install-commands`
target.

**Spawn:** `opencode run [msg]` is non-interactive; for a TUI peer, launch plain
`opencode` and deliver the bootstrap as the first inbox message (the plugin injects it
on `session.created`), or use `--port` + `/tui/append-prompt` + `/tui/submit-prompt`.
No `--fork-session` analogue → gate `branch`, support `spawn`.

**Verdict: one small plugin away.** Most work of the two easy harnesses, best
runtime behavior once wired.

## 4. Codex CLI — ~~compromised listener~~ full push via app-server (updated 2026-07-21)

> **Update 2026-07-21 (supersedes this section's verdict; probes on bdx
> `cbus-6ij.4`).** Live spikes on codex-cli 0.145.0 proved: (a) the Stop-hook
> block-continuation works, chains indefinitely, and the hook timeout is
> configurable past 600s — a "parked listener" long-poll makes a worker peer
> permanently reachable; (b) `codex app-server --listen unix://` speaks
> WebSocket-over-UDS, and an external client's `turn/start` wakes an idle
> thread AND renders inside a live `codex --remote` TUI, composer intact.
> True push exists. The v2 path below is therefore promoted to primary (per-
> peer socket, no machine daemon — reconciliation with the simplicity rule in
> design-space.md §7), with the Stop-hook park as the worker/fallback path.
> The original analysis below stands as the record of the pre-probe picture.

`openai/codex` (Rust core behind a Node shim on npm installs — track the native child
whose comm is `codex`, not the `node` parent).

**Receive: no background-process-into-context primitive and no idle wake.** Two paths:

- **Stop-hook long-poll (v1 recommendation).** Hooks are stable and on by default.
  A `Stop` hook may block up to its timeout (default 600s); returning
  `{"decision":"block","reason":"<inbox text>"}` injects the reason as a new user
  turn, with `stop_hook_active` as the loop guard. So: a `cbus codex-stop-hook` verb
  that reads hook stdin (`session_id` present), long-polls the inbox, and emits the
  continuation JSON. Delivery is turn-boundary only — once the hook returns without
  blocking, the session is idle and unreachable until the human types. And while the
  hook blocks, the turn is held open. Acceptable for a spawned worker peer; poor for
  a human's interactive session. The Stop-continuation semantics are documented but
  were not executed in our probe — prototype first.
- **App-server bridge (v2, heavier).** `codex app-server --listen unix://…` owned by
  cbus, TUIs launched with `codex --remote`; an external process can then
  `turn/start` (new turn) or `thread/inject_items` (silent context append) against
  the shared thread. True push, but violates the simplicity rule (a daemon per
  machine) and multi-client-per-thread behavior is unverified. A plain standalone
  TUI is externally unreachable, confirmed.

**Friction:** hooks are trust-gated per content-hash — auto-provisioned peers need a
one-time interactive `/hooks` approval or `--dangerously-bypass-hook-trust` on the
spawn argv. Custom prompts are deprecated (skills replace them). **No session-id env
var exists at all** — id comes from hook stdin, the `codex exec` banner, or
`~/.codex/session_index.jsonl`. The clean fix is a `SessionStart` hook that runs
`cbus join $CBUS_CHANNEL --session-id <id-from-stdin>` when cbus spawned the process
with `CBUS_CHANNEL` set, which requires join/leave/rename to accept `--session-id`.
`notify` (argv-JSON on `agent-turn-complete`) is send-side only; useful as a presence
heartbeat. MCP cannot push unsolicited content. `codex exec resume <id> "prompt"`
exists; `codex fork` exists, so branch may map (*unverified* semantics).

**Verdict: integrate as sender + spawned worker with turn-boundary delivery; document
that an idle interactive Codex session cannot be woken.** Revisit the app-server
bridge only if that limitation bites.

## 5. Comparison

| | Grok Build | OpenCode | Codex CLI |
|---|---|---|---|
| Push receive | `monitor` tool, ≡ Claude Monitor, `cbus tail` as-is | plugin spawns follower, injects via `session.prompt` | Stop-hook long-poll; turn-boundary only |
| Idle wake | yes | yes | **no** (only while hook blocks) |
| Busy delivery | next step (like Claude) | **queued into the live run** | next turn boundary |
| Session id | `GROK_SESSION_ID` (hooks; bash env unverified) | `shell.env` plugin hook → any var we choose | none; hook stdin / banner / index file |
| Liveness comm | `grok` / `xai-grok-pager` | `opencode` (single pid) | `codex` native child under `node` shim |
| `/bus-*` commands | reads `~/.claude/commands` zero-config (verify) | `command/*.md` port | skills (prompts deprecated) |
| spawn | `grok -p` / TUI + prompt | `opencode run`; TUI via first-inbox-message | `codex exec`; TUI spawn + trust flags |
| branch (fork) | `--fork-session` exists | none → gate | `codex fork` (verify) |
| Integration cost | ~0 new code beyond core seams | ~60-line plugin + install verb | new hook verb + `--session-id` + trust handling |

## 6. Proposed increments (all small, ordered)

1. **Core seams, harness-neutral (unblocks everything).**
   `SessionID()` → ordered lookup `CBUS_SESSION_ID`, `CLAUDE_CODE_SESSION_ID`,
   `GROK_SESSION_ID`; add `--session-id` to join/leave/rename/send for env-less
   harnesses. Extend the ownerPid name predicate to `{claude, grok, xai-grok-pager,
   opencode, codex}` (still fail-open). Lenient hook stdin decoding (`session_id` |
   `sessionId`).
2. **Grok Build pilot.** No new code expected: set the env var, `cbus join`, ask the
   agent to arm `cbus tail` under its monitor tool. Verifies the volume governor,
   bash-env session id, and whether `~/.claude/commands` skills surface.
3. **OpenCode plugin.** `integrations/opencode/cbus.ts`: spawn follower, forward
   frames via `client.session.prompt`, `shell.env` → `CBUS_SESSION_ID`; plus an
   `install-commands` target for `command/*.md`.
4. **Codex Stop-hook verb.** `cbus codex-stop-hook` + SessionStart auto-join;
   document the idle-unreachable limitation.
5. **Harness-aware spawn (when 2-4 prove out).** `harness` field on formation peers
   and reservation meta; `HarnessLauncher` mapping per-harness argv
   (resume/fork/model/name flags); bootstrap prompt templates parameterized on the
   arm mechanism. `branch` gated on fork capability per harness; `spawn` is the
   universal verb.

Deliberately out of scope for now: any per-machine bridge daemon (Codex app-server,
Grok `leader.sock` ACP injection, OpenCode HTTP servers). The relay's agnosticism
means any of these can become a *remote-style* peer later by speaking the ws leg
directly, without touching the file protocol.

## 7. Open questions before building

- Grok: monitor volume-governor threshold vs cbus frame bursts; `GROK_SESSION_ID` in
  bash tool env; Claude-commands compat actually surfacing `/bus-*`; interactive
  launch with a positional first prompt.
- OpenCode: plugin behavior across multiple concurrent sessions in one process
  (which session does the follower belong to — likely needs per-session state keyed
  off `session.created`); `serverUrl` fix-up in 1.18.x.
- Codex: Stop-hook block-continuation actually works as documented (5-minute
  prototype); `codex fork` semantics; hook trust UX when cbus provisions the config.
