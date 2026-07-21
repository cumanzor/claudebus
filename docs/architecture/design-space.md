# claudebus — store & transport design space

Why this file exists: the store/transport choices (append-only files, a polling
follower, mark-and-fetch truncation handling) get re-litigated every time a
reviewer meets the codebase, because the obvious modern alternatives — a local
websocket broker, RPC/IPC primitives, SQLite, Redis — all *look* stronger on
paper. This doc records the analysis once, from a design-review thread run at
v0.7.0 (2026-07-20/21), so the next reviewer starts from the conclusions instead
of re-deriving them. The mechanisms themselves are specified elsewhere and are
only summarized here:

- [behavior-spec.md](behavior-spec.md) §8.6 (cursor/resume) and §8.7
  (displacement gate, `--steal`, dormancy)
- [protocol.md](protocol.md) §4 (frame grammar, Monitor constraints) and §13
  (constants & invariants)
- [../prior-art-and-cc-internals.md](../prior-art-and-cc-internals.md) §3
  (harness constraints) and §4 (decision log, incl. the relay's Maildir spool
  and the "no Maildir in the local bus" ruling)
- [overview.md](overview.md) §5 (founding decisions)

## 1. Three constraints pin the design

Every serious alternative fails exactly one of these; append-only files plus a
polling follower is close to the unique solution satisfying all three.

1. **Durable store-and-forward, by construction.** Sender and recipient must
   never need to coexist in time: send to a peer that has not armed yet, that is
   mid-compaction, or that is dead and rejoins tomorrow, and the message waits.
   The queue must survive process death and reboot without any component being
   configured correctly — the append *is* the persistence.
2. **No daemon.** The binary is the whole system: no broker lifecycle, no
   upgrade coordination between components, no single failure domain covering
   every channel. `cbus selfupdate` updates everything that exists.
3. **The Monitor boundary.** A session consumes events only as stdout lines
   from a process it spawns, or as ws frames the Monitor dials out to. Nothing
   else reaches a conversation. Any transport must terminate in one of those
   two shapes.

| Alternative | Breaks constraint | How |
|---|---|---|
| Local ws broker | 2 | someone must own the socket |
| Peer-to-peer ws | 1 | delivery requires both ends alive; ws has no p2p mode anyway (§5.2) |
| RPC (gRPC, XPC-style calls) | 1 | call semantics = schema + deadline; the bus is freeform text + no deadline |
| Unix sockets / FIFOs / POSIX mq / shm | 1 | kernel buffers, destructive reads, no reboot survival (§5.3) |
| SQLite | 3 (economically) | passes 1 and 2, but has no cross-process change notification — the tail, the bus's hot loop, gets nothing for free (§5.4) |
| Redis / in-memory stores | 2, and 1 by configuration | daemon returns; durability becomes redis.conf being right forever (§5.5) |

## 2. What the poll actually costs

The follower (`internal/client/follow.go`) does **not** re-open the inbox per
check. It opens once, holds the fd, and loops: one `read()` at EOF, one
`os.Stat` for the rotation check (`rotated()` — dev+ino change or size
regression), sleep `followPoll` (200 ms). Every `identityEvery` (5th) idle tick
it re-reads meta.json to confirm it is still the recorded listener. Re-opens
happen only on rotation or a vanished inbox (`reopenUntilSuccess()`).

Idle ledger per armed peer — derived from the loop shape, not measured (the
same measured-vs-derived honesty the harness constants get): ~5 reads + ~5
stats + ~1 meta read + 5 sleep wakeups per second, call it ~20 syscalls/second
against files that are a few KB and permanently hot in the page cache — on the
order of tens of microseconds of CPU per second, well under 0.01 % of one
core. Cursor sidecar writes happen only when a
frame boundary actually advanced, not per tick. Cost is linear in armed
followers; hundreds of simultaneous peers would be needed before it shows up in
a process monitor.

The real trade is latency granularity, and it is priced against the workload:
delivery latency is ≤ one poll interval (200 ms), while the recipient's
processing is an LLM turn — seconds to minutes. The transport contributes well
under 1 % of end-to-end latency and none of its variance.

