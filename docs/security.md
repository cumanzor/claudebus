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
  files via `cbus auth`, never in code, argv, or the repo. Do **not** expose `:8090`
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

## Operator grants

`cbus grant` lets the operator approve one action for one local peer mid-run, in a
form the peer checks itself (`cbus grants`) instead of trusting a quoted approval.
It is **friction, not a security boundary**: it stops a model from mistaking a
relayed approval for the operator's word, or from minting a grant by running the
command the ordinary way. It does not stop a same-user process that sets out to get
around it. Command details: [command reference](architecture/command-reference.md#cbus-grant-channelalias-action---once----ttl-d).

**What a grant is.** One action, for the exact session registered under a local
`<channel>/<alias>` when it is minted, either `--once` (consumed by its first
`cbus grants use`) or `--ttl` (at most 24h). The operator confirms it by typing the
peer address and then the action at the controlling terminal. The record is written
once under `$CBUS_DIR/.grants/<channel>/<alias>/` with its provenance: the minting
terminal and the process ancestry. A grant is **suspect**, and never usable, when
that ancestry contains a model harness (Claude Code, Codex CLI, OpenCode, including
one started through node, bun or deno) or could not be walked to init.

**What it catches.** Measured against the real binary:

- No controlling terminal, as in a harness's shell tool, `setsid` or `nohup`: refused, nothing written.
- A harness's shell wrapping the command in a pty (`script`, a python pty, `nohup` plus `script`): written as suspect, exit 1.

Established by tests (in-process, through the real code paths):

- A suspect mint sends no notice to the peer.
- A later session holding the same alias (a formation recreating the peer, or a reclaimed dead peer): it cannot use a grant bound to the earlier session.
- A grant quoted in a message, or a notice-shaped message from any sender (including an alias named `cbus-grant`): not a grant. `cbus send` cannot set a message kind, so only `cbus grant` writes the `kind=grant` notice.

**What it does not stop:**

- **Reparenting.** Anything whose parent chain no longer contains the harness. `tmux new -d` plus `send-keys` produced a live grant in testing (its listing shows `via zsh < tmux`); launchd, `at` and `cron` were not run but are the same class.
- **A direct write of a grant record** (measured). Every file under `$CBUS_DIR` is writable by the same user. A hand-written record with a valid session id lists as live, and it carries whatever provenance its writer invents: one claiming `minted on /dev/ttys000 via zsh < login` looks exactly like an operator's grant. Only a record with no bound session is suspect.
- **A claimed identity at use time** (measured). `cbus grants use` reads the caller's session id from the environment (`CBUS_SESSION_ID`, `CLAUDE_CODE_SESSION_ID`, `CODEX_THREAD_ID`), so a same-user process can claim to be the grantee: with the grantee's id set, `cbus grants use` consumed a hand-written grant.
- **A harness running as root or another user** (by mechanism, not measured). An ancestor the user cannot inspect is recorded as `?` and the walk continues past it.
- **Races** (by mechanism, not measured). Revoke against use, and expiry against use, are check-then-claim: a use can win in the same instant as a revoke.
- **The opencode match is loose.** Any script path containing `opencode` counts as a harness. This fails safe: a false positive reads suspect.
- **Dead targets.** The refusal to bind a dead target covers peers with a listener process. A daemon-managed peer whose session has exited still reads as registered; the grant binds that exact session, which can only use it if it resumes.
- **The `!` prefix** of a harness: not yet measured on the real binary. It is expected to be refused (no controlling terminal) or recorded suspect (harness ancestor), never live.
- **Not supported:** Windows (refused) and remote peers (refused at mint; a `kind=grant` frame arriving over a relay is rejected by the daemon on receipt).

**The boundary is doctrine.** The committed role files tell a peer to act on a
reserved action only on the operator's own word or on a grant `cbus grants` lists as
live for it, taken with `cbus grants use` first, and never to run `cbus grant` itself.
A model that writes under `.grants/`, wraps the command to escape the ancestry check,
or sets another session's id is breaking that rule. Provenance written by `cbus grant`
itself shows a harness or pty-wrapped mint as suspect, and `cbus grants --all` lists
it with its session, minting terminal and nearest ancestors. But a hand-written record
can claim any provenance, and a reparented mint reads like an ordinary terminal
multiplexer, so the listing cannot prove that a live grant came from the operator.

## Deploying a relay

The relay listens on `127.0.0.1` only. Anything that puts it on a network (a tunnel, a
reverse proxy, a LAN) must authenticate every request before it reaches the relay; the
relay's own bearer is a second, independent layer on top, never a substitute for that
front door. Per path:

- `POST /send`, `GET /peers`, `POST /prune` need the front door to accept the
  service-token header pair the client sends (protocol.md §12 has the exact
  header names), or be absent on a trusted network, *and* the relay's
  `Authorization: Bearer <token>`.
- `GET /tail` and `GET /tail/durable-v1`, the two WebSocket paths, each need an
  exact-path bypass at the front door instead, because a WebSocket handshake cannot
  carry the front door's own headers. On those two paths, the
  `Sec-WebSocket-Protocol: bearer.cbus.<token>` subprotocol is the only authentication.
- `GET /healthz` is unauthenticated at the relay itself and returns only `ok`. Put it
  behind the front door too, or accept that it discloses liveness and nothing else.

**Current risk.** The bearer is one shared, relay-wide secret: a leaked token lets
anyone read any channel's mail through the paths above, acknowledge durable messages
on a channel's behalf, and publish presence text that reaches every session connected
to that channel. It also lets a caller open a legacy `/tail` for a channel/alias that
already has one attached: the newer attach displaces the older one outright, last
writer wins, so a leaked token can take over live delivery for a peer, not only read
its queued mail. The `409 Conflict` response only fires when the key is held by a tail
of the other kind (legacy vs. durable, either direction) or by a durable consumer with
a different consumer id, and it is a guard against accidental collisions, not an auth
control: the consumer id is just the connection id the client sends in the query
string, so a token holder presenting the same id displaces that durable consumer like
any other last-writer-wins attach. Keep the token strong, store it only where the relay
and its clients need it, and rotate it if you suspect exposure.
