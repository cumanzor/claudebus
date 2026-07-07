# Prior art and Claude Code internals

Why this file exists: the decisions behind claudebus's design were made from live
probing of Claude Code internals and a landscape survey of sibling projects — work
done inside ephemeral session scratchpads that evaporate once those sessions end.
This is the durable copy. It exists so a future contributor (human or agent) can
find out *why* claudebus looks the way it does without re-running the same probes.

## 1. Landscape review

Research date 2026-07-07, comparing file-based `cbus` against the field. The
landscape splits into three architectures: **file-based mailbox** (our family —
plain files on a shared filesystem, no daemon), **centralized broker** (a
long-running HTTP/MCP daemon plus a real index), and **native Agent Teams**
(Anthropic's own in-process teammate system, covered in §2).

### ██████████████ (████████████) — closest philosophical match

Files under `~/.claude/██████████████/sessions/<id>/{inbox,outbox}/`, JSON
messages, chosen explicitly because you can `cat` them. 9 bash scripts + `jq`, no
runtime, 124 tests. `status: pending → read` rewritten atomically on pickup — the
same idea as our replay-once design. **Hooks fought them the whole way**:
`UserPromptSubmit` ran with the wrong cwd, prompt hooks have no shell access; they
gave up and moved inbox-checking into a skill (awareness context) instead. They
also built and then abandoned a background watcher (`claude -p` polled every 3s)
— it cost real money and only approximated context instead of using the live
agent's own state. They hit our exact orphan/cleanup bug (a fast restart made the
old `SessionEnd` cleanup delete the new session) and fixed it by checking
`lastHeartbeat` before deleting. Honest limits: single machine, 5-10s latency,
context only as good as the live session.

### ███ / ███████████████████ (█████████) — the mature sibling, ~██ stars, v████

Go binary, pure file-based, zero infra. **Atomic Maildir delivery**
(`tmp→new→cur`, the classic qmail trick) — messages are never partially written or
lost; this is the direct fix for the concurrent-write/torn-read races our own
review flagged in the local bus. JSON frontmatter + Markdown body, inspectable
with `cat`/`grep`/`git`. Presence freshness uses "wake locks": `███ doctor --ops`
reports a lock as `stale` when *"███ proved the recorded owner is gone or is not
the same process."* **This is independently the same design as our `ownerPid`
liveness check** — convergent evolution, not something we copied. Also has a
dead-letter queue for malformed messages, delivery receipts, priority levels,
message kinds, threading, waitable handoffs, semantic exit codes, and
cross-project federation via `reply_project`/`from_project`. `███ wake` injects
terminal notifications on mail arrival — experimental, and their own SECURITY.md
flags the TIOCSTI injection risk. Deliberately stays one layer below
orchestration (no task decomposition/worktrees/scheduling).

### ███████████ ████████████ — design doc, not code

