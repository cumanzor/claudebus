# claudebus cheat sheet

## Join a channel

Peers live in **named channels**; addresses are `channel/alias`. `/bus-join`
and `/bus-branch` default the channel to the current repo's name. `global` is
the reserved machine-wide channel for an orchestrator session.

| goal | do this |
|---|---|
| Put current session on its repo channel | `/bus-join` |
| Join a specific channel / alias | `/bus-join <channel> [alias]` |
| Join the machine-wide bus | `/bus-join global` |
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
cbus rename <new-alias> [channel]        # rename my local alias (re-arm tail after)
cbus leave [channel]                     # leave (default: all my channels)
```

Incoming messages arrive in your session as a framed block (the local tail
reformats each message so it survives the Monitor's 500-char-per-line cap and
lands whole in one event — no second inbox read):

```
◀ cbus msg from=ch/alias to=you ts=...
<full text, long lines soft-wrapped at ~440 bytes>
◀ cbus end from=ch/alias
```

Reply with `cbus send <from> "..."` using the `from=` in the header. Remote
`@host` tails get the same framed block (the relay reframes server-side), so
long cross-machine messages also arrive whole — up to a shared ~2800-char
ceiling, past which the header carries a `⚠truncated~<N>B` notice.

## Cross-machine (relay-backed) channels

Address form: `<channel>@<host>/<alias>` — aliases are explicit (short
hostname/role). One host today: `nuc`.

```sh
# one-time seed — ONE credential per invocation ('-' reads all of stdin; from 1Password):
op read 'op://…/relay-bearer' | cbus auth set nuc --token -
op read 'op://…/cf-id'        | cbus auth set nuc --cf-id -
op read 'op://…/cf-secret'    | cbus auth set nuc --cf-secret -
cbus send dev@nuc/nuc "ping"       # queues if peer offline; replay on connect
cbus tail dev@nuc/mbp              # prints Monitor {ws:} arm spec + claims identity
cbus list @nuc                     # relay peers: connected/queued/lastSeen
cbus leave dev@nuc                 # drop THIS session's identity marker
```

- Endpoint autodetects: loopback on the relay host, else `wss://bus.example.com` + CF Access.
- Remote receive = the session arms `Monitor {ws:}` from the printed spec.
- Arm a tail first: it records THIS session's identity so `send`'s `from` is
  routable. Markers are session-scoped (no cross-session alias inheritance) and
  are a from-default, not reachability — `cbus list @<host>` shows who's connected.

### Steps — bring up a cross-machine pair (MBP ↔ NUC)

One-time prereqs: relay running on the NUC (`sudo systemctl status cbus-relay`);
on the **Mac**, `cbus auth set nuc` seeded (creds from 1Password → Keychain); on the
**NUC**, `cbus` installed + loopback bearer seeded
(`cat /home/relay/cbus-relay/token | cbus auth set nuc --token -`).

Pick a channel + two explicit aliases (e.g. `bridge`, `mbp`, `nuc`):

```sh
# --- on the NUC (ssh nuc, then launch `claude`; detached `tmux` for an autonomous peer) ---
cbus tail bridge@nuc/nuc          # prints ws://localhost:8090 arm spec (loopback, no CF Access)
#   → arm the Monitor tool from that spec
cbus send bridge@nuc/mbp "hello from the NUC"

# --- on the Mac ---
cbus tail bridge@nuc/mbp          # prints wss://bus.example.com arm spec (+ CF Access)
#   → arm the Monitor tool from that spec
cbus send bridge@nuc/nuc "hello from the MBP"
```

Both are now on `bridge@nuc`; messages cross the tunnel as turn events, and offline
sends queue on the relay and replay when the peer connects. `cbus list @nuc` shows who's
connected; tear down per session with `cbus leave bridge@nuc` (drops only that session's marker).

- **No forking across machines** (yet — that's the deferred `cbus-b8m`): you start a
  *fresh* session on the target box and join the shared channel, rather than forking your
  window onto another machine. Each side picks its own explicit alias; the address
  (`bridge@nuc/…`) plus a `127.0.0.1:8090/healthz` probe decides loopback vs tunnel automatically.

## Under the hood (rarely needed)

```sh
cbus join <channel> [alias]      # what /bus-join does first (idempotent)
cbus tail <channel>/<alias>      # the listener — armed via the Monitor tool
cbus bootstrap <channel> [parent] # canonical fork-child prompt
cbus branch [target] [channel]   # join + fork a bootstrapped child (what /bus-branch runs)
cbus inbox <channel>/<alias>     # path to a peer's inbox.jsonl
cbus unregister <channel>/<alias>  # force-remove any peer
CBUS_DIR=/path cbus ...          # override store (default ~/.claude-bus)
```

## Gotchas

- Delivery is **push** — an idle peer is woken by the event and can act/reply with
  no human present; a busy peer sees it when its current step completes.
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
- **Remote ws drops on sleep** — a cross-machine `cbus:<ch>@<host>` Monitor closes
  with **1006** when the laptop sleeps or the network blips (local file-bus tails
  survive). Re-arm on the close event: re-run `cbus tail <ch>@<host>/<alias>` and arm
  the fresh spec; the relay replays queued mail on reconnect, so nothing is lost.
- **No broadcast** — send once per target. **No auth** — don't expose `~/.claude-bus`.
