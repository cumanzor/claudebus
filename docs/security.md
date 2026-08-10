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
  LAN/tailnet, or (b) through an **authenticated Cloudflare tunnel with service-token keys** —
  which is how this deployment runs it: the daemon binds `127.0.0.1` only, fronted by CF Access.
  `POST /send` sits behind a CF Access **service token** *and* the relay's own bearer (a request
  must clear both edge and origin); `GET /tail` uses a CF Access **bypass** scoped to that path
  only (the Monitor `ws:` client can't send Access headers), with auth carried in
  `Sec-WebSocket-Protocol: bearer.cbus.<token>`. All keys live in the macOS Keychain / `0600`
  files via `cbus auth` — never in code, argv, or the repo. Do **not** expose `:8090` directly:
  without the tunnel + Access in front, anyone reaching it with the bearer can read/inject on
  any channel.
- **Identity is a convenience, not a credential.** `from` is spoofable (local and remote). The
  session-scoped remote marker prevents *accidental* cross-session impersonation, but it is not
  auth. `cbus list <ch>@<host>` reports who's actually connected; a marker is only a from-default.
- **What it deliberately does not do:** no encryption at rest beyond filesystem permissions, no
  multi-user isolation, no message signing, no broadcast. It's a coordination bus for one
  operator's machines, not a shared messaging service.
