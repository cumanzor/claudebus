---
description: Open a fresh session in a new window, both sides joined to a cbus channel
argument-hint: "[window|tab|tmux|pane] [channel|ch@host] [--model m] [--name n]"
allowed-tools: Bash(cbus:*), AskUserQuestion
---

Open a **fresh** Claude Code session (blank transcript — NOT a fork of this one)
in a new terminal, prompted to connect natively to a `cbus` channel through the daemon,
and connect THIS session to the same channel first, so parent and child can
message each other immediately.

For a **codex** peer rather than a Claude one, use `/bus-codex`: `cbus spawn`
defaults to Claude Code; `--harness codex` launches an ordinary Codex CLI
whose own session connects through the daemon.

The user passed: "$ARGUMENTS" — first word is the target (window | tab | tmux |
pane; ask via AskUserQuestion ONLY if empty), optional second word is the channel: a
local name, or `<channel>@<host>` for a relay-backed cross-machine channel
(remote must be explicit; no alias — the child picks its own). Omitted channel:
derive it yourself before step 1, the way spawn would — this session's own
channel (the channel half of `cbus whoami`'s first line; it exits 1 when not
joined), else the git toplevel basename, else `global` — and use that name in
every step below.

If the user mentions a model anywhere (e.g. "spawn a sonnet worker",
"use opus"), append `--model <m>` — valid values today: sonnet, fable,
claude-opus-4-8. "opus" is temporarily pinned to Opus 4.8: pass
`claude-opus-4-8` verbatim, never bare `opus` (which now resolves to Opus 5).
If the user names the child (e.g. "name it worker3"), append `--name <n>` —
it becomes the child's bus alias, its session title, and (tmux target) the
tmux window name (alias charset: [A-Za-z0-9._-]). Omitted: a local channel auto-reserves an alias (main/fork-N)
and titles the child with it; a remote channel leaves the child to pick its
own alias, titling it with the address.

1. Connect this session first with `cbus connect CHANNEL [ALIAS] --json`.
   For a relay, use an explicit alias. If already connected, preserve the exact
   address. Follow `/bus-join` for capability errors, roster and presence handling.
   Do not create a Monitor or tail loop.
2. Run `cbus spawn <target> <channel> [--model m] [--name n]`. The child receives
   native connect instructions and uses its assigned alias (claiming the launch
   reservation for a local channel).
   Terminal placement is independent of delivery.

Then confirm in one line: channel, this session's address, the child's alias
(from the spawn output), and the target. Verify membership when asked:
`cbus list <channel>` (local) or `cbus list @<host>` (remote).

Do nothing else.
