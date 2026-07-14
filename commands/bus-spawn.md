---
description: Open a fresh session in a new window, joined to a cbus channel
argument-hint: "[window|tab|tmux] [channel|ch@host] [--model m] [--name n]"
allowed-tools: Bash(cbus:*), AskUserQuestion
---

Open a **fresh** Claude Code session (blank transcript — NOT a fork of this one)
in a new terminal, prompted to join a `cbus` channel and arm its own listener,
so this session or any peer can message it.

The user passed: "$ARGUMENTS" — first word is the target (window | tab | tmux;
ask via AskUserQuestion ONLY if empty), optional second word is the channel: a
local name, or `<channel>@<host>` for a relay-backed cross-machine channel
(remote must be explicit; no alias — the child picks its own). Omitted channel
defaults to this session's own channel, else the repo-derived name.

If the user mentions a model anywhere (e.g. "spawn a sonnet worker",
"use opus"), append `--model <m>` — valid values today: sonnet, opus, fable.
If the user names the child (e.g. "name it worker3"), append `--name <n>` —
it becomes the child's bus alias AND session title (alias charset:
[A-Za-z0-9._-]). Omitted: a local channel auto-reserves an alias (main/fork-N)
and titles the child with it; a remote channel leaves the child to pick its
own alias, titling it with the address.

One step: run `cbus spawn <target> [channel] [--model m] [--name n]` and report its
output in one line. The child joins and arms ITSELF — its launch prompt carries the join +
Monitor-arming instructions, so there is nothing to arm on this side and no
bootstrap to print. Verify membership when asked: `cbus list <channel>`
(local) or `cbus list @<host>` (remote).

Do nothing else.
