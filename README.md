# claudebus

A **file-based message bus for Claude Code and Codex CLI sessions** — an
orchestrator and the worker sessions it spawned, two
windows working the same repo, or a session on your laptop and one on a home
server — so results flow between them live instead of through handoff files you
carry over by hand.

*Two Claude Code sessions on one channel: one pings, the other answers.*
(Claude Code's own peer-message notice is trimmed from this and the
formation recording below, for clarity.)

Claude Code and Codex CLI can connect their existing conversations through a
local daemon: native Codex since **v0.12.0**, native Claude since **v0.13.0**.
Claude uses its per-session native messaging socket;
Codex uses its experimental native queue API. Release field checks used Claude
2.1.278 and Codex 0.155.1 on macOS / 0.154.0 on Linux; see the
[release and validation](https://github.com/cumanzor/claudebus/releases/tag/v0.13.0).
Idle connections require no model polling or Monitor re-arming. Delivery is
independent of the terminal: iTerm2, tmux, and
manually launched terminals share the same bus. The client is a Go binary; the
Codex adapter also requires a compatible installed Codex CLI.

> **Scope — bespoke by design.** A personal, single-operator tool wired to one
> specific setup (a small always-on home server reachable through an authenticated
> tunnel). It's here to
> be *read* — an honest write-up of the architecture and tradeoffs — not packaged
> for others to deploy. Read [docs/security.md](docs/security.md) before pointing
> any of it at a network.

## Quickstart

```sh
# from source
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" \
  -o ~/.local/bin/cbus ./cmd/cbus
cbus install-commands && cbus install-roles && cbus install-codex-skills

# or bootstrap from a release (see docs/install.md), then stay current with
cbus selfupdate
cbus daemon restart   # loads the new binary; a stale daemon refuses new connects
```

Two CLI conversations, one channel — ask each session to run its own commands:

```sh
# Inside Claude Code (or use /bus-join demo worker)
cbus connect demo worker --json
cbus list demo

# Inside Codex CLI (or ask $cbus-connect to join demo as advisor)
cbus connect demo advisor --json
cbus list demo
cbus send demo/worker --from demo/advisor 'Please review the diff'
```

Supported running CLI sessions connect without a restart or special launcher.
For seamless Codex command permissions, opt in once with
`cbus install-codex-skills --with-permissions`, then restart/resume the CLI to load
those rules. This trusts all cbus subcommands; ordinary skill installation keeps
normal command approvals. See the [Codex cheat sheet](CHEATSHEET.md#codex-cli-quick-reference)
and [full setup](docs/codex.md).

Claude uses [native receive](docs/claude.md); existing Monitor peers need deliberate
migration. Neither native path needs a Monitor or a recurring roster check.
Native receive supports macOS/Linux; desktop harness clients remain outside v1.
The shell `join`/`tail` interface is [legacy](CHEATSHEET.md#legacy-joinmonitor-peers-only).

Bring a supported Claude formation back after a reboot:

```sh
cbus formation save myeffort      # snapshot the channel: peers, roles, models
cbus formation resume myeffort    # after the reboot: one command; the restored
                                  # anchor gets a decision brief and reconciles
                                  # the rest itself
```

Formations preserve Codex identity too, but automatic Codex restore/bootstrap is
not yet supported. Resume the recorded Codex thread and connect it manually;
cbus will not substitute a fresh Claude session.

## What's in the box

- **Channels & aliases** — named N-way registries with real process liveness,
  auto-assigned aliases, presence events, and self-cleaning state —
  [docs/how-it-works.md](docs/how-it-works.md)
- **Session launchers** — `/bus-branch` forks a window with the bus pre-wired;
  `cbus spawn --role coder` opens a fresh peer briefed from a committed role
  file — [docs/usage.md](docs/usage.md)
- **Formations** — save a fleet's shape, restore it with one command, stamp out
  fresh fleets from starter templates, or checkpoint the current channel with
  `/save-formation`; see the [advanced example](#advanced-example) below —
  [docs/formations.md](docs/formations.md)
- **Harness-neutral peers** — ordinary Claude Code and Codex CLI sessions connect
  from inside their conversation. Existing `cbus codex` launches remain supported;
  OpenCode is the next adapter. Terminal placement remains independent —
  [Claude](docs/claude.md), [Codex](docs/codex.md)
- **Cross-machine relay** — a std-lib-only Go daemon extends channels across
  machines (`<channel>@<host>/<alias>`) behind an authenticated tunnel —
  [docs/relay.md](docs/relay.md)

## Advanced example

*A four-peer formation: an orchestrator spawns a coder and a reviewer, a
documenter joins, and the four coordinate a task entirely over the bus.*

See [docs/formations.md](docs/formations.md) for how to save, resume, and
stamp out a fleet like this one.

## How this relates to Claude Code's own coordination

Claude Code has its own ways to fan sessions out and message across them:
Subagents, Agent Teams, cross-session SendMessage, Workflow. cbus overlaps
some of that, but stays open where those stay closed: any peer that can
write a line, not just Claude Code, a relay you own, and a mailbox and
ledger you can read with `cat`. See
[docs/claude-code-coordination.md](docs/claude-code-coordination.md) for
the comparison, including why I built it and where each mechanism lands
against it.

A send to a dead listener is refused unless `--force`; see the
[send gate](docs/architecture/command-reference.md#cbus-send-target---from-x---force-text--local).
Each transport keeps its own resume and retained-mail behavior across a
restart: [Claude](docs/claude.md), [Codex](docs/codex.md), the
[legacy transport](docs/how-it-works.md), and the [relay](docs/relay.md).

## Docs

| doc | what's in it |
|---|---|
| [CHEATSHEET.md](CHEATSHEET.md) | daily commands, Codex setup/resume, Claude, relay, formations and compatibility paths |
| [docs/how-it-works.md](docs/how-it-works.md) | native daemon delivery, legacy file transport, receipt semantics and caveats |
| [docs/install.md](docs/install.md) | releases, `selfupdate`, from-source, what gets installed where |
| [docs/usage.md](docs/usage.md) | forking, spawning, roles, the global channel, presence |
| [docs/formations.md](docs/formations.md) | save / apply / resume, starter templates, birth records, drift anchors |
| [docs/claude.md](docs/claude.md) | Native Claude connections and legacy Monitor migration |
| [docs/codex.md](docs/codex.md) | Codex sessions as bus peers |
| [docs/relay.md](docs/relay.md) | the networked relay and `@host` remote channels |
| [docs/security.md](docs/security.md) | the trust boundary, stated honestly |
| [docs/claude-code-coordination.md](docs/claude-code-coordination.md) | why claudebus exists next to Subagents, Agent Teams, SendMessage and Workflow |
| [docs/shared-instructions.md](docs/shared-instructions.md) | repo-policy vs harness-join-instructions split, for AGENTS.md/CLAUDE.md authors |
| [docs/architecture/](docs/architecture/) | the living references: current architecture, the wire and disk protocol, command reference, cross-harness scope |
| [docs/history/](docs/history/) | dated records: acceptance reports (incl. the native Claude receive milestone), decision packages, explorations, and legacy (the bash-era spec, the original overview, the Monitor stopgap) |

## License

[MIT](LICENSE) — © 2026 Carlos Umanzor. A fun, single-operator personal project:
fork it, read it, take ideas from it — that's what it's here for. No support,
warranty, or contribution process is implied; see the *bespoke by design* note above.
