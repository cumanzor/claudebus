# claudebus cheat sheet

## Join a channel

Peers live in **named channels**; addresses are `channel/alias`. `/bus-listen`
and `/bus-branch` default the channel to the current repo's name. `global` is
the reserved machine-wide channel for an orchestrator session.

| goal | do this |
|---|---|
| Put current session on its repo channel | `/bus-listen` |
| Join a specific channel / alias | `/bus-listen <channel> [alias]` |
| Join the machine-wide bus | `/bus-listen global` |
| Fork + put both sides on the repo channel | `/bus-branch window` (or `tab` \| `tmux`) |
| Fork onto a named channel | `/bus-branch window <channel>` |
| N sessions | join the same channel from each — any-to-any |

Aliases are auto-picked (`main`, then `fork-N`) and **recycled** — `join` prunes
dead peers first, so numbers don't grow forever.

## Talk (ask Claude, or run directly)

```sh
cbus send fork-1 "build is green"        # bare alias = within my own channel
cbus send deploy/server "done"           # full address = any channel
cbus send global/main "task finished"    # reach the orchestrator
cbus send fork-1 --force "queued"        # send even if peer isn't listening
cbus list [channel]                      # peers + listen/off state
cbus active [channel]                    # only peers currently listening
cbus channels                            # channels with peer counts
cbus whoami                              # my channel/alias memberships
cbus prune                               # sweep dead peers everywhere
cbus leave [channel]                     # leave (default: all my channels)
```

Incoming messages arrive in your session as JSON events:
`{"from":"ch/alias","to":"ch/alias","ts",...,"text":...}` — reply with
`cbus send <from> "..."`.

## Under the hood (rarely needed)

```sh
cbus join <channel> [alias]      # what /bus-listen does first (idempotent)
cbus tail <channel>/<alias>      # the listener — armed via the Monitor tool
cbus bootstrap <channel> [parent] # canonical fork-child prompt (used by /bus-branch)
cbus inbox <channel>/<alias>     # path to a peer's inbox.jsonl
cbus unregister <channel>/<alias>  # force-remove any peer
CBUS_DIR=/path cbus ...          # override store (default ~/.claude-bus)
```

## Gotchas

- Delivery is **at turn boundaries** — a peer sees your message next time it acts.
- `send` **refuses a dead ex-listener** unless `--force` (best effort — a re-arm
  follows from the end of the inbox, so the queued line may never be delivered);
  a joined-but-not-yet-armed peer is always accepted (first arm replays the inbox;
  re-arms follow from the end, no redelivery).
- Reply targets must be `channel/alias` — a `hostname-PID` sender is unjoined and
  has no inbox to reply to.
- **Liveness** = the real `tail` pid, cross-checked against its inbox args (no
  false `listen` from a recycled pid) and the owning `claude` pid (a crash-orphaned
  tail still reads `off`). A clean exit kills the tail via the Monitor.
- A freshly-joined peer that hasn't armed its Monitor yet has a **10-min grace
  window** before prune can sweep it.
- **No broadcast** — send once per target. **No auth** — don't expose `~/.claude-bus`.