**Why not kqueue/fsnotify:** event-driven watching would buy near-zero idle
wakeups and sub-millisecond latency nobody can perceive, and would cost the
failure class polling cannot have — missed or dropped watch events. kqueue
watches fds, not paths, so the rejoin `rm`+recreate flow needs watch
re-establishment logic; inotify differs on Linux; both are exactly the kind of
live stateful mechanism that develops "follower silently dead" modes. The poll
loop cannot miss an event and cannot leak a watch; its worst case is a 200 ms
delay. If latency ever matters, the lever is the `followPoll` constant, not a
rearchitecture.

## 3. The takeover cadence (`--steal`)

A steal rewrites only meta.json — no rotation, no fd invalidation, nothing the
displaced follower can passively observe. So the identity re-check *is* the
discovery mechanism, and its cadence (`identityEvery` × `followPoll` ≈ 1 s) is
the takeover latency by construction. Inside that window both followers are
live: a message arriving is emitted by both (a duplicate — the outcome the
design trades for everywhere, never a loss), while cursor writes are
identity-conditional so the loser cannot drag the stealer's resume point.

Two rulings worth restating because they look like bugs until the rationale is
loaded:

- **Opt-in, not last-wins.** The relay displaces remote tails automatically
  because a single server referees both connections. Local arming has no
  referee — just two processes racing over files — so takeover requires the
  human to type `--steal`; a race can never displace a working listener by
  accident.
- **No lock (R-B).** The displacement gate is deliberately non-atomic: two arms
  can both pass it. The race self-corrects (the loser's identity check finds it
  is not the recorded listener and goes dormant within one interval). A lock
  would buy atomicity at the price of a wedged-alias recovery path — a crashed
  lock-holder bricking the address — which is the worse failure.

Mechanism, dormancy causes, and the cursor's role: behavior-spec §8.6–8.7.

## 4. The harness boundary: who truncates, and why cbus doesn't fight it

cbus never truncates. `LocalSend()` (`internal/client/send.go`) **rejects** a
message whose marshaled line exceeds `core.MaxMessageBytes` (1 MiB) rather than
cutting it — torn or partial data is the corruption the single-write invariant
exists to prevent (`appendInbox()` in `internal/client/presence.go`:
`O_APPEND|O_CREATE|O_WRONLY`, one write). The relay adopts the same constant via
`http.MaxBytesReader` in `handleSend()`, so the two paths cannot drift.

Truncation happens downstream, in Claude Code's Monitor pipeline, which defends
the conversation context from its tenants with two measured caps (`MonitorLineCap`
= 500 chars/line, notification ceiling ≈ 3000 chars; see `internal/core/frame.go`
and protocol.md §4.1 — measurements of the harness, not negotiated values). The
two caps are different in kind:

- The **per-line cap is the cooperative limit**: fully dodgeable by any producer
  willing to wrap, which is what `BodyWrap` (440 bytes, rune-safe) is. Disciplined
  producers neutralize it entirely; it clamps exactly the producers that were
  going to be a problem (raw `tail -f` spew).
- The **per-notification cap is hard**: it applies to the batched block's total,
  so no amount of wrapping helps. `Reframe()` — the relay framer — handles it by
  *warning instead of fragmenting*: the ` ⚠truncated~<N>B` marker rides the
  header, which is delivered first and survives the cut. `LocalEmit()` adds no
  oversize warning; the marker is relay-only by design.

Mark-and-fetch beat fragmentation because: (a) one message = one notification is
the framing contract, and fragments would interleave with other peers' events
and push stateful reassembly onto every recipient; (b) the durable copy already
sits next to the recipient — recovery is one read of its own inbox, and the
byte count in the marker lets it judge whether recovery is even worth it;
(c) chunking would be tuned to measured harness behavior (the ~3000 ceiling,
the 200 ms batch window) that can shift under any harness update, while a
header warning degrades gracefully. Detection is triple-redundant on the relay
path and double-redundant on the local one: the ⚠ marker (relay-only), the
harness's own `...(truncated)` marker (itself a measured 2026-07-13 harness
delta that an update can revoke), and the missing `◀ cbus end` trailer;
what remains genuinely charged to the recipient is the recovery step — accepted
because it lands only on the rare oversize path, and a >2800-byte bus message
is a workload smell anyway (send a path, not a document).

