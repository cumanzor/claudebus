---
description: Rearrange the channel's live peers into a tmux pane layout described in plain English
argument-hint: "<layout in words> | scatter | focus <alias>"
allowed-tools: Bash(cbus:*)
---

Rearrange peers that are ALREADY RUNNING into the terminal layout the user
described. This moves live sessions between panes and windows; it never launches,
never closes, and never touches a transcript. tmux only — iTerm2 has no verb for
moving a running session between surfaces.

The user asked: "$ARGUMENTS"

Your job is the translation. `cbus` takes a pane-tree spec, not English:

| spec | means |
|---|---|
| `a \| b` | two columns, a left, b right |
| `a / b` | two rows, a on top, b below |
| `a \| (b / c)` | left column a, right column split into b over c |
| `(a / b) \| (c / d)` | two columns, each split into two rows |
| `a:30% \| b` | same, with a pinned to 30% of the window width |

`/` binds tighter than `|`, so `a | b / c` already means `a | (b / c)`. A size on a
child of a `|` node is a width, on a child of a `/` node a height. Every alias must
name a live peer, and each may appear once.

Steps:

1. Run `cbus list` (or `cbus list <channel>`) and read the ACTUAL aliases. The user
   will say "the reviewer" or "my coder"; the spec needs the alias as registered.
   If a name they used matches nothing, say which and stop — do not guess a peer.
2. Pick the verb:
   - a layout description → `cbus arrange '<spec>'` (single-quote it; `|` and `(`
     are shell metacharacters)
   - "break these apart", "give each its own window", "undo that" →
     `cbus scatter [channel]`
   - "go to X", "show me X", "focus X" → `cbus focus <channel>/<alias>`
3. Order the spec the way the user described it, left to right and top to bottom.
   The layout is built in the window of the FIRST alias in the spec, so lead with
   the peer whose window should host it — usually the one they are looking at.
4. Add `--channel <ch>` when the peers are not in a channel this session joined.
5. Report the one-line result. If it fails partway it says how many steps applied:
   the window is half-arranged, and re-running the same command finishes it.

Reversible: `cbus scatter` gives every peer its own window back, so an arrangement
the user dislikes costs one command to undo. Prefer just running it over asking.

Worked example — "convert this to a split view, one column with orchestrator, the
other column with two vertical splits, coder on top, reviewer down":

```
cbus arrange 'orchestrator | (coder / reviewer)'
```

Do nothing else.
