# claudebus and Claude Code's own coordination

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