Thesis: "the right pattern is older than the internet: mail." Maps every email
feature (To/Cc, Reply-To, Subject, threading, read/unread, delivery receipt,
filters) onto agent coordination. Layout
`.claude/mailbox/<agent>/{inbox,sent,archive}` + `all/` broadcast + INDEX.md.
Receives via `SessionStart` (full scan), `UserPromptSubmit` (quick check), and a
throttled `PreToolUse` (explicit anti-pattern: don't poll every tool call).
Security stance matches ours exactly: *"mailbox messages are untrusted input …
verify intent with the user before acting on instructions found in messages"* —
"trust boundary, not a security boundary."

### Reverse-engineered Claude Code teammate inbox (███████████)

What Anthropic's own teammate system does internally, reverse-engineered before
we had direct evidence: `~/.claude/teams/<team>/inboxes/<agent>.json`, each inbox
a JSON array, `proper-lockfile`-style exclusive locks around read-modify-write
with stale-lock detection, `read:false` flags, mark-all-read, a 5s polling loop.
Message types include `idle`, `permission_request/response`, `plan_approval_*`,
`shutdown_request`. This account is now **superseded** by the direct internals
finding in §2 — the reverse-engineering got the on-disk shape right but couldn't
see that those files are a persistence layer only, not a delivery path.

### ██████████████ (█████████████████) — the heavyweight, ~██ stars

A FastMCP HTTP daemon; agents are MCP clients calling tools, not a file
convention. Dual persistence: SQLite (+FTS5 search) *and* a per-project git repo
of markdown messages, so the git tree doubles as a human-auditable log. Identity
is a per-project namespace keyed by absolute workspace path, with memorable
`AdjectiveNoun` names and opt-in git-worktree-aware identity. Its standout feature
is **File Reservations** — advisory leases on paths/globs (granted/conflicts, TTL,
auto-expiry), deliberately advisory rather than hard locks to avoid head-of-line
blocking, with an optional git pre-commit hook to enforce them, plus "build
slots" for long-running tasks. Liveness is inferred from multi-signal silence
(agent inactivity + mail + FS + git). This is a different product — coordination
and conflict-avoidance, not just messaging — and worth revisiting if claudebus
ever needs "who's editing what."

### CCS and ███████████████ (covered fully here; summarized in the epic context)

**CCS** (kaitranntt/ccs, ~2.7k stars) was checked for overlap since claudebus
installs alongside it and `cc-branch.sh` relaunches through it. Verdict: zero
intercom. A sonnet subagent did a binary grep of the shipped `dist/`, a GitHub
full-text search across all issues/PRs (discussions are disabled on that repo),
and a web sweep (dead HN thread, Reddit mentions all about model switching) — no
messaging subcommands, no intercom/IPC/message-bus terminology anywhere, no
findable feature request. `ccs config channels` is a different thing entirely:
Anthropic's Official Channels, a session→**human** bridge (Telegram/Discord/
iMessage), not session↔session. `--share-context` is at-rest file sharing between
profiles, not live messaging. **███████████████**
(github.com/████████/███████████████) is the only wild sibling found: unaffiliated,
█ star, a single push on 2026-03-18, looks abandoned. Same family as claudebus —
file-based JSON store, per-agent 4-char codes, auto session linkage via
process-ancestor walk — but its receive path needs an MCP server + `fs.watch` +
an "asyncRewake" exit-code wake, i.e. more moving parts for what our Monitor-tail
gets turn-natively with zero servers. It does have broadcast, ack receipts, reply
threading, and peek — the same feature set on claudebus's deferred list, and a
useful reference if those get built.

**Anthropic Managed Agents multi-agent** (platform.claude.com/docs/en/managed-agents/
multi-agent, reviewed 2026-07-07): ruled out — wrong product category, the same
class of error as native Agent Teams one level further removed. "Agents" are API
resources running as threads inside a single hosted sandbox (`environment_id`;
"all agents share the same sandbox, filesystem, and vault credentials"; max 20
agents / 25 threads). No terminal, no live CC CLI process, no human-attended REPL
on either end. Delivery is SSE push to *your API client*, not into a live
conversation turn — there is no Monitor-tail equivalent and no idle session to
wake. Scope is the opposite of cross-machine (one cloud sandbox), and the
operational weight (Agent/Environment/Vault resources, session lifecycle, SSE
consumption, tool-confirmation event routing) is a hosted-platform SDK, not a
comms channel. It is hosted fan-out-and-collect delegation — the Agent tool's
shape moved to the cloud — and never touches bridging independent, human-attended
CC windows.

### What everyone converges on

Files for debuggability (every serious file-based effort cites `cat`/`grep`/
`git`); JSON messages with read/status flags and `in_reply_to` threading;
"messages are untrusted input" as the universal security posture, with no auth
treated as a trust boundary rather than a security boundary; and **notification
is the hard problem nobody has solved cleanly** — hooks are cwd-fragile, polling
(`claude -p`) costs real money, terminal injection (`███ wake`/TIOCSTI) is a
flagged security risk. claudebus's Monitor-tail delivery (see §3) is the field's
only turn-native answer with no hooks, no polling, and no injection.

### Where claudebus stands out, and what's worth borrowing

