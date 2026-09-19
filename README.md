# claudebus

A **file-based message bus for Claude Code and Codex CLI sessions** — an
orchestrator and the worker sessions it spawned, two
windows working the same repo, or a session on your laptop and one on a home
server — so results flow between them live instead of through handoff files you
carry over by hand.

![two live Claude Code sessions on one channel: main joins and stands by, a presence event announces fork-1 joining, main pings it over the bus, and fork-1 wakes and answers](docs/demo-live.gif)

The original Monitor integration below shows the file-bus exchange at the CLI
level. Native CLI connections now let the daemon handle waiting:

![the CLI internals: join, tail, a presence event when a second peer joins, a message arriving framed, and cbus list showing liveness](docs/demo.gif)

And a whole fleet driving itself — one prompt in, then the orchestrator spawns
its coder and reviewer with `cbus spawn pane` and runs a task → review → verdict
loop entirely over the bus:

![a three-peer dev fleet: the orchestrator spawns coder and reviewer as panes, dispatches a task over the bus, routes the result to review, and announces the verdict](docs/demo-fleet.gif)

Claude Code and Codex CLI can connect their existing conversations through a
local daemon. Claude uses its per-session native messaging socket (tested with
2.1.277); Codex uses its experimental native queue API (tested with 0.154.0).
Idle connections require no model polling or Monitor re-arming. Delivery is independent of the terminal: iTerm2, tmux, and
manually launched terminals share the same bus. The client is a Go binary; the
Codex adapter also requires a compatible installed Codex CLI.

> **Scope — bespoke by design.** A personal, single-operator tool wired to one
> specific setup (a small always-on home server reachable through an authenticated tunnel). It's here to
> be *read* — an honest write-up of the architecture and tradeoffs — not packaged
> for others to deploy. Read [docs/security.md](docs/security.md) before pointing
> any of it at a network.

## Quickstart

```sh
# from source
go build -o ~/.local/bin/cbus ./cmd/cbus
cbus install-commands && cbus install-roles && cbus install-codex-skills

# or bootstrap from a release (see docs/install.md), then stay current with
cbus selfupdate
```

Two shells, one channel:

```sh
# shell 1                          # shell 2
cbus join demo                     cbus join demo
cbus tail demo/main                cbus send main "build's green — merging"
```

Inside Claude Code, use `/bus-join` or run `cbus connect demo worker --json`
from the current session. An idle session receives native input without a Monitor
or a restart. See [Claude setup](docs/claude.md) for capability checks, existing
Monitor migration and receipt/recovery semantics. The shell `join`/`tail` example
above remains the legacy file interface.

Inside an ordinary Codex CLI conversation, invoke `$cbus-connect` or run
`cbus connect demo advisor --json`. It joins the current thread without restarting
or launching through cbus. See [Codex setup](docs/codex.md) for exact-command
permissions, presence notifications, lifecycle status and delivery recovery.
Desktop harness clients are outside v1.

Bring a whole fleet back after a reboot:

```sh
cbus formation save myeffort      # snapshot the channel: peers, roles, models
cbus formation resume myeffort    # after the reboot: one command; the restored
                                  # anchor gets a decision brief and reconciles
                                  # the rest itself
```

## What's in the box

- **Channels & aliases** — named N-way registries with real process liveness,
  auto-assigned aliases, presence events, and self-cleaning state —
  [docs/how-it-works.md](docs/how-it-works.md)
- **Session launchers** — `/bus-branch` forks a window with the bus pre-wired;
  `cbus spawn --role coder` opens a fresh peer briefed from a committed role
  file — [docs/usage.md](docs/usage.md)
- **Formations** — save a fleet's shape, restore it with one command, stamp out
  fresh fleets from starter templates; there's a three-peer fleet demo at the
  top of the doc — [docs/formations.md](docs/formations.md)
- **Harness-neutral peers** — ordinary Claude Code and Codex CLI sessions connect
  from inside their conversation. Existing `cbus codex` launches remain supported;
  OpenCode is the next adapter. Terminal placement remains independent —
  [Claude](docs/claude.md), [Codex](docs/codex.md)
- **Cross-machine relay** — a std-lib-only Go daemon extends channels across
  machines (`<channel>@<host>/<alias>`) behind an authenticated tunnel —
  [docs/relay.md](docs/relay.md)

## How this relates to Claude Code's own coordination

