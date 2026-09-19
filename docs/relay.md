# Networked relay (server)

`relay/` holds `cbus-relay`, a std-lib-only Go daemon that extends the bus across
machines (shipped; the client speaks to it via `<channel>@<host>` addresses —
see below):

> **Cross-machine messaging requires a relay.** The local file bus (see *How it
> works*) never leaves one machine; anything crossing a machine boundary goes
> through a `cbus-relay` daemon, which is the shared rendezvous point. **One relay
> serves every participating machine** — you don't run one per host. Adding a
> machine to the mesh means pointing it at the *existing* relay, not standing up a
> new one: set `CBUS_SITE_<HOST>_URL` to the relay's base and `cbus auth set
> <host>` with its bearer (plus CF Access service-token if it's behind a tunnel),
> then address `<channel>@<host>/<alias>`. A machine only needs its *own* relay if
> you want other machines to address channels *hosted on it* (`@that-host`) —
> uncommon. The relay host itself reaches its channels over loopback and needs no
> `CBUS_SITE_*` override; every other client does.

## Native Claude and Codex subscriptions

An ordinary Claude or Codex CLI session connects through its local daemon.
Native Claude requires a build with the Claude adapter; v0.12.2 supports native
Codex only. Configure `CBUS_SITE_<HOST>_URL` and `cbus auth` as described below,
then run inside each target session:

```sh
cbus connect dev@server laptop --json
cbus list dev@server                              # one roster check after joining
cbus connection status dev@server/laptop --json       # on-demand receipt inspection
cbus connection disconnect dev@server/laptop         # stop delivery, retain local history
```

No Monitor, tail or periodic model polling is needed. This requires the relay's
`/tail/durable-v1` endpoint; an older server is refused before consuming messages.

The durable stream sends a stable message ID with the raw bus message. The
daemon acknowledges only after an atomic, synchronized local inbox append;
the relay moves the message to delivered storage after that acknowledgment.
Reconnection deduplicates those IDs. Relay acknowledgment is not recipient
receipt. Claude socket writes remain pending until an exact transcript receipt;
Codex queue acceptance is also distinct from recipient history. Inspect with
`cbus connection status CHANNEL@HOST/ALIAS --json` and `connection reconcile`.

A mailbox has a consumer identity: the same consumer may reconnect, but a
different active consumer is refused. Presence uses the actual CLI consumer's
join/exit/resume transitions; losing and restoring the WebSocket does not create
false consumer leave/join events. Presence delivery is journaled with recipient
ownership checks, while ordinary `/tail` behavior remains compatible with
existing Monitor clients. Compaction notices remain local-only in v1.

Do not add a Monitor to a managed inbox or silently fall back if native checks
fail. For existing legacy memberships, follow the deliberate [migration
steps](claude.md#migrating-an-existing-monitor-peer); `leave` deletes a legacy
inbox and is not native disconnect.

## Legacy Monitor relay interface

The `/tail` protocol and join/arm examples below apply only to legacy peers.
Native peers use the durable subscription above.

- **`POST /send`** (bearer token) appends `{from,to,ts,text}` — the exact local
  inbox shape — to a Maildir spool (`spool/<channel>/<alias>/{tmp,new,cur}`).
- **`GET /tail?channel=&alias=`** upgrades to WebSocket, authed via
  `Sec-WebSocket-Protocol: bearer.cbus.<token>` (k8s-apiserver pattern — the
  Claude Code Monitor `ws:` source can't send headers). Replays queued messages,
  then streams; delivered messages move `new/` → `cur/` (at-least-once).
- **`GET /peers`** (bearer) — presence/queue depth; liveness = relay presence +
  30s/90s ping heartbeat, not pids. **`/healthz`** — unauthenticated.
- Runs as systemd unit `cbus-relay` on the server, loopback `127.0.0.1:8090`,
  fronted by the CF tunnel. Deploy with `relay/deploy.sh` (builds on the server).
- One active tail per peer: a new `/tail` displaces the old (per-message
  displacement checks; delivery is at-least-once — a narrow handover race can deliver
  one in-flight message to both tails).

### Configuring credentials and using legacy remote tails

The client speaks to the relay through the `<channel>@<host>/<alias>` address
form. Each `<host>` resolves from its `CBUS_SITE_<HOST>_URL` env var — there are
no built-in hosts (the examples below use `server`):

```sh
# seed the macOS Keychain — ONE credential per invocation (each '-' reads ALL of stdin,
# so the three can't share one line); values piped from a password manager:
<secret-manager> read <relay-bearer-item>  | cbus auth set server --token -
<secret-manager> read <cf-client-id-item>  | cbus auth set server --cf-id -
<secret-manager> read <cf-secret-item>     | cbus auth set server --cf-secret -
cbus send dev@server/server "build finished"                # POST /send — queues if the peer is offline
cbus tail dev@server/laptop                                 # prints the Monitor ws arm spec + claims 'laptop' as your identity
cbus list @server                                        # peers the relay knows: connected / queued / lastSeen
cbus leave dev@server                                    # drop THIS session's identity marker
```

Details that matter:

- **Aliases are explicit** — pick a short hostname/role (`laptop`, `server`, `ci`).
  There's no remote registry; a taken alias is self-evident because the relay
  keeps one active tail per peer (your Monitor visibly drops if displaced).
- **Endpoint autodetects**: a session on the relay host probes
  `127.0.0.1:8090/healthz` and talks loopback with no CF Access; everyone else
  goes through the host's `CBUS_SITE_<HOST>_URL` (e.g. `https://bus.example.com`)
  with CF Access service-token headers.
- **Credentials are never in code**: `cbus auth` stores them in the macOS
  Keychain (`security(1)`) or, on Linux, 0600 files under `~/.config/cbus/`.
- **Monitor receive**: remote `tail` prints the `Monitor {ws:}` arm
  spec (URL + `bearer.cbus.<token>` subprotocol) rather than exec'ing a
  process — the session arms it, and messages arrive as turn events exactly
  like local ones.
- Arming a remote tail records a **session-scoped identity marker**
  (`.remote/<host>/<channel>/<sessionId>` = `{alias, ownerPid, ts}`) so *this
  session's* later sends on that channel auto-fill a routable `from`. Sessions
  never inherit each other's aliases (no cross-session impersonation); a
  session without its own marker falls back to `hostname-PID` (unroutable —
  same caveat as local unjoined senders). Markers carry the owning `claude`
  pid, so `cbus prune` sweeps them when their session dies. A marker is a
  from-default, **not** proof of reachability — `cbus list <ch>@<host>` is the
  truth source for who is actually connected.
- **Relay presence** — the relay pushes `join`/`departed` presence to connected
  peers on a channel (server-side, cbus-ijx.5), so a session tailing a relay
  channel is notified when a peer arms or drops, like on a local channel. It is
  connection-lifecycle, not registration: `join` fires on ws attach, `departed`
  ~90s after a tail drops (a grace window that debounces sleep/wake re-arms).
  `/peers` stays the state truth source; the pushed events are edge notifications.
  Delivery is connected-only — offline roster catch-up stays `cbus list`.
- **Relay peers are append-only** — the spool creates a peer's maildir on its
  first queued message and never GCs it, so an off peer lingers in
  `cbus list <ch>@<host>` forever (`off`, `queued 0`). The relay holds no pid to
  test liveness on, so local `cbus prune` can't reach it. `cbus prune <ch>@<host>`
  (or bare `@<host>`) reaps those from the server side: it drops every peer that
  has no live tail **and** no queued mail — a peer with pending mail is always
  kept, so nothing undelivered is lost.