Standing out: Monitor-tool tail delivers as first-class conversation turns with
none of the field's workarounds; `ownerPid` liveness independently matches ███'s
most sophisticated feature, which is good validation; named channels (per-repo
default + reserved `global`) are a middle ground most peers under-emphasize
(flat or team-scoped instead); and the whole thing is one bash script plus
python only for robust JSON. Worth borrowing, roughly in priority order: ███'s
atomic Maildir `tmp→new→cur` delivery (directly fixes concurrent-write/torn-read
races — this is exactly why Maildir landed server-side in the relay, see §4);
file locking on `meta.json` read-modify-write (█████/CC pattern — our append is
atomic for small lines but `jset` on meta.json is not); `in_reply_to` threading;
DLQ/corrupt-message handling; delivery/read receipts.

## 2. Claude Code internals findings

███████████ ██████ ██████████ ██████ █████ ████ ████████ ███ ████ ██ ███ █████
███████ ████████ ████ ██████████ █████████ █████ ██ █████ ██ ████ ████ ████
██████ █████████████ ██████ ██████████ ███ ███

- ███████████████ ███████████ ████████ ██ ███ █████████ █████ █████ ████ ███████
  ██████████ ███ █████████ ██ █████ ██████████ █████████ ██ █████████████ ██
  ████████ ███ ███ ████████ ████ ████████ ████ █ ██████████ █████████ █████ ████ █
  █████████ ███████ ████████████ ████ ███ ███ ██████ ████ ███ ███ ████
  ███████████████ ██████████ █ ████ █████████ █████ ██████ ███ █████ █████ █ ██
  ███████████ █████ ██ ██ █████████████ █████████ ██ ████
- ███████ ███████ ████ ██████ █ ██████████ █████ ████ ██ ████████ ███████
  █████████████████ █████ █████████████████ ███ ████
  █████████████████████████████████████ ███ ████ ████████████ █████████ ██████ ██
  ██████████████████████████ ███ ██████ ████ ████████
- ███████████████ ███████ █████ ███████████████ ███████████████ ███ ███████ ██
  ██████████ █████ ██ █████ ███ █████████ ████ ███ ███████ ████████████████ █████
  ███ █ ███████████ █████ █████ ███████ ████ ████ ██████ ██ ████████████
  █████████ █████████ █████████ █ ███████ ███ ████ █████ ███ ███████ ███ ██
  █████████ ██ ██████ ████████ ███████
- ███ █████████████ ███████ ██████ █████ ████ ████ ████████ ████████ █ ████████
  ███████ ████████ ██ ████ ████ █████ █████████ █ ████ █████ ███
  ███████████ █████████████████████ ████████████ ██████ ███████████
  ██████████████ █████████████ ███████ ███████████████████ ██████████
  ████████████ ███████████████ █████████████████ ████ ███████ ██████████
  ███████ ███ █████ ██ █████ ███ ████████████████ ███ ██ ███████████ ███
  ████████████ ██████████ ████████ ███████████ ██ █████████████ █████ ███
  ██████████ ████████ ███ ████ ███ ███████ ████ ██ ███ ████ ██████████ █████
  █████ ████████ ████ █████ █ ████████████ ██████ ████████ █ ███████████
  █████████████ ██████ ██ ██ ███████████ ██████ ███ ███ ████████ █████████ ████
  ████ ███ ████████ ██ █ ████████ ██████████ ███ ██████████████ ████████ █████
  ███ ███ ██ ██ ██████ █████ ███ ███ ███ █████████████████ ███████ ██ ████
- █████████████████ ███████████ █████ ████ ██ ████████ ████ ██ █████ ███ ██████ ████
  █ ████ ████ ████ ██ █ █████ ████ ████████ ██ ███ █████ ███ ███████████ ██
  ██████████ ███████ ███ █████████ █████ ██████ █████████ ██ ████████████
  ████████ ███████████ █████████ ████████ █████████ ██ ██████████ ███████
  █████████████ ███████████ ███ █████████ ████ ███ █ ████ ███████████ ███ ███ ███
  ███████████ ███ ██████████ ███████ ███ ████ ███ ███ ██████████████████ ████

