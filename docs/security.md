# Security & network model

claudebus deliberately aims to be a **trust boundary, not a security boundary**, and the
design is honest about that line.

- **Local bus — trust boundary, not a sandbox.** Everything under `~/.claude-bus` is
  readable/writable by anything running as your user; any such process can append to any
  inbox and set `from` to whatever it likes. There is *no* sender authentication, by design.
  The guardrail lives on the receiving side: Claude Code treats an incoming bus message as an
  untrusted peer request that can't escalate permissions (and delivery can wake an idle
  session — see the [caveats](how-it-works.md#caveats)). Safe on your own machine; unsafe beyond it. Don't put `~/.claude-bus`
  on a shared or networked filesystem.
- **Cross-machine relay — keep it off the open internet.** The relay is a single-operator
  service with **no multi-tenant auth**. It must only be reachable either (a) on a trusted
  LAN or private network, or (b) through an authenticated front door in front of loopback. See
  [Deploying a relay](#deploying-a-relay) below for the exact per-path requirement and the
  current risk if the token leaks. All keys live in the macOS Keychain / `0600`
  files via `cbus auth` — never in code, argv, or the repo. Do **not** expose `:8090`
  directly: without a front door in place, anyone reaching it with the bearer can
  read/inject on any channel.
- **Identity is a convenience, not a credential.** `from` is spoofable (local and remote). The
  session-scoped remote marker prevents *accidental* cross-session impersonation, but it is not
  auth. `cbus list <ch>@<host>` reports who's actually connected; a marker is only a from-default.
- The local daemon holds each connected Claude session's messaging token under
  `$CBUS_DIR/.daemon/claude-credentials/`, one file per binding, mode `0600`.
  Protection is user file permissions only, the same trust boundary as the bus
  directory above: anything running as your user can read those tokens and
  inject input straight into a Claude session, not only into a bus inbox.
- **What it deliberately does not do:** no encryption at rest beyond filesystem permissions, no
  multi-user isolation, no message signing, no broadcast. It's a coordination bus for one
  operator's machines, not a shared messaging service.

## Deploying a relay

The relay listens on `127.0.0.1` only. Anything that puts it on a network (a tunnel, a
reverse proxy, a LAN) must authenticate every request before it reaches the relay; the
relay's own bearer is a second, independent layer on top, never a substitute for that
front door. Per path:

- `POST /send`, `GET /peers`, `POST /prune` need the front door's own auth *and* the
  relay's `Authorization: Bearer <token>`.
- `GET /tail` and `GET /tail/durable-v1`, the two WebSocket paths, each need an
  exact-path bypass at the front door instead, because a WebSocket handshake cannot
  carry the front door's own headers. On those two paths, the
  `Sec-WebSocket-Protocol: bearer.cbus.<token>` subprotocol is the only authentication.
- `GET /healthz` is unauthenticated at the relay itself and returns only `ok`. Put it
  behind the front door too, or accept that it discloses liveness and nothing else.

**Current risk.** The bearer is one shared, relay-wide secret: a leaked token lets
anyone read any channel's mail through the paths above, acknowledge durable messages
on a channel's behalf, and publish presence text that reaches every connected session.
It also lets a caller open a legacy `/tail` for a channel/alias that already has one
attached: the newer attach displaces the older one outright, last writer wins, so a
leaked token can take over live delivery for a peer, not only read its queued mail.
The narrower `409 Conflict` protection only fires when the key is held by a *durable*
consumer with a different identity; two ordinary legacy attaches on the same key never
trigger it. Keep the token strong, store it only where the relay and its clients need
it, and rotate it if you suspect exposure.
