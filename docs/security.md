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
  LAN or private network, or (b) through an **authenticated tunnel with service-token keys**, binding
  the relay to `127.0.0.1` and fronting it with an edge access-control layer.
  `POST /send` sits behind a CF Access **service token** *and* the relay's own bearer (a request
  must clear both edge and origin); `GET /tail` and the daemon's native `GET /tail/durable-v1`
  each require a CF Access **bypass** scoped to that path only (neither client can send Access
  headers), with auth carried in `Sec-WebSocket-Protocol: bearer.cbus.<token>` as their only
  authentication. All keys live in the macOS Keychain / `0600`
  files via `cbus auth` — never in code, argv, or the repo. Do **not** expose `:8090` directly:
  without the tunnel + Access in front, anyone reaching it with the bearer can read/inject on
  any channel.
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
