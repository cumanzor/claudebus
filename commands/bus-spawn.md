---
description: Open a fresh session in a new window, both sides joined to a cbus channel
argument-hint: "[window|tab|tmux|pane] [channel|ch@host] [--model m] [--name n]"
allowed-tools: Bash(cbus:*), Monitor, AskUserQuestion
---

Open a **fresh** Claude Code session (blank transcript — NOT a fork of this one)
in a new terminal, prompted to join a `cbus` channel and arm its own listener —
and join THIS session to the same channel first, so parent and child can
message each other immediately.

The user passed: "$ARGUMENTS" — first word is the target (window | tab | tmux |
pane; ask via AskUserQuestion ONLY if empty), optional second word is the channel: a
local name, or `<channel>@<host>` for a relay-backed cross-machine channel
(remote must be explicit; no alias — the child picks its own). Omitted channel:
derive it yourself before step 1, the way spawn would — this session's own
channel (the channel half of `cbus whoami`'s first line; it exits 1 when not
joined), else the git toplevel basename, else `global` — and use that name in
every step below.

If the user mentions a model anywhere (e.g. "spawn a sonnet worker",
"use opus"), append `--model <m>` — valid values today: sonnet, opus, fable.
If the user names the child (e.g. "name it worker3"), append `--name <n>` —
it becomes the child's bus alias AND session title (alias charset:
[A-Za-z0-9._-]). Omitted: a local channel auto-reserves an alias (main/fork-N)
and titles the child with it; a remote channel leaves the child to pick its
own alias, titling it with the address.

Three steps:

1. **Join this side first** — skip steps 1 and 2 entirely if this session
   already has a cbus Monitor armed for this channel. Local channel: run
   `cbus join <channel>` (idempotent; auto-picks `main`/`fork-N`, prunes dead
   peers) and note the `channel/alias` it prints. Joining before spawning means
   a fresh channel gives the parent `main` and the child `fork-N` — the same
   layout as `cbus branch`; don't reorder to change that. Remote channel
   (`<channel>@<host>`): there is no join verb — pick an explicit alias (short
   hostname/role, e.g. `mbp`) and run `cbus tail <channel>@<host>/<alias>` to
   get the **Monitor ws arm spec** (requires `cbus auth` credentials; if
   missing, tell the user to run `cbus auth set <host>`).
2. **Arm this session's listener** with the **Monitor** tool, persistent —
   local: command `cbus tail <channel>/<alias>`, description
   `cbus:<channel>/<alias>`. Remote: arm from the ws spec (`ws:` source, NOT a
   command); if a `[WebSocket closed]` event later fires, re-run the same
   `cbus tail` and arm the fresh spec. ⚠️ Never run a **local** `cbus tail` in
   Bash — it execs a follower that never exits, so a Bash call blocks forever
   and delivers nothing; it is the Monitor tool's event source only.
3. Run `cbus spawn <target> <channel> [--model m] [--name n]`. The child joins
   and arms ITSELF — its launch prompt carries the join + Monitor-arming
   instructions, so there is nothing more to arm and no bootstrap to print.

Then confirm in one line: channel, this session's address, the child's alias
(from the spawn output), and the target. Verify membership when asked:
`cbus list <channel>` (local) or `cbus list @<host>` (remote).

Do nothing else.