Why I built it, and why it stayed: the shapes that fan out inside one task kept
failing the same way. A teammate reports *finished* and never delivers its report,
so the only recovery is asking an agent what it remembers concluding — an open
invitation to reconstruct a verdict after the fact. A peer that owns a terminal and
a file on disk fails visibly instead: scroll its pane, `cat` its inbox.

Claude Code has cross-session messaging of its own since 2.1.224, so the bus is no
longer the only thing that crosses a session boundary. Four mechanisms overlap what
cbus does, and this is where each one lands (measured on 2.1.235, macOS + iTerm2):

|  | Subagent | Agent Teams | SendMessage | Workflow | cbus |
|---|---|---|---|---|---|
| Target has its own terminal | no | pane when configured | session, terminal optional | no | peer, terminal optional |
| Target outlives this session | no | no, unless the lead is killed | yes | no | yes |
| Nesting | 3 layers by default | teammates spawn subagents, not teammates | n/a | n/a | no enforced limit |
| Discoverable by other sessions | no | no, team-scoped | `ListAgents` | no | `cbus list` |
| Reachable from a hook or script | no | no | own session's socket | no | `cbus send` |
| Cross-machine | no | no | Remote Control, web sessions | no | your own relay |
| Non-Claude peer | no | no | no | no | Codex today |
| Readable store | agent transcripts | mailbox, config, tasks | receiving transcript | script + run JSON | `inbox.jsonl`, relay spool |

Agent Teams' pane column is configuration-specific: the default is in-process, and
panes come from `teammateMode: tmux`, which picks iTerm2 when it's there. Teammates
are separate Claude Code instances either way, parented by the terminal rather than
by the lead, and torn down when the lead exits cleanly. Kill a lead ungracefully and
they keep running without one.

**Agent Teams** and **Workflow** are shapes for fan-out inside one task. The ceiling
worth knowing is that only the lead adds teammates: *"Teammates cannot spawn other
teammates — the team roster is flat."* A cbus formation has no such limit, which is
how an orchestrator spawns a coder that spawns its own helpers.

**Cross-session messaging** is the near neighbour. It delivers a long message whole
where a Monitor tail clips it at the documented ~2800-character notification budget,
it needs no follower process, and hooks and Bash children can post into their own
session through `CLAUDE_CODE_MESSAGING_SOCKET`. Its peers are Claude Code sessions,
reached through a socket and a token.

What's left for cbus is an **open** boundary rather than a wider one: a file and a
CLI usable by anything that can write a line, peers that aren't Claude Code, a relay
you own and can inspect, and a mailbox and ledger you can read with `cat`.

A send to a peer whose listener died is refused unless you pass `--force`.
Legacy join/tail registrations have their existing restart and inbox-reset
semantics. Managed native connections retain their inbox, delivery cursor and
uncertain attempts across daemon and exact-session CLI restarts. Claude socket
writes remain unconfirmed until an exact transcript receipt; Codex queue acceptance
is also distinct from recipient history receipt and a completed reply; inspect
`cbus connection status` and use on-demand `reconcile` for evidence. Native relay
subscriptions require the acknowledged-delivery endpoint in the matching relay
release; the old Monitor WebSocket endpoint keeps its legacy semantics.

## Docs

| doc | what's in it |
|---|---|
| [CHEATSHEET.md](CHEATSHEET.md) | the quick-reference card — every verb in one screen |
| [docs/how-it-works.md](docs/how-it-works.md) | store, join, tail, send; delivery semantics, caveats, and how this sits next to the built-in teammate mailbox |
| [docs/install.md](docs/install.md) | releases, `selfupdate`, from-source, what gets installed where |
| [docs/usage.md](docs/usage.md) | forking, spawning, roles, the global channel, presence |
| [docs/formations.md](docs/formations.md) | save / apply / resume, starter templates, birth records, drift anchors |
| [docs/claude.md](docs/claude.md) | Native Claude connections and legacy Monitor migration |
| [docs/codex.md](docs/codex.md) | Codex sessions as bus peers |
| [docs/relay.md](docs/relay.md) | the networked relay and `@host` remote channels |
| [docs/security.md](docs/security.md) | the trust boundary, stated honestly |
| [docs/architecture/](docs/architecture/) | the deep end: system overview, full command reference, wire protocol, port map |

## License

[MIT](LICENSE) — © 2026 Carlos Umanzor. A fun, single-operator personal project:
fork it, read it, take ideas from it — that's what it's here for. No support,
warranty, or contribution process is implied; see the *bespoke by design* note above.
