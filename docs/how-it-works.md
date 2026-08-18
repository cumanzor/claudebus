# How it works

Each participating session **joins a channel** and **arms a listener**:

- **Store** — `~/.claude-bus/<channel>/<alias>/` holds:
  - `meta.json` — registry entry: `{alias, channel, sessionId, listenerPid, ownerPid, cwd, host, ts}` (plus `origin`/`model` birth-record fields; see [Birth records](formations.md#birth-records))
  - `inbox.jsonl` — append-only, one JSON message per line
- **Join** — `cbus join <channel>` auto-picks the alias (`main` if free, then
  `fork-1`, `fork-2`, …), is idempotent for a session already in the channel, and
  **auto-prunes dead peers first** so alias numbers get recycled instead of
  growing forever.
- **Receive** — the session runs `cbus tail <channel>/<alias>` under Claude Code's
  **Monitor** tool (persistent). It runs the blocking follower in-process, so
  *its own pid* becomes the liveness signal, recorded together with its process
  start time (`listenerStart` in `meta.json`) — the identity witness the
  liveness check matches. The follower reframes each stored message into a
  short `◀ cbus msg from=… to=… ts=…` header + the text soft-wrapped at ~440 bytes
  + a `◀ cbus end` marker. Why: the Monitor truncates any single stdout line at
  **500 chars**, so a raw 1-line JSON event forces the receiver into a second inbox
  read; emitting several short lines (which the Monitor batches into one
  notification) delivers a long message whole in the first event. Push delivery: an
  idle session is woken immediately; a busy one sees it when its step completes.
  Remote `@host` tails get the identical framing — the **relay** reframes each
  message server-side into one multiline ws frame (a multiline frame is capped
  per-line at 500, not 500 total), so long cross-machine messages arrive whole
  too. Both paths share a ~2800-char per-notification ceiling. Past it, remote frames carry an
  in-band `⚠truncated~<N>B` header notice; on the local path the harness itself appends a
  `...(truncated)` marker — visible on both paths, by different mechanisms.
- **Send** — `cbus send <channel>/<alias> "text"` appends a line to the target's
  inbox. Within your own channel a bare alias works: `cbus send fork-1 "text"`.
  The sender's `from` is resolved automatically (this
  session's own registration where possible; unjoined senders fall back to an unroutable
  `hostname-PID`).

## Design details worth knowing

- **No lost messages during setup.** `join` truncates the inbox and the *first*
  arm replays the whole inbox from the start, so anything sent between *join* and
  *arming the Monitor* is still delivered — `send` accepts a joined-but-not-yet-
  armed peer for exactly this reason. A *re*-arm resumes from a durable
  per-peer cursor at the last delivered message, so nothing is redelivered —
  and nothing that arrived while unarmed is lost.
- **Liveness is a real process, not a stale flag.** The tracked `listenerPid` *is*
  the follower process. When a window closes or the Monitor is stopped, the peer flips
  to `off` on its own, and `cbus send` refuses to message a dead window (override
  with `--force`, which queues the line; the next re-arm resumes from the
  durable cursor and delivers it). Two edge cases are hardened:
  - *pid recycling* — a live pid is only trusted if its recorded start-time
    witness (`listenerStart`) still matches the process now wearing the pid, so
    an unrelated process that inherited the number doesn't read as a false
    `listen`.
  - *crash-orphaned listener* — on arm, cbus also records `ownerPid`, the owning
    `claude` process. If the session is hard-killed (crash, `kill -9`), the follower
    can survive as an orphan with a live pid — but its `ownerPid` is gone, so the
    peer still reads `off`. (On a *clean* exit Claude Code stops the Monitor, which
    kills the follower directly; this covers the abnormal path.)
- **Self-cleaning registry.** `join` prunes dead peers in its channel before
  picking an alias; empty channels are removed. A peer that joined but hasn't
  armed its Monitor yet gets a 10-minute grace window so it can't be swept
  mid-setup. `cbus prune` does a manual sweep across all channels.

## Caveats

- **No sender authentication.** Anything that can write to `~/.claude-bus` can inject a
  message, and `from` is taken at face value. Claude Code treats an incoming bus message
  as an untrusted peer request (no permission escalation), which is the right guardrail —
  but don't expose the bus directory beyond your own machine.
- **Channels are namespaces, not isolation.** Any local process can send to any channel;
  the channel only scopes addressing and cleanup.
- **Delivery is push — an idle session wakes and can act autonomously.** A Monitor
  event re-invokes the receiving agent on its own: a session sitting idle at the
  prompt processes the message (and can reply) with no human present. Only a *busy*
  session defers — the event queues until its current step completes rather than
  interrupting it. Corollary: a peer message can trigger action while you're away,
  which is why incoming messages are treated as untrusted peer requests.
- **No broadcast primitive.** `cbus send` targets one peer; message N times to reach N peers.
- **No runtime dependencies.** The client is a single static Go binary (the bash-era
  python3 and `tail -F` requirements are gone).
- **Message size cap:** messages over 1 MiB are rejected, matching the relay's `/send` cap.

## Why not the built-in teammate mailbox?

**Superseded 2026-08-18.** Claude Code gained cross-session messaging (2.1.224+), so the
closed boundary this section was built on is open. The original finding is kept at the
bottom because it explains why claudebus looks the way it does, not because it still
describes the product.

What the built-ins do today: `ListAgents` enumerates independent sessions on the machine
and `SendMessage` addresses them by name, over a per-session socket at
`/tmp/cc-socks/<pid>.sock`. Hooks and Bash children are handed
`CLAUDE_CODE_MESSAGING_SOCKET` and `CLAUDE_CODE_MESSAGING_TOKEN` and may post into their
own session, so external processes are not shut out either. Teammates are separate Claude
Code instances rather than in-process workers, and under `teammateMode: tmux` each gets its
own terminal pane.

What still holds: teammates are addressed through a session-scoped team roster rather than
the cross-session one, that roster is flat (a teammate cannot spawn a teammate, only
foreground subagents), and every reachable peer is a Claude Code session.

So the case for cbus is no longer that it is the only thing crossing a session boundary. It
is that the boundary is **open** rather than merely wider: a file and a CLI any process can
use with no socket handshake and no token, peers that are not Claude Code (Codex today,
with the protocol harness-open), a relay you own and can inspect, and an explicit on-disk
mailbox and ledger with liveness and replay semantics you can read with `cat`.

> *Original finding (2026-07, accurate when written and now historical):* Claude Code's
> teammate `SendMessage` is **closed by design**: teammates are spawn-bound subprocesses,
> registered with their parent session **in-process at spawn time**. There *are* team files
> on disk (`<config>/teams/session-<sid>/` — config, inboxes), but they are not a delivery
> path: a hand-launched `claude` process with matching `--team-name` /
> `--parent-session-id` flags comes up alive yet is unreachable via SendMessage, and
> writing to the inbox files registers nothing (verified empirically). A session can only
> message agents it forked itself — there is no cross-session addressing at all.