████ ██ ███ ██████ ████████ ██████ ████████ ████ ███ ███ ████████ ████████
█████████ ████████ ███ ██ ██████████ ███ ███████████ ███████████████████ ██ ███ ███
████ ████████ ███ ███ ███████ █████ █████ ███ ███ ██ ███ ██ ███████ ████ █████
█████ ██████ █ ████████ █████

## 3. Constraints that shaped the design

Four ████ constraints, ████ with a design consequence:

- **Monitor's `ws:` source takes only `{url, protocols}` — no custom headers.**
  Discovered during NUC relay recon, this rules out CF Access header auth
  (`CF-Access-Client-Id`/`Secret`) for the WebSocket `/tail` leg, even though
  `POST /send` (shelled via `curl`) is unaffected. This single constraint is why
  the relay's receive-side auth rides in `Sec-WebSocket-Protocol` instead of a
  header — see the decision log in ████
- **The auto-mode safety ██████████ denies launching raw ███████
  --permission-mode auto` via ██████ ("Create Unsafe Agents"). The capability to
  hand-launch a matching process exists (used above purely to prove
  unreachability); building any part of the design on ███ of that as a
  workaround was explicitly ████████ as a guardrail bypass.
- **`--fork-session` reads the parent transcript at child *boot*, not at fork-helper
  invocation time.** This means a parent that has already armed its Monitor
  before forking will always leak a "no ██████████ record" background-task note
  into the child's inherited transcript — the ordering ██ steps in the fork
  helper cannot avoid it, ████ choose which race it trades ███ it ████ the
  fork-ordering entry in §4).
- **Delivery is push, not poll.** █ Monitor event re-invokes an idle session —
  proven live, a parent session acknowledged a bus message autonomously with no
  human present. "Turn █████████ only defers delivery on a *busy* session (the
  event queues until ███ current step completes); an idle one wakes and acts
  immediately. This upgraded claudebus from █ notification convenience into ██
  autonomous coordination fabric, █████ is precisely why incoming bus messages
  are ███████ as untrusted peer requests rather than as trusted instructions —
  the same trust posture the whole file-based-mailbox field converges on (§1).

## 4. Decision log

████████ of the six commits on `main`, each with its rationale and any
declined-with-rationale alternatives:

- **`███████`** — initial channel bus █ hardened liveness. Named channels
  (`channel/alias` addressing, `global` ████████ for a ████████████
  orchestrator), auto-recycled aliases, and the ██████████ liveness guard
  (pid-recycling false-positives closed by ██████████████ process args against
  the inbox path; crash-orphaned listeners closed by recording the owning
  `claude` pid).
- **`███████`** — review fix set from a high-effort code review of `███████`,
  implemented by a ██████ session coordinating ████ its parent over the bus
  itself (the mechanism being fixed was the one used to fix it). Fixes: `send`
  ███ accepts a joined-but-never-armed peer (closes █ self-contradiction with
  the join→arm replay design — the previous refusal defeated the very window
  the replay exists to cover); atomic `mkdir` alias claim on `join` (closes a
  ████ where two concurrent joins could both pick `main` and the loser
  truncates the winner's inbox); re-arm replays ████ the end of the █████
  (`-n 0`) instead of the start (`-n +1`), so a Monitor restart no longer
  redelivers history; `.`/`..` rejected ██ █████████████ names everywhere
  (closed a self-inflicted `rm -rf` path-escape); new `cbus bootstrap` prints
  the canonical fork-child prompt from the binary so ██ can't drift from CLI
  behavior; fork-ordering reverted to arm-before-fork (see below).
  - **Fork-ordering, disproven then reverted:** attempt #1 tried arm-*after*-fork
    on the theory that a not-yet-armed parent wouldn't leak a background-task
    note into the child. This was empirically █████████ — `--fork-session` reads
    the transcript at child boot regardless (§3), so the note appeared anyway —
    and arm-after-fork additionally opened a child-announce █████ Reverted to
    ████████████████ the note is documented as cosmetic and unavoidable.
- **`███████`** — doc correction: delivery is █████ not deferred-until-idle-acts
  (§3). Verified live before writing the correction.
- **`███████`** — doc correction: the teammate mailbox is closed *by design*
  ███████████ backend, §2), not merely "unarmed" as the ████████ README wording
  implied. Rewritten once the direct internals ██████ landed.
- **`███████`** — `cbus ████████ collapsed the parent side of `/bus-branch` from
  3-4 model turns to one CLI call plus arming the Monitor. Channel derivation
  moved from skill █████ (model-executed, ███████████████████ into the shell
  script (deterministic). Also resolved a review finding about a hardcoded
  `cc-branch.sh` path by making the helper location resolvable via `CC_BRANCH`.
- ███████████████████████████ — NUC relay daemon, the first piece of the networked leg
  (`cbus-foc.1`/`.2`). Std-lib-only Go: `POST /send` → Maildir spool
  (`tmp→new→cur`), `GET /tail` → WebSocket authed via
  `Sec-WebSocket-Protocol: bearer.cbus.<token>` (the k8s-apiserver pattern,
  chosen in the WS-leg ████████ recorded in `cbus-foc`'s context: app-level
  token in the subprotocol ████ a █████████ query param, because query tokens
  leak into CF edge/relay access logs and a subprotocol rides in a header
  instead; Tailscale was considered and kept only as a documented fast-path
  alternative because the Mac was 17 days offline on the tailnet at recon █████
  failing the always-armed-receive-leg availability bar). `POST /send` keeps
  the stronger CF Access █████████████ ██████ `/tail`'s bypass-scoped token
  compromise only allows eavesdropping a channel, not injecting ████ one — a
  deliberate asymmetry, since the write path is the one that injects
  instructions into a live session. Reviewed by zen/gpt-5.5 (high thinking): one
  HIGH finding (a displacement/drain overlap on tail handover could duplicate
  delivery or kill the displacing ████ on █ mark race) was fixed and
  regression-tested (7-message handover █████ 7 delivered, █ unique, 0
  leftover); four MEDIUM findings applied (write deadlines, masking
  enforcement, frame validation, pong payload echo); two MEDIUM findings
  **declined with rationale**: fsync-level power-loss durability was judged
  overkill for a session-scoped message bus and documented as a █████
  limitation instead ██ implemented; sequence-number-first spool file naming
  was declined because it would break delivery ordering across a relay
  restart.

**Standing POC decision, not tied to one commit:** no Maildir or threading in the
*local* bus — the atomic `mkdir` join in `███████` already closes the realistic
concurrent-write race, ██ the added complexity wasn't justified ████████ ███████
durability landed *server-side* in the █████ instead ██████████████████████████ where a
process restart losing queued cross-machine messages is a real, higher-stakes
failure mode. This is a direct application of the ███ gap identified in §1.

## 5. Pointers

- The full landscape write-up this section condenses:
  `█████████████████████████████████████████` from the research session (fork-1)
  — session-scratchpad-ephemeral, not expected to survive; this document is its
  durable copy.
- The full NUC recon (host facts, port table, systemd template, the WS-header
  constraint in raw form): `██████████████████████████` from the same family
  of sessions, folded into `cbus-foc`'s bdx context.
- `bdx epic cbus-foc --project claudebus` — networked relay epic (NUC), 4
  subtasks, 2 closed (`.1` relay daemon, `.2` durable delivery + deploy), 2 open
  (`.3` cbus client remote support, `.4` end-to-end tunnel test).
- `bdx epic cbus-oq9 --project claudebus` — local orchestration & GUI epic
  (cross-CCS-profile test matrix, windowing identity + `cbus focus`, split-pane
  fork targets, `cbus list --json`, menubar GUI), all 5 subtasks open.
- █████████ ████████ ██████ ███ ████████████ ███ █ ████████████ ██████ ███ █████
  █████ ███████ ████ █████ █████████████ ████████████████████████████ ████ ██
  ███████████████████████████████ ███ █ ███████████████ ███████ ████████████
  ████████ █████████ ███████ ███████████████ █████████ ███ ███ █████████ █████
  ███████ ███ ██████████ ███ ██████████ █████ ████ ███████ ██ ███ ██████████
  █████████ ███ ██████ ███████ ██████████ ██████ ███████ ███████ ████ ██████
  ██████ █████ ██████
