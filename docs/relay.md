# Networked relay (NUC)

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

- **`POST /send`** (bearer token) appends `{from,to,ts,text}` — the exact local
  inbox shape — to a Maildir spool (`spool/<channel>/<alias>/{tmp,new,cur}`).
- **`GET /tail?channel=&alias=`** upgrades to WebSocket, authed via
  `Sec-WebSocket-Protocol: bearer.cbus.<token>` (k8s-apiserver pattern — the
  Claude Code Monitor `ws:` source can't send headers). Replays queued messages,
  then streams; delivered messages move `new/` → `cur/` (at-least-once).
- **`GET /peers`** (bearer) — presence/queue depth; liveness = relay presence +
  30s/90s ping heartbeat, not pids. **`/healthz`** — unauthenticated.
- Runs as systemd unit `cbus-relay` on the NUC, loopback `127.0.0.1:8090`,
  fronted by the CF tunnel. Deploy with `relay/deploy.sh` (builds on the NUC).
- One active tail per peer: a new `/tail` displaces the old (per-message
  displacement checks; delivery is at-least-once — a narrow handover race can deliver
  one in-flight message to both tails).

## Using remote channels from cbus

The client speaks to the relay through the `<channel>@<host>/<alias>` address
form. Each `<host>` resolves from its `CBUS_SITE_<HOST>_URL` env var — there are
no built-in hosts (the examples below use `nuc`):

```sh
# seed the macOS Keychain — ONE credential per invocation (each '-' reads ALL of stdin,
# so the three can't share one line); values piped from 1Password:
op read 'op://…/relay-bearer'  | cbus auth set nuc --token -
op read 'op://…/cf-client-id'  | cbus auth set nuc --cf-id -
op read 'op://…/cf-secret'     | cbus auth set nuc --cf-secret -
cbus send dev@nuc/nuc "build finished"                # POST /send — queues if the peer is offline
cbus tail dev@nuc/mbp                                 # prints the Monitor ws arm spec + claims 'mbp' as your identity
cbus list @nuc                                        # peers the relay knows: connected / queued / lastSeen
cbus leave dev@nuc                                    # drop THIS session's identity marker
```

Details that matter:

- **Aliases are explicit** — pick a short hostname/role (`mbp`, `nuc`, `ci`).
  There's no remote registry; a taken alias is self-evident because the relay
  keeps one active tail per peer (your Monitor visibly drops if displaced).
- **Endpoint autodetects**: a session on the relay host probes
  `127.0.0.1:8090/healthz` and talks loopback with no CF Access; everyone else
  goes through the host's `CBUS_SITE_<HOST>_URL` (e.g. `https://bus.example.com`)
  with CF Access service-token headers.
- **Credentials are never in code**: `cbus auth` stores them in the macOS
  Keychain (`security(1)`) or, on Linux, 0600 files under `~/.config/cbus/`.
- **Receive is Monitor-native**: remote `tail` prints the `Monitor {ws:}` arm
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