The resulting size funnel:

| Stage | Ceiling | Enforced by |
|---|---|---|
| Protocol (stored line) | 1 MiB, reject-not-truncate | `core.MaxMessageBytes`, both paths |
| CLI send (body rides argv) | ~128 KiB on Linux (`MAX_ARG_STRLEN`); ~1 MiB total argv+env on macOS | kernel exec limits |
| Follower stdout | unlimited — full framed block, however many wrapped lines | — |
| What the session sees inline | header + ~2.9 KB of body | harness notification ceiling |

The bus *carries* ~350× more than the harness *displays*; the gap between those
numbers is exactly the mark-and-fetch regime.

## 5. Alternatives considered and rejected

### 5.1 Local ws broker

Buys: sub-millisecond delivery (imperceptible, §2) and deletion of the follower
— the Monitor has a native ws source, so the arm gets simpler. Costs: the
daemon (constraint 2) and durability demoted from construction to
implementation — ws frames are ephemeral, so offline delivery needs a disk
spool, which is exactly what the relay had to build
(`relay/internal/spool/spool.go`, Maildir tmp→new). The relay is the honest
price list for a broker: spool semantics, bearer tokens, a deploy script, and
rough edges like no leave endpoint (abandoned mail queues forever). ws is used
remotely because no shared filesystem exists there — forced, not preferred.

### 5.2 Peer-to-peer ws

Does not exist at the protocol level: a WebSocket is an upgraded HTTP
connection, one end a listening server, the other a dialing client. "p2p ws"
means every peer runs a server, and then: discovery needs a port registry (which
reinvents meta.json — the filesystem is already the rendezvous); the Monitor's
ws source is client-only, so a session cannot listen without a helper process
(the follower again, now holding a port); and direct sockets couple peers
temporally, killing constraint 1 — delivery to an absent peer fails, so an
offline queue appears, on disk, next to the peer, which is `inbox.jsonl`.
Topology cost is O(N²) socket pairs churning with every session start/end vs
O(N) churn-free files. (WebRTC data channels, the genuinely p2p web transport,
need a signaling server plus STUN — solving NAT problems that don't exist on
one machine.)

### 5.3 RPC and the classic IPC family

- **Unix domain sockets** — ws-local minus HTTP; still rendezvous-synchronous.
- **FIFOs** — the inbox's closest cousin, and a synchronous handoff in a file
  costume: kernel buffer (16–64 KiB depending on platform, gone on reboot),
  writer blocks or takes
  EPIPE without a reader, destructive reads, no replay. `inbox.jsonl` is
  precisely "a FIFO with an infinite, durable, replayable buffer."
- **POSIX message queues** — destructive reads, tiny defaults, and `mq_*` is
  not implemented on macOS at all.
- **Shared memory** — hand-built ring buffer plus semaphores, zero durability,
  zero inspectability: the maximal version of everything cbus refuses to own.
- **D-Bus / XPC** — each is one platform's blessed broker; either splits the
  codebase down the OS line *and* reintroduces the daemon.
- **RPC as a shape** (gRPC, JSON-RPC) — fails differently: "invoke a method on
  a live endpoint and wait" is the strongest temporal coupling in the list and
  requires an operation schema. A cbus message is freeform text handed to a
  model that decides what to do; the "API" of a session is natural language.
  RPC imposes a schema and a deadline on a system whose pillars are no schema
  and no deadline.

cbus is itself an IPC system built from the two most boring primitives in
Unix: an append-only file for the durable hop and an anonymous pipe (follower
stdout → Monitor) for the live hop. The file is the only primitive in the list
with durability, reboot survival, universal tooling, and no liveness
requirement on either end. The precedent is local mail delivery — Maildir/mbox
append-to-a-file-per-recipient, sender and reader decoupled in time, no daemon
required to store, only to notify. `inbox.jsonl` occupies the same design
point deliberately (see the prior-art decision log for the related ruling:
Maildir rigor server-side in the relay spool, atomic-`mkdir` simplicity
locally).

If request/reply is ever needed, it is a *convention on top of the bus*
(correlation IDs in message text, the AMQP RPC pattern) — a protocol pattern
over `inbox.jsonl`, not a transport swap.

### 5.4 SQLite

The first alternative that is a true peer: embedded, daemonless, durable,
crash-safe — SQLite's own docs market it as the replacement for `fopen()` and
for piles-of-files formats. It loses on three specifics:

1. **The hot loop is the one thing it lacks.** SQLite has no cross-process
   change notification (`sqlite3_update_hook` fires only in the writing
   process). A SQLite follower polls `SELECT … WHERE rowid > ?` on the same
   200 ms interval — identical latency, more machinery per tick — while the
   file version gets its cursor for free: the fd offset *is* the resume point,
   dev+ino *is* the epoch check. Porting means re-earning the M4 invariants
   (lossless steal, mid-frame safety, epoch detection) as rows, with nothing
   user-visible improving.
2. **One database is one lock domain.** SQLite serializes writers per db file:
   every peer's send contends on one lock with `SQLITE_BUSY` retry logic,
   vs fully independent per-inbox appends and per-peer failure domains today.
   Deeper: the R-B ruling is a considered rejection of locks (bounded
   self-correcting races preferred over wedged-lock recovery paths); SQLite's
   transactionality is exactly the lock the design ruled against.
3. **The glass-box store is load-bearing.** Every audit worked by
   `ls`/`cat`/`jq` over the store; truncation recovery is `tail -1` + `jq`;
   field-smoke doctrine is "look at the real store." A schema between you and
   the bytes taxes all of it.

SQLite would win if the bus became a *log* — queryable history, retention
policies, cross-channel search, real volume. Today it is a *mailbox*: tiny
messages, consumed once, truncated at epoch boundaries. (Also: WAL mode needs
real shared memory and POSIX locks — a SQLite store must never live on the
SMB/sshfs mounts present on these machines; files degrade more gracefully.)

### 5.5 Redis and in-memory stores

The mirror image of SQLite: natively solves notification, fails on residency.
Redis Streams is uncannily close to cbus-in-a-box — `XADD` the append,
`XREAD BLOCK` a true cross-process push tail, last-delivered-id the cursor,
consumer groups the listener registry, `XAUTOCLAIM` literally `--steal`. A
meaningful slice of `internal/client` is a hand-rolled, file-backed subset of
Redis Streams; had a daemon been acceptable, Streams would have been the
serious contender.

The two dealbreakers: the daemon returns (constraint 2, with the full broker
bill from §5.1 plus a second component with its own version and uptime), and
durability inverts from construction to configuration — RDB loses minutes, AOF
`everysec` loses ~a second, `always` taxes every write; `maxmemory` eviction
policies can silently discard mail *by design*, and disabling eviction trades
that for OOM. "A message to a dead peer survives reboot and waits forever"
becomes a redis.conf stanza that must be correct forever, failing silently
until a power blink tests it. The follower process survives the migration
anyway (constraint 3: something must still bridge to stdout), so the
complexity does not even buy the deletion it promises.

Rest of the family, briefly: memcached has neither persistence nor
notification; NATS JetStream and Kafka are real durable buses that are *more*
daemon, not less.

## 6. What would reopen the question

- **Machine-to-machine traffic with no LLM in the loop** — the 200 ms poll and
  the whole "latency is invisible" argument (§2) are priced against LLM
  turnaround; remove that and the ledger changes.
- **Hundreds of simultaneously armed peers** — poll cost is linear; the first
  lever is the two constants (`followPoll`, `identityEvery`), the second is a
  shared-watcher process, and only then a broker.
- **Sub-100 ms fan-out requirements** — same order of levers.
- **History as an asset** — if messages need to be searched, retained, or
  aggregated, that is a log, not a mailbox; add a SQLite *index* (or mirror)
  beside the store rather than replacing the store.
- The relay stands as the measured cost of a broker if one is ever genuinely
  forced: local design + spool + tokens + deploy surface.
