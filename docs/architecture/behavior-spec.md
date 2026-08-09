# claudebus — behavior spec (as-is, exhaustive)

Canonical as-is behavior of every command, state file, wire format, framing rule, and liveness rule. Anchors are `bin/cbus:N` unless another file is named; relay = `relay/cmd/cbus-relay/main.go`, wire = `relay/internal/wire/ws.go`, spool = `relay/internal/spool/spool.go`. HEAD `f213e26`. Behavioral oddities are tagged **quirk** (preserve or rethink in a port), not bugs to fix. Items marked *(live-verified)* were reproduced on 2026-07-12.

> **STATUS (2026-07-13): bash-era reference spec — FROZEN.** This documents the bash
> client at `f213e26`, the contract the Go port (`cmd/cbus` + `internal/client` +
> `internal/core`, branch `go-port`) was differentially verified against (27/27
> verbs, MBP + NUC). The Go binary is now installed as `cbus` on both machines;
> `bin/cbus:N` anchors point at the retired implementation (in-repo until P3).
> Everything below remains true of the shared contract EXCEPT the port's intended
> deltas:
>
> 1. Unknown relay hosts and invalid channel/alias/host names are **hard errors** (bash: a
>    non-fatal stderr message from a die-in-substitution; `tail ch@bogus/al` even exited 0).
> 2. Remote HTTP calls have **timeouts: 4 s connect / 20 s total, no retry** (bash: none —
>    a wedged origin hung the Bash tool call).
> 3. `cbus auth status` **validates its host argument** (bash: the one unvalidated
>    `auth_get` path; Linux `../` read traversal).
> 4. An empty `--from` value is an error.
> 5. A `--` **flag terminator** is supported.
> 6. **Trailing junk is an error on fixed-arity verbs** (bash silently discarded it, e.g.
>    `cbus whoami junk`).
> 7. `--help` no longer lists the obsolete `CC_BRANCH` env line (branch is native). The
>    `CBUS_PYTHON (default python3)` help line is still printed for byte-parity
>    (COMPAT(P3 #4)) but the Go client ignores it.
> 8. New verb: `cbus --version` (prints the build stamp; bash had no version verb).
> 9. **python3 is no longer needed at runtime** (nor `tail(1)`, nor bash 3.2 compatibility).
> 10. **Max message size 1 MiB** — local sends now reject oversize messages, matching the
>     relay's `/send` body cap.
>
> Go-side equivalences (superseded 2026-07-19 for liveness/follower — see the dated
> note below; current as written for the framer): local framer = `core.LocalEmit`,
> shared with the relay (§8.3's divergence matrix unified per port-map D6 —
> tool-authored traffic byte-identical).
>
> **Doc-refresh note (2026-07-18, cbus-yle):** this file stays frozen to the bash
> client it audited and is not being expanded with post-cutover Go-native work
> (spawn/formations/distribution/roles shipped 2026-07-14→07-18 — none of it is
> port work, none of it has a `bin/cbus:N` anchor). See
> [overview.md](overview.md) for prose and
> [commit-timeline.md §2](commit-timeline.md#2-post-cutover-commit-timeline-go-native-no-bash-anchor--2026-07-14--2026-07-18)
> for the SHA table covering that surface. Nothing else in this file was found to be outright wrong —
> §9/§11's `install.sh`/`cc-branch.sh` entries describe the retired bash artifacts
> as they were, which remains true; `install.sh` itself is deleted from the repo
> as of 2026-07-18, but this file was never claiming otherwise in the present tense.
>
> **Doc-refresh note (2026-07-19, `cbus-8k9.4` P3 tranche 2):** the §4 argv-grep
> liveness predicate and the re-exec'd follower this file's Go-side-equivalences note
> described are gone for any peer armed by this binary. Listener identity is
> structural — `(pid, starttime)`, `internal/client/starttime.go` — with a
> `TRANSITION(P3T2)` argv-grep fallback that applies only to metas armed by a
> pre-P3 binary (bash or an earlier Go build) and is scoped to one release. The
> follower runs in-process from arm to exit; nothing re-execs, so a peer this binary
> arms carries no inbox path in its own argv at all — a bash-era (or pre-tranche-2
> Go) `ps`-grep against a NEW peer no longer matches, which is expected: this file's
> own §5 contracts (A4) already scoped that observable to "through Phase 2" / "until
> no bash cbus process can arm a tail anywhere." Two edge cases kept their exact
> pre-existing observable behavior across the rewrite: rename still invalidates the
> listener record (old tail reads stale, needs re-arm — deliberately now, where
> before it worked by the argv needle going stale by accident, port-map D1). The
> re-arm-loses-the-gap half of that sentence is no longer true: `cbus-8no` closes
> in M4 via the durable replay cursor (§8.6). And a dead-but-unreaped
> (zombie) listener still reads dead — pinned since the port by
> `TestArgvClauseZombieDead` against the argv clause; the structural rewrite
> regressed it briefly (a zombie's `/proc` stat and `kill -0` both still succeed,
> so its `(pid, starttime)` token still byte-matches), reproduced then fixed
> pre-ship (`f853ff2`) so the net observable answer is unchanged from bash.
>
> **Doc-refresh note (2026-07-19/20, `cbus-8k9.4` M6.2 — TRANSITION(P3T2) shim
> closure, `9a3a075`):** the fallback the note above named is now gone.
> `liveness_transition.go` is deleted whole; `listenerIdentityHolds` has one
> branch left — the recorded `(pid, starttime)` witness against the process now
> wearing the pid. An armed meta with no witness reads dead, the same posture R1
> already took for a stampless meta, so there is no longer a second answer for a
> pre-P3 arm to fall into. `procArgs` itself survives (it backs `close`'s owner
> guard, close.go:96, and `ownerFromPid`, marker.go:79 — unrelated call sites);
> what is gone is the fallback BRANCH, and the TRANSITION token with it. Field
> impact was verified nil before the drop, not assumed: every armed meta on the
> Mac carried a `listenerStart` (9 of 9), and the NUC's store held no peer metas
> at all — no pre-P3 arm survived anywhere in the fleet at drop time. The
> consequence going forward, stated plainly because it is a fleet compatibility
> line, not a subtle one: **a pre-P3 (pre-v0.4.0) binary arming against a shared
> `CBUS_DIR` after this upgrade writes metas the fleet reads as dead — the argv
> read shim is gone and listener identity is structural-only; fleet binaries
> must be v0.4.0+.** Riding the same span, unrelated to the shim itself:
> `cbus-fi3` (`1601f13`), a test-only golden-normalizer fix (a fixed-width pid
> column was normalized by digit-count, not by column, so the goldens only held
> for 5-digit pids — a container running this milestone's own gate exposed it).
> Also this span, M6.1 (`75e352d`): the deprecated `register`/`peers` verbs are
> removed, not just deprecated — see command-reference.md §15.
>
> **Doc-refresh note (2026-07-19, `cbus-8k9.4` M4 — D4/D5, local replay + local
> collision only):** §8.6 below is REWRITTEN, not amended, to describe the durable
> replay cursor (D4) that now decides local re-arm resume position; the null-
> `listenerPid` tri-state it replaced is preserved above only as the bash-era
> record. The rename-window loss (`cbus-8no`) and the `--force`-into-a-dead-gap
> hole are the same defect wearing two hats — seeking END on every re-arm silently
> discarded whatever arrived while nobody held the file — and both close via the
> cursor, with no rename-specific or gap-specific branch anywhere in the decision:
> see §8.6's table. §8.7's zombie-reattach hazard also closes here, via a
> displacement gate (D5) plus a follower self-identity check; see the closure note
> appended to §8.7. Local dormancy markers each name the remedy that actually
> works for their cause, not a uniform "re-arm" that was wrong three times out of
> four. One new local-only stderr warning (port-map D7) fires once on join/send/
> tail/rename/leave/whoami when `CLAUDE_CODE_SESSION_ID` is unset, naming what
> sessionless mode actually loses. None of this touches the wire, presence, the
> relay, or remote tail — those replay semantics are unchanged and out of scope.

## 1. Global invariants

- `#!/usr/bin/env bash`, `set -euo pipefail` (bin/cbus:1,14). Runs on macOS bash 3.2 and GNU bash 5.x identically — bash 3.2 is a hard floor.
- **python3 required for everything** — checked before dispatch; even `--help` dies without it: `cbus: python3 not found (set CBUS_PYTHON)` (bin/cbus:22). ~10 embedded python one-liners: jget/jset, payload builders, marker writes, /peers render, hook stdin parse, and the tail follower itself. No jq anywhere.
- `die()` prints `cbus: <msg>` to stderr, exit 1 (bin/cbus:19).
- **Two error dialects** *(live-verified, identical on bash 3.2/5.3)*: `die` → `cbus: …`, no line number; required-positional `${1:?usage: …}` → `<argv0-as-invoked>: line <N>: 1: usage: …`, exit 1, no `cbus:` prefix. `${2:?}` sites (243, 446) render bash's stock `parameter null or not set`. Full `:?` inventory: 243, 395, 442, 446, 489, 692, 710, 743, 749, 795, 827 (+ `${CBUS_DIR:?}` rm-rf guards at 666, 695).
- Timestamps: UTC ISO-8601 `YYYY-MM-DDTHH:MM:SSZ` via `date -u` (bin/cbus:20).
- `valid_name`: `^[A-Za-z0-9._-]+$`, rejecting literal `.`/`..` (bin/cbus:24). Applies to channels, aliases, hosts. Identical regex on the relay (main.go:33-37) — what makes names safe as path segments. **Quirks**: leading-dot names pass (`.remote` collides with the marker tree, glob-invisible everywhere); leading-dash names pass (`--active`/`-a` become un-filterable in `list`; no `--` terminator exists anywhere); no length cap (only filesystem NAME_MAX enforces one).

### Env vars read (complete)

| Var | Anchor | Effect |
|---|---|---|
| `CBUS_DIR` | :16 | state root (default `~/.claude-bus`) |
| `CBUS_PYTHON` | :17 | python interpreter (default `python3`) |
| `CLAUDE_CODE_SESSION_ID` | :93,189,432,685 | session identity; without it `whoami/leave/rename/branch` can't find "self" and join is never idempotent |
| `PPID` | :46,189,259,478 | owner-walk seed; `nosession-$PPID` marker id; `<host>-$PPID` unroutable from |
| `CBUS_ALIAS` | :478 | last-resort `from` on **local** send only; unvalidated; documented nowhere shipped |
| `CBUS_SITE_<HOST>_URL` | :134-139 | per-host relay URL override/extension. Name mangling: uppercase, non-`[A-Z0-9]`→`_`, ONE trailing `_` stripped (:137) — distinct hosts can collide |
| `CBUS_RELAY_LOCAL_URL` | :149 | loopback probe target (default `http://127.0.0.1:8090`) |
| `XDG_CONFIG_HOME` | :165 | Linux cred dir root (default `~/.config`) |
| `HOME`, `PWD` | :16,165,817,432 | defaults; `cwd` recorded at join |
| `CC_BRANCH` | :817 | fork-helper path (default `~/.claude/bin/cc-branch.sh`) |

### stdin (complete)

- `auth set … --token - / --cf-id - / --cf-secret -` — each `-` reads ALL of stdin (`cat`, :750-751); one `-` per invocation.
- `hook-exit` — reads SessionEnd JSON, extracts `session_id` (:682-684). **Blocks at a TTY until EOF**.
- Internal: relay auth headers piped to `curl -K -` (:225-235,266,299) — credentials never in argv (message payload IS argv, `-d "$payload"` :267).

### Exit codes

| Code | When |
|---|---|
| 0 | success; `--help`/no-args; empty `list`/`channels`; `prune` nothing-to-do; `auth status` always; `hook-exit` always; idempotent join; no-op rename |
| 1 | every `die`; every `${n:?}`; `whoami` with no registrations (the only read-only nonzero-on-empty); `leave` nothing matched; **`list @host` transport failure** *(live-verified — the rightmost python's JSONDecodeError exit 1 masks curl's code under pipefail; earlier "curl's code" claim is wrong; stderr = curl message + python traceback, no `cbus:` framing)* |

## 2. Address grammar

### Local (`split_target`, :80-89)
- `<channel>/<alias>` — split at FIRST `/`; a second `/` fails the alias (`cbus: bad alias "b/c"`).
- Bare `<alias>` — channel empty; `send`/`tail` resolve via `find_peer_channel` (:107-114): first of the **sender's own** channels (alphabetical glob order) containing that alias — ambiguity silent. `inbox`/`unregister` refuse bare aliases (`cbus: use <channel>/<alias>`).
- **Quirk** *(live-verified)*: empty channel half skips validation (`[ -z "$T_CH" ] ||`, :87) so **`/alias` ≡ `alias`** exactly; `ch/` → `bad alias ""`.

### Remote (`split_remote`, :121-132)
- `<channel>@<host>[/<alias>]`; channel = before FIRST `@`; remainder splits at first `/`. Each part validated **only if non-empty** (:129-131).
- Detection: a target is remote iff it contains `@` anywhere — checked on `$1` only, in `send` (:441), `tail` (:488), `list` (:581), `leave` (:651). `rename` rejects `@` outright (:707-709).
- **Quirk** *(live-verified)*: empty channel is accepted by send/tail too (only `leave` requires it, :653). `cbus send @host/al` posts `channel:""` → relay 400 → `relay send failed`. `cbus tail @host/al` **exits 0**, prints an unusable spec (`/tail?channel=&alias=al` — relay 400s pre-upgrade) AND writes a degenerate 2-level marker `.remote/<host>/<sid>` that whoami can't see (3-level glob, :781), `leave @host` can't remove, and a bare `cbus prune` sweeps unconditionally as a "legacy" marker (:201-205).

### Hosts & endpoints
- Built-in table: only `nuc → https://bus.example.com` (:140-143). Unknown host: `die "unknown relay host …"` fires **inside a command substitution** so it's a non-fatal stderr line *(live-verified)* — the command continues with `mode=public base=""`; the actual abort is usually the missing-credential die. With creds stored for a bogus host: `tail` exits 0 with a scheme-less spec + a marker for an unreachable site; `send` → `curl: (3)` → `relay send failed (public )`.
- `relay_base` (:148-155): probe `${CBUS_RELAY_LOCAL_URL}/healthz`, `curl -m 0.3`, body exactly `ok` → `local` (loopback, no CF headers); else `public`. Runs on EVERY remote command (≤0.3 s cost off-relay). Identity-blind: anything answering `ok` on loopback:8090 is trusted.
- `ws_url` (:157-162): `https://`→`wss://`, `http://`→`ws://`, **no default case** — other schemes yield empty string, no error.

## 3. On-disk state

```
$CBUS_DIR/<channel>/<alias>/meta.json       # registration
$CBUS_DIR/<channel>/<alias>/inbox.jsonl     # append-only mailbox
$CBUS_DIR/.remote/<host>/<channel>/<sid>    # remote identity marker {alias, ownerPid, ts}
$CBUS_DIR/<channel>/.reap.$$.<alias>        # prune temp (dot-prefixed = glob-invisible)
${XDG_CONFIG_HOME:-~/.config}/cbus/<host>/{token,cf-id,cf-secret}   # Linux creds, 0700/0600
```

- Dot-prefix invisibility is load-bearing: every `*/` glob (list :589, channels :616, prune :634, broadcast :337) skips dot entries, so `.remote` and crashed `.reap.*` temps are inert. A port using ReadDir must add an explicit filter.
- No chmod/umask outside the Linux cred store — any same-user process can read/append any inbox. No per-peer ACL, by design.

### meta.json (written whole at join, `json.dump indent=2`, :428-433)

| Field | Type | Notes |
|---|---|---|
| `alias` | string | rewritten by rename (:733). **Quirk**: `jset` coerces all-digit values to JSON int (`"alias": 42`) (:76) |
| `channel` | string | redundant with path |
| `sessionId` | string ("" if unset) | the `resolve_self` key |
| `cwd` | string | `$PWD` at join |
| `listenerPid` | null → int | set to `$$` at tail arm (:501); never cleared, only overwritten. Null-ness = "never armed" (drives replay + send gate) |
| `ownerPid` | null → int/null | claude-ancestor pid at arm (:502) |
| `host` | string | `hostname -s` fallback `hostname` (:433) |
| `ts` | string | join time; never refreshed as a field. **mtime** is what matters (10-min grace; jset at arm refreshes it) |

- `jget` (:30-35) swallows ALL exceptions → corrupt/torn meta reads as "field absent" (transient "off" flicker; self-heals). `jset` (:71-78) rewrites in place — **not** temp+rename, no locking; coercion: digits→int, ""→null.

### inbox.jsonl
One JSON object per line, single `printf '%s\n' >>` append (:483, :346 — O_APPEND line-atomicity assumed; unguarded beyond one stdio write). Message: `{"from","to":"<ch>/<al>","ts","text"}` (:480-482; values passed via env into `json.dumps` — quotes/newlines/unicode survive). Presence adds `"kind":"presence","event":"join|leave|rename|departed"` (:341-343). Created empty at join (`: >`, :427); truncated by explicit-alias rejoin (`rm -rf`+recreate).

### Remote identity marker
`.remote/<host>/<ch>/<sid>` = `{alias, ownerPid, ts}` (:186-191, written :281-284). `sid` = `$CLAUDE_CODE_SESSION_ID` or `nosession-$PPID` (:189). `ownerPid = find_owner_pid || $PPID` (:284 — outside a claude tree this records a transient shell pid: sweep-bait). Legacy machine-global markers (a FILE at the channel path) are deleted on next arm (:279) and always swept by prune (:201-205). Session-scoping is the anti-impersonation invariant (5db94ff): remote send reads only its own marker; two sessions can hold different aliases on one remote channel.

### Credential store
macOS: Keychain generic passwords, service `cbus-relay-<host>`, account = field; reads `security find-generic-password -w`; writes via `security -i` with the command on stdin (:166-177 — a value containing `"` or `\` would corrupt the command; `auth set` strips ALL whitespace :751). Linux: 0600 files under umask 077 (:178-181).

## 4. Liveness model (pure pid forensics — no heartbeat)

- `find_owner_pid` (:44-53): walk `$PPID` upward ≤16 hops, `ps -p -o comm=`, basename match `claude|claude-*`. *(live-verified: harness parent comm is `/Users/dev/.local/bin/claude` → matches at depth 0; node sits ABOVE claude, never shadows.)* No match → rc 1 → liveness degrades to pid-only.
- `pid_alive` = `kill -0` (:37).
- **`meta_listener_alive`** (:58-68) — ALL of: (1) `listenerPid` set + alive; (2) `ps -ww -p <pid> -o args=` contains the peer's **inbox path** (`grep -qF`) — pid-recycling guard; the exec'd follower keeps the path in argv precisely for this (:509-511,577); (3) recorded `ownerPid`, if any, alive — crash-orphan guard.
- **`peer_dead`** (:316-323): never-armed (`listenerPid` null) → dead only if meta.json **mtime > 10 min** (`find -mmin +10`, :319 — any meta rewrite resets the clock); armed-ever → dead iff `!meta_listener_alive`.
- Used by: `meta_listener_alive` → send gate (:463), list listen/off (:601), channels count (:623), join/rename takeover refusals (:422,:728). `peer_dead` → prune reaping (:356,364,372) AND presence recipient filter (:340) — deliberately the same rule as send, so unarmed peers still receive presence.
- Death teardown: NONE by the listener (no traps). Monitor stop kills the exec'd pid; liveness flips on its own (:495-498). SIGKILL'd listeners leave stale pids until prune/re-arm overwrites.

## 5. Presence broadcasts (`broadcast_presence`, :326-348 — in NO shipped doc; only the bootstrap prompt mentions it)

Wire: normal message + `kind=presence`, `event=<join|leave|rename|departed>`; one shared `ts` per event (:334). Recipients: every `!peer_dead` peer in the channel except `skip` (default = subject). Appends guarded `2>/dev/null || continue` (:344-346) against concurrent prunes. Renderer shows `kind=` in the frame header; **`event` is stored but never rendered** (:543-545).

| Event | Site | text | skip |
|---|---|---|---|
| join | cmd_join :434 | `joined <ch> as <alias>` | =from |
| leave | cmd_leave :665 (broadcast BEFORE rm -rf :666) | `left <ch>` | =from |
| leave (hook) | hook-exit → cmd_leave | `left <ch>` per channel | =from |
| rename | cmd_rename :734 (AFTER mv — from= is the NEW alias) | `renamed <old> -> <new>` | =from |
| departed (reclaim) | cmd_rename :730 | `departed (name reclaimed)` | **old alias** (actor ≠ subject) |
| departed (prune) | prune_channel :375 (after winning the mv claim — fires once) | `departed (listener gone)` | =from |
| departed (force) | cmd_unregister :696 | `unregistered` | =from |

Notes: `departed`/`leave` events carry an unroutable `from` (the subject's dir is gone — replies die "no such peer"). Presence persists in inboxes and replays on first arm (stale roster possible). One `cbus join` can emit `departed`(s) then `join` (auto-prune first). **Remote presence does not exist**: the relay's `sendReq` and `reframe` have no `kind` field — it's silently dropped (main.go:145-151, 227-233; filed cbus-ijx.5). **[SUPERSEDED 2026-07-16: cbus-ijx.5 shipped — join/departed cross the relay server-side; phase 2 (client-originated leave/rename) remains open. Post-cutover truth: overview.md.]**

## 6. Prune / GC

- `prune_channel` (:350-385): legacy v1 entry (meta.json at channel level) → whole channel dir removed if dead. Per peer: if `peer_dead` → atomic reap: `mv` to `.reap.$$.<peer>` (one winner) → post-claim re-verify (alive & slot refilled → drop copy; alive & slot empty → mv back; still dead → `rm -rf` + `departed` broadcast) → rmdir empty channel. `pruned <ch>/<peer>` on stderr (:373).
- `cmd_prune [ch]` (:632-647): re-emits reap lines on **stdout** (join's auto-prune leaves identical lines on stderr — quirk). Remote markers swept **only on bare `cbus prune`** (:640-645) — `prune <ch>` never touches `.remote` (README:227-228 overstates); nothing else sweeps markers ever.
- `prune_remote_markers` (:193-221): per-session markers removed when `ownerPid` dead; legacy FILE markers always removed; empty dirs rmdir'd.
- Auto-prune trigger: `cmd_join` only (:398). `list` never prunes.
- `cmd_unregister <ch>/<al>` (:691-699): unconditional force-removal of ANY peer (no liveness/ownership check; works on live listeners), `departed "unregistered"` broadcast.

## 7. Send path

### 7.1 Local `cbus send <target> [--from X] [--force] <text…>` (:440-485)
- Flags parsed AFTER the target, stopping at the first non-flag token; text = `"$*"` join (single spaces; a message beginning with `--from`/`--force` is eaten as a flag). Empty text → `cbus: empty message`.
- **The gate** (:460-469): never-armed (`listenerPid` null) → always accepted (first arm replays). Dead ex-listener → refused `"…is not listening; use --force to queue anyway"`; `--force` → stderr warning + queue best-effort (re-arm starts at END, so the line may never deliver — admitted at :866-870). Live → accepted.
- `from` chain (:470-478): `--from` (unvalidated, free text — header-injection surface, see §8.4) → own registration in the TARGET channel → first own registration anywhere (lexicographic glob order) → `$CBUS_ALIAS` → `<hostname -s>-$PPID` (unroutable). Send never fails on identity; sessionless senders succeed with an unreplyable from (README:60's "resolved from session id" is only the middle of the chain).
- Success: `sent to <ch>/<al> (from <from>)`. **Quirk**: the final append is unguarded — a peer pruned between gate and append kills the command with a raw bash error.

### 7.2 Remote `cbus send <ch>@<host>/<al> [--from X] [--force] <text…>` (:237-270)
- Alias mandatory. `--force` **accepted and ignored** ("the spool always queues", :244) — no shipped doc scopes README:241's `--force` text to local.
- `from`: `--from` → this session's marker for host+channel (`<ch>@<host>/<al>`) → `<hostname>-$PPID`. Never consults local registrations or `CBUS_ALIAS`.
- POST `<base>/send` body `{"channel","alias","from","text"}` (no ts) built via env→python (:263-265); curl config on stdin carries auth. Failure → `cbus: relay send failed (<mode> <base>)`. **No timeout on the curl** (only the 0.3 s probe is bounded) — a wedged-origin hang blocks the Bash tool call until the harness timeout (fill-r2-1 §4).
- **Ack ambiguity**: the relay writes the spool BEFORE the HTTP response (main.go:192-199) — "relay send failed" can mean "queued (and maybe delivered)"; a retry is a NEW message (no idempotency key; response `id` is discarded by the client).

## 8. Framing (the load-bearing wire format)

### 8.1 Measured harness constants (provenance: detailed_changelog.md:106-119,180-187; live re-measured 2026-07-13)
| Constant | Value | Anchor |
|---|---|---|
| Monitor per-line truncation | EXACTLY 500 chars *(live-verified 2026-07-13, bisected: 500 passes, 505 cut)* | :504 comment; main.go:207 |
| Body wrap (UTF-8-byte-aware, never splits a codepoint) | 440 bytes *(unchanged, 2026-07-13)* | :522; main.go:239 |
| Per-notification ceiling / relay safe cap | ~3000 *(confirmed 2026-07-13)* / `wsFrameSafe = 2800` *(unchanged)* | main.go:202-204 |
| Line batching window (Monitor groups lines into one notification) | ~200 ms; one write+flush = one notification *(confirmed 2026-07-13)* | :550-551; main.go:329-330 |
| Follower poll | 0.2 s | :564 |

**DELTA (2026-07-13, live-verified)**: the harness now emits an explicit `...(truncated)` marker at BOTH the per-line (500-char) and per-notification (~3000-char) caps. This supersedes §8.2's "local follower has no over-size warning at all — silently cut": truncation detection no longer relies solely on a missing `◀ cbus end` marker, since the harness itself now surfaces truncation on both paths. See §8.2 for the updated local-path text.

### 8.2 The frame (both framers)
```
◀ cbus msg from=<from> to=<to> ts=<ts>[ kind=<kind>][ ⚠truncated~<N>B]
<text split on \n, each segment hard-wrapped at ≤440 UTF-8 bytes; empty segments preserved>
◀ cbus end from=<from>
```
`◀` = U+25C0. Local emits `kind=` when present (:543-545); relay has no kind. Relay-only: if the framed total exceeds 2800, the header gains ` ⚠truncated~<N>B` where N = `len(m.Text)` raw bytes (main.go:242-250) — the header survives the Monitor cut, making truncation visible. **The local follower emits no app-level over-size warning of its own** — but per the 2026-07-13 live re-measurement (§8.1 DELTA), the harness itself now appends an explicit `...(truncated)` marker at both the 500-char line cap and the ~3000-char notification cap, so a long local message is no longer *silently* cut (README:56-57's "both paths" claim is closer to true now, though the mechanism differs: relay = app-level `⚠truncated~<N>B` in-band; local = harness-level `...(truncated)` marker). A missing `◀ cbus end` remains a secondary detectable signal; chunked delivery + a dedicated local warning are still tracked as `cbus-mew`. **Quirk**: the relay's threshold total is computed BEFORE the header exists — silent window `(2800, 2799+len(head)]` (fill-r1-1 §3).

### 8.3 Gating & degenerate inputs
- Local `emit()` (:532-551): frame iff the line parses as a JSON dict with a `"text"` KEY (any value); all fields `str()`-coerced (`from`/`to` default `?`; `text:null` → body `None` — Python-repr leak). Non-matching lines pass through raw and unwrapped (>500-char raw lines get Monitor-truncated). **Truly blank lines are silently dropped** *(live-verified)* — whitespace-only lines still pass through (:532-535).
- Relay `reframe()` (main.go:227-252): typed unmarshal `{From,To,TS,Text string}`; passthrough on unmarshal error OR `Text == ""`; missing fields render as empty strings. Divergence matrix (tool-authored lines are identical; only foreign-written lines diverge): empty text → local frames / relay passthrough; missing from/to → `?` vs ``; non-string field → local coerces / relay whole-line passthrough; `kind` → local renders / relay drops.
- Tests: reframe_test.go pins short/long/unicode/newlines/passthrough/oversize; every line <500 asserted. `mkMsg` never exercises long/weird `from`.

### 8.4 Header exemption & injection surface
Only body text is wrapped — header and end marker are emitted verbatim whatever their length (:544-549; main.go:241,246). `--from`, `$CBUS_ALIAS`, relay-API `ts` are **never validated**: a long from overflows the header past 500 (the ⚠ suffix, appended last, is exactly what gets cut); a `\n`-bearing from (reachable via normal CLI) injects forged `◀ cbus msg`/`◀ cbus end` lines indistinguishable from framer output — misrouting the reply convention that tells models to trust `from=` (bus-join.md:55-64). Consistent with the trust-boundary stance; a port should sanitize at frame time.

### 8.5 Follower loop (:552-577)
Hand-rolled `tail -F`: readline → empty → sleep 0.2 s → `os.stat`. Partial lines buffered in `pend` until `\n` (:556-562). Reopen when `st_ino` changed OR size < tell (:569) → offset 0, full replay of the fresh file, `pend` reset (survives rejoin truncate/recreate). Path vanished → keep old fd, poll forever — never exits on its own, **except** one narrow rotation race *(the only self-termination mode)*: stat succeeds → file vanishes → reopen `OSError: pass` leaves `f` closed → next `readline()` raises uncaught ValueError → traceback, exit (fill-r1-2 §4; Monitor notifies, standard re-arm recovers). stdout reconfigured UTF-8 `errors="replace"` (:517-520); the `-c` source is pure ASCII (`◀` escaped) for locale independence.

### 8.6 Replay selection

**Bash-era mechanism (frozen, historical):** the arming shell read the PREVIOUS
`listenerPid` before overwriting it: never set → `'+1'` = read from byte 0 (full
replay); ever set (alive or dead) → `'0'` = seek END. `'+1'/'0'` are vestigial
`tail -n` spellings; python tests only `== "0"`. Rename preserved meta (mv +
alias patch) so a post-rename re-arm followed from the end — which is exactly
`cbus-8no`, the rename-window loss this null-`listenerPid` tri-state could not
avoid: it could only answer "byte 0 or END," and END silently discards whatever
arrived while nobody held the file.

**Go mechanism (M4, `internal/client/cursor.go`, local only — REWRITES this
section, does not amend it):** a durable per-peer sidecar, `.cursor` next to
`meta.json`/`inbox.jsonl`, records `<dev> <ino> <offset>` for the last frame
boundary the follower actually delivered, written temp+rename (never torn,
best-effort — a failed write costs duplicates on the next arm, never a crashed
follower) and only while this process still holds the identity check (§8.7): a
displaced or orphaned follower stops moving the cursor, not just stops reading,
or it would drag the stealer's or the new epoch's resume point past messages it
never delivered. The offset is the last `\n` actually emitted, not the raw byte
count off the fd — a partial line still sitting in the read buffer is excluded,
so a re-arm can never resume mid-frame (F2, `TestCursorNeverPointsMidFrame`: the
persisted position always sits immediately after a newline).

`resolveResume` is the whole decision, run once per arm before meta is
overwritten (the migration row needs the PREVIOUS `listenerPid`). It is 8 rows,
and rename / `--steal` / a `--force`-into-dead-gap re-arm are deliberately NOT
three of them — each one just satisfies the ordinary "cursor valid" row, which
is the point: an if-branch naming any of them would mean the design is wrong.

| # | State | Resume at | Why |
|---|---|---|---|
| 1 | No `.cursor`, peer never armed (fresh join) | byte 0 | First arm, unchanged from the bash tri-state |
| 2 | No `.cursor`, peer WAS ever armed (pre-M4 binary, or a join that outran its first cursor write) | seek END, once | The migration rule: reproduces v0.4.0 semantics exactly for the one case with no better information, then self-heals — the follower writes a cursor immediately on open, so this row cannot recur for the same peer |
| 3 | `.cursor` valid: dev+ino match the open inbox, offset ≤ current size | `.cursor`'s offset | The general case. This ONE row is what a re-arm, a re-arm after a dead `--force` gap, a post-rename re-arm, and a post-`--steal` re-arm all resolve to — none of the four gets its own branch |
| 4 | `.cursor` present, dev+ino mismatch | byte 0 | The inbox was recreated (a rejoin's `rm`+recreate); join already truncated the new file, so a full replay loses nothing |
| 5 | `.cursor` present, offset past current EOF | byte 0 | Truncate-in-place; same reasoning as row 4 |
| 6 | `.cursor` present but unreadable/malformed (not 3 whitespace-separated fields, or an unparseable/negative offset) | byte 0, explicitly NOT row 2's migration path | CORRUPT is a distinct state from ABSENT: a damaged record means the position is genuinely unknown, and seeking END would silently discard whatever it could not account for. Replay costs duplicates instead — the trade this whole mechanism exists to make |
| 7 | `cbus join` (new epoch) | n/a — deletes `.cursor` beside its inbox truncate, then row 1 or 2 governs the NEXT arm | A join starts a new epoch; the previous epoch's cursor is void by definition. No special join branch in `resolveResume` itself — join just removes the input row 3-6 would otherwise read |
| 8 | Inbox rotation mid-follow (`rotated()`: dev+ino changed or size shrank) | byte 0 on the reopened file, cursor republished against the NEW inode | Not an arm-time decision (the follower is already running) but the same reasoning as row 4: the file underneath changed identity, so the fresh file is replayed from its start and the sidecar is rewritten so the NEXT arm doesn't read a stale inode |

Local only: the wire, the relay, and remote `tail` have no cursor and are
untouched — remote replay is the relay spool's business (§10).

### 8.7 Local arm has NO ownership or collision gate (fill-r2-0 §1)
`cmd_tail` checks only that the inbox exists (:494) — no `meta_listener_alive` on the previous pid, no sessionId comparison, no session required at all (contrast: join :422 and rename :728 refuse live-listener takeover; the relay displaces). Consequences: double-arm → TWO live followers, every message delivered twice; meta pins to the newest pid only (observable state diverges from delivery topology); second arm starts at END; ownerPid reassigned to the hijacker; simultaneous first-arms both fully replay; **zombie reattach** — a follower orphaned by prune later sees a NEW session's rejoin inode and shadow-replays a stranger's inbox to the old Monitor indefinitely. Missing meta.json is tolerated (`|| true` jsets) → a fully functional listener invisible to list/send/prune. Only guard: skill discipline ("skip if already armed", bus-branch.md:22-23).

**Go-side closure (M4 N2/N3, `internal/client/identity_follow.go` +
`follow.go`, closes `cbus-0r8`):** two mechanisms together close this section's
entire hazard class for any peer this binary arms. First, a displacement gate at
arm time (D5): a second local `tail` on an already-armed alias is refused
outright — relay-style — unless the caller passes `--steal`, which takes over
cleanly because the cursor (§8.6) belongs to the peer, not the follower, so the
stealer resumes exactly where the displaced one stopped. Second, a running
follower carries proof of which listener it is (its `(listenerPid,
listenerStart)` witness) and re-checks that meta still agrees, at minimum every
~5 poll ticks and — this is the part that specifically closes zombie reattach —
**every time the inbox rotates, checked BEFORE the reopen, never after**: a
rotation is exactly the foreign-reopen trigger (a stranger's `join` reclaiming
this path), so if the identity check finds this process is no longer the
recorded listener, it goes dormant and emits a marker instead of reopening and
shadow-streaming the stranger's inbox. The polarity is deliberate (R14, frozen):
anything the check cannot confirm reads NOT-MINE, inverting §1's file-read
leniency, because the destructive direction is reversed here — a false continue
leaks another session's traffic into someone else's terminal, where a false
dormant only costs a quiet window and a re-arm. Dormancy is a one-way door (never
re-entered) and each of its four causes (`displaced`, `rejoined`, `renamed`,
`gone`) gets its own marker line naming the remedy that actually works for that
state — a uniform "re-arm to resume" was wrong for three of the four (`04dfbc8`).
The gate itself is not atomic and takes no lock (R-B): two arms racing it can
both pass before either writes meta, but that race self-corrects, since the
loser's own identity check finds it is not the recorded listener and it goes
dormant within one interval — a bounded duplicate window, not a permanent second
listener.

## 9. Command reference (dispatch :836-914)

Usage heredoc → stdout, exit 0 on no-args/`-h`/`--help` (:854-912); unknown command → `cbus: unknown command '<X>' (cbus --help)` (:913).

| Command | Anchor | Spec |
|---|---|---|
| `join <ch> [alias]` | :394-438 | Auto-prune channel first (may emit `departed`s). Idempotent per (session, channel): prints `already joined "<ch>" as "<alias>"` + arm reminder, **requested alias ignored** on this path. Auto-alias: `main` else lowest free `fork-N` (`pick_alias` :387-392), claimed by bare `mkdir` (≤50 retries — pick keys on meta.json, claim on the dir: can spin on a half-created sibling). Explicit alias: live-listener slot refused (`taken by a live listener`); dead slot → **`rm -rf` destroys the dead peer's queued inbox** (no departed broadcast — asymmetric with rename's reclaim). Fresh truncated inbox; meta with `listenerPid:null`; `join` presence; 3-line stdout ending in the arm-via-Monitor warning (:435-437) |
| `register [alias]` | :838 | deprecated ≡ `join global` (documented only at README:266). Row is bash-era only — **removed from the Go client, M6.1 (`75e352d`, v0.7.0)**, falls through to unknown-command |
| `send` | §7 | |
| `tail <ch>/<al>` (local) | :487-578 | **Monitor-only; blocks forever under Bash.** Records pids, execs follower (§8.5-8.7) |
| `tail <ch>@<host>/<al>` | :272-293 | Instant Bash command. Needs token only (no CF pair). Writes/overwrites the session marker; prints the ws arm spec: `url: wss://…/tail?channel=<ch>&alias=<al>`, `protocols: ["bearer.cbus.<token>"]` (token in cleartext by design), `description: cbus:<ch>@<host>/<al>` persistent. No alias-free pre-flight — collisions surface as displacement |
| `list`/`peers` `[--active|-a] [ch \| [ch]@host]` | :580-612 | Local row: `listen|off  <ch>/<al>  pid=<p>  <host>  <cwd>` (`%-28s` address col). Legacy v1 rows advertised for prune. Never prunes. Remote (`@` in **$1** only): renders `/peers` → `listen|off  <ch>@<host>/<al>  queued=N lastSeen=…`. **No active-only remote view exists by any arg order**: `list ch@nuc --active` silently discards trailing args (:296 reads $1 only); `list --active ch@nuc` treats `ch@nuc` as an unmatchable local filter → misleading `no active listeners`; `active ch@nuc` is structurally dead (:842 prepends `--active`) |
| `active [ch]` | :842 | ≡ `list --active` |
| `channels` | :614-630 | `<ch>  N peers (M listening)`; skips legacy v1; extra args dropped |
| `prune [ch]` | §6 | |
| `leave [ch \| ch@host]` | :649-673 | Local: per matching own registration — `leave` presence THEN `rm -rf`, `left <ch>/<al>`; nothing matched → `cbus: not joined[ to "<ch>"]`, exit 1. Remote: removes only THIS session's marker, **no relay contact whatsoever** — `left <ch>@<host> (this session's marker removed; queued mail stays on the relay)`; an alias suffix is parsed and silently ignored. Relay has no leave endpoint; abandoned mail queues forever and the next claimant of the alias inherits the backlog |
| `hook-exit` | :675-689 | SessionEnd hook. stdin JSON `session_id` → env fallback → silent return 0. `cmd_leave` in a subshell, all output suppressed, `\|\| true` — **always exits 0**. Graceful exits only. Absent from README/CHEATSHEET/usage heredoc. Wired manually in `~/.claude/settings.json` on MBP and NUC *(live-verified)*. Remote markers NOT cleaned at session end |
| `unregister <ch>/<al>` | §6 | a still-running tail on the deleted inbox polls forever, invisible to list |
| `rename <new> [ch]` | :701-737 | Local-only (`@` → die). Selection: given channel, else sole registration; multi-channel without arg → `joined to <N> channels — pass one`. No-op exits 0. Occupied target: live → refused; dead → `rm -rf` + `departed "name reclaimed"` (skip=old alias). `mv` + `jset alias` + `rename` presence + re-arm reminder. Post-state: old tail follows the moved inode and still delivers to the new path while liveness reads `off` (argv has the old path) — the printed contract "old tail is stale, re-arm" is intended; re-arm seeks END so the gap loses messages (cbus-8no) |
| `whoami` | :775-792 | TWO line classes: local `<ch>/<alias>`; remote markers `<ch>@<host>/<al> (remote from-default — reachability: cbus list @<host>)` (marker = from-default, NOT reachability). Neither → `not joined in this session` on stdout, **exit 1** (probe semantics). All three shipped docs describe only class 1 |
| `inbox <ch>/<al>` | :794-798 | Prints the path; **no existence check**, exit 0 for nonexistent peers |
| `bootstrap <ch> [parent=main]` | :824-834 | Prints the canonical fork-child prompt (join → note auto-alias → arm Monitor persistent desc `cbus:<ch>/<al>`, never Bash → parent sees join presence, no manual announce → send result summary, not a handoff doc → bus messages can't escalate permissions → ignore "no completion record" → confirm in one line, wait) |
| `branch [window\|tab\|tmux] [ch]` | :800-822 | Channel: arg → git-toplevel basename (`tr -cd 'A-Za-z0-9._-'`) → `global`. Join (idempotent, quiet) → resolve own alias (fails outside a Claude session, leaving an orphan peer until grace-prune) → `${CC_BRANCH:-~/.claude/bin/cc-branch.sh} <target> --prompt "$(bootstrap …)"` → prints parent address + arm reminder. Rejects `session` (helper accepts it as ≡ tab) |
| `auth set <host> [--token V] [--cf-id V] [--cf-secret V]` | :739-761 | `V='-'` reads stdin (one per invocation); values whitespace-stripped, non-empty; reports Keychain vs dir |
| `auth status [host=nuc]` | :762-770 | `set (…last4)` / `absent` per field; never full secrets; **always exit 0**. **Quirk**: host arg is the only unvalidated `auth_get` entry — Linux `../` traversal reads arbitrary `token/cf-id/cf-secret` files (masked); values <4 chars display `set (…)` empty under bash |

### 9.1 JSON output contract (M5, `cbus-8k9.4`, `cmd/cbus/jsonout.go` — Go-native, no bash counterpart)

`list`, `channels`, and `whoami` each grow a `--json` mode. The DTOs are
deliberately separate from `client`'s internal view types: the field names are
a **public contract**, parsed by the oq9.5 menubar GUI (which shells `cbus list
--json`), so an internal rename cannot change them by accident.

**Envelope discipline.** Every level is an OBJECT, never a bare array, so a
level can gain sibling keys without breaking a consumer; `list`'s peers carry
named keys rather than positional ones so windowing identity (window/pane/term
— not landed yet) arrives as purely additive fields when it does. `schemaVersion`
(currently `1`) bumps only on a BREAKING change — adding a field is not one,
and a consumer that treats an unknown key as fatal is the one at fault.

**`list --json` / `active --json`.** `emitListJSON` renders the same
`client.ScanStore` snapshot the text path renders, under the same `--active`
and channel filters, so the two renderings can never drift on who is listening
— the divergence a GUI would surface first and a text-only test would never
see. `ScanStore` is deliberately NOT `ChannelRoster` (the formation-save roster
reader, `formation_save.go:51`, called from `formation_save.go:144` and
`formation_plan.go:113` — no rename call site), which errors on an absent
channel and drops a peer whose meta is torn, since a save must not record what
it couldn't read: `ScanStore` keeps a torn peer with
blank fields, because `list` has always shown it with `?` columns and hiding a
peer is how a user loses track of a session. Both functions now carry a
comment naming the difference so the next reader doesn't unify them by eye.

- `listenerPid` is `omitempty` — absent, never `0`, when the peer never armed;
  `0` is itself a real pid-shaped value and would read as one if emitted.
- `scope` is pinned `"local"` — the key exists now, before `"remote"` does, so
  a consumer written today survives `"remote"` appearing later.
- A legacy v1 channel entry is marked explicitly (`legacyV1: true`) and carries
  an empty `peers` array rather than being omitted or half-populated, so a
  consumer iterating `channels[].peers[]` gets nothing for it instead of
  choking (no silent caps).
- `--active` (R22) drops a non-listening peer, then drops a channel left with
  zero peers — identical to the text path, which prints no row for either.
- **The zero-peers drop is unconditional, not `--active`-gated (`cbus-vjo`,
  post-release).** A channel with an empty `peers` array is dropped from the
  unfiltered path too, matching `list`/`channels`/`channels --json`, all three
  of which already agreed on this — `list --json` was the lone dissenter,
  emitting any peerless store-root directory (e.g. `$CBUS_DIR/roles`, written
  by `install-roles` beside the channels) as a phantom channel. Legacy v1 stays
  exempt from the drop (`legacyV1: true`, `peers: []`) — it is peerless BY
  CONSTRUCTION, predating the alias level, and R18 wants it visible so a GUI
  can surface the prune remedy.
- An empty store is a valid document with an empty `channels` array, not the
  text path's `no peers registered` sentence.

**Remote refusal (R15).** `--json` is local-only in M5; `refuseRemoteJSON`
refuses BOTH ways of asking for it remotely, because each reaches a different
silently-wrong answer if left unguarded: `list @host --json` would reach
`runListRemote`, which never reads the remaining args and drops the flag
(silently answering the text-mode question instead); `list --json @host` would
never reach remote detection at all (it inspects `args[0]` only), so the
`@`-target falls through and is misread as a local channel filter. `active
<ch>@<host> --json` inherits the same refusal — `active` routes through the
same `runList`/`refuseRemoteJSON` path (dispatch just prepends `--active`). The
pre-existing `--active @host` dead quirk (§9 table, `list`/`active` rows) is
untouched when `--json` is absent — this milestone does not touch remote JSON
at all; that shape rides the relay-wire follow-up.

**`channels --json`.** `emitChannelsJSON` — same envelope conventions, one
count object per channel (`name`, `peers`, `listening`), same skip rules as
text (legacy v1, zero-peer channels). No remote form exists for `channels`, so
there is no analogous refusal.

**`whoami --json` (R16).** `emitWhoamiJSON` emits ONE document shape whether or
not the session is joined: an unjoined session gets the same keys with empty
`local`/`remote` arrays, never a different document and never the text path's
`not joined in this session` sentence — a consumer parses one thing. `joined`
is spelled out in the body (the same answer the exit code carries) so a
consumer reading only stdout never has to infer it from two array lengths. The
**exit code is preserved**: still `1` when both collections are empty, matching
the text path's probe semantics that scripts already branch on. `local` and
`remote` are separate keys rather than one list with a kind field — they are
genuinely different identities (a local registration has no host; a remote
from-default marker always does), and the split makes that legible without a
discriminator. The remote fixture in the test suite is written by
`WriteRemoteMarker`, the same writer `cbus tail <ch>@<host>` calls, rather than
staged by hand — and a marker with no local registration still counts as
`joined`, pinned as its own case since the flag and the exit code must agree in
every combination, not just the common one.

## 10. Relay spec

### Startup (main.go:391-420)
Flags: `-listen 127.0.0.1:8090`, `-spool spool`, `-token-file token` (absolute in the systemd unit). Token: env `CBUS_RELAY_TOKEN` wins, else file, trimmed; fatal if empty or containing `=`, `,`, `/`, space (must be subprotocol-safe). **Read once — rotation requires restart**; deploy never rotates. Routes: `/send /tail /peers /healthz`; `ReadHeaderTimeout: 5s` only.

### Auth
- HTTP bearer for `/send`/`/peers`: `Authorization: Bearer <token>`, constant-time compare (main.go:121-125).
- ws subprotocol for `/tail`: scan `Sec-WebSocket-Protocol` entries for `bearer.cbus.<token>` (constant-time on the suffix); echo the matched protocol in the 101 (main.go:127-143). Rationale: Monitor `ws:` cannot send headers.
- CF Access (infra outside the repo): `/send`,`/peers` need CF-Access-Client-Id/Secret at the edge; `/tail` has a path-scoped Access bypass.

### POST /send (main.go:153-200)
`405 POST only` / `401 unauthorized` / body capped 1 MiB (`http.MaxBytesReader`) / `400 bad json | bad channel/alias | empty text`. `from` empty → `"unknown"`; `ts` empty → server RFC3339 now, **else stored verbatim** (garbage accepted; bash client never sends ts). Stored line: `{"from","text","to":"<ch>/<al>","ts"}\n` — Go-map **alphabetical key order** (local lines are insertion-ordered; parse JSON, never key order). Write spool → poke hub → `{"ok":true,"id":"<spool filename>"}`. No existence check — sends to any valid name create the spool dir and queue forever.

### GET /tail (main.go:254-364)
Param validation (400) **before** auth (401 — plain HTTP, pre-upgrade). Failed `wire.Upgrade` → log + bare return = **implicit empty 200** (quirk; stock ws libs would 400/426). Per-conn `WriteTimeout 10s`. Attach displaces the existing tail for the key (`close(old.done)` under mutex, then replace, main.go:62-72); **detach is compare-and-delete on tail pointer identity** (main.go:74-81) so a displaced tail's deferred detach can't evict its displacer — port-critical; `seen` refresh is unconditional. Reader goroutine: read deadline `pongGrace+pingEvery` = 120 s; echoes ping payloads; pong OR any text frame refreshes `lastPong` + `hub.touch`. Drain loop: `ListNew` → per message: done-check → Read (ErrNotExist → continue) → strip newlines → reframe → ONE OpText frame → write error = return with message still queued (redelivery) → `MarkDelivered` (ErrNotExist benign) → touch; then select on notify / done / readerDone / 30 s ping tick (pong timeout when >90 s stale).
- **notify semantics** (main.go:68,83-93): 1-buffered channel; `poke` is a non-blocking send under the hub mutex. The token is level-triggered ("queue may be non-empty" — loop always re-lists all of `new/`); capacity-1 + always-rescan = no lost wakeups, no producer blocking; pokes coalesce. Write-then-signal ordering is required. Broken port symptom: push latency silently degrades to ≤30 s (the ping-tick re-list backstop).
- **Teardown**: deferred `conn.Close()` sends a best-effort **EMPTY close frame** (no status code) then closes TCP (ws.go:283-286) — client-side that reads as **1005** (close-frame-received: displacement, pong timeout, spool errors), vs **1006** (no frame: sleep, network loss, relay restart/`log.Fatal`, failed-write path). The re-arm doctrine keys on "1006 (or similar)"; whether the Monitor distinguishes 1005 is unobservable from the repo.

### GET /peers (main.go:366-388)
Bearer; no method check. Merge of spool walk (`queued` per dir) + hub snapshot (`connected`) + `seen` lookups: `{"<ch>/<al>":{"connected":bool,"lastSeen":RFC3339,"queued":int}}`. `lastSeen` is hub memory only — zero time (`0001-01-01T00:00:00Z`) after restart; senders never update it. **Quirks**: a connected-but-never-mailed peer appears; once it disconnects it **vanishes entirely** (spool dirs are created only by Write) — absence vs `off` keys on "has ever received mail", and the re-arm ritual reads this output. During the ~90-120 s zombie window after a silent client death, `connected:true` and a *refreshing* `lastSeen` actively lie while deliveries are being marked-then-lost. No query params; channel filtering is client-side python; `queued` counts `new/` only.

### GET /healthz
Unauthenticated `ok\n` — load-bearing for the client probe and deploy check.

### Spool (spool.go)
Maildir per `channel/alias`: write `tmp/<name>` then rename into `new/`; `MarkDelivered` renames `new/→cur/`. Names `UnixNano.%06d(seq).json` — process-lifetime atomic counter; lexicographic = chronological except across backwards clock steps/restarts. Crash-safe (invisible/queued/delivered, never torn); **no fsync** (declined — session bus). `ListNew` sorts names; missing dir = empty. **No retention anywhere** *(live-verified: no cron, no timers; 32 delivered files retained across 8 peer dirs)* — `cur/` grows forever, tmp orphans persist, peer dirs never deleted, dead test channels immortal in `/peers`.

### wire (ws.go) — the RFC 6455 subset a port must speak
Server upgrade requires GET, `Upgrade: websocket`, `Connection: upgrade` token, version 13, 16-byte key; echoes accept + selected subprotocol. Client `Dial` is TCP-only (no TLS) — wstail is loopback-only, NOT a client-side bridge. Opcodes: Text/Close/Ping/Pong only — **no binary, no fragmentation, no extensions/compression** (hard errors). `maxFrame = 1 MiB` enforced **read-side only** — `WriteFrame` has no size check, so a near-cap /send reframes into a frame the contract itself rejects; a compliant reader drops the connection and the message is already in `cur/` → deterministic loss (fill-r1-1 §5). Masking direction enforced both ways; control payloads ≤125 read-side. WriteFrame is mutex-serialized, always FIN=1.

### Deploy & systemd
deploy.sh: rsync src → build on the host (a failed build ABORTS the deploy; the `-h` smoke line is shown to the operator and nothing gates on it) → seed token if missing → sed-template the unit (`@USER@`/`@DEST@`) → `sudo cp && daemon-reload && enable && restart && is-active` → healthz. Requires **passwordless sudo** (non-interactive ssh); a sudo failure aborts AFTER the new binary is on disk → stale-process state with no flag. Unit: `Restart=on-failure`/5 s, a dedicated service user, WorkingDirectory anchors relative defaults, no hardening directives. Restart drops all tails (client 1006).

## 11. Claude Code integration contracts (cc-integration A1-A17)

| # | Harness assumption | Encoded at |
|---|---|---|
| A1 | `CLAUDE_CODE_SESSION_ID` exported into Bash subprocesses = stable identity | :93,189; cc-branch.sh:19 |
| A2 | Bash tool's tree descends from a `claude|claude-*` comm within 16 hops | :44-53 |
| A3 | Monitor kills its command's pid on stop — exec makes the recorded pid the Monitor's child; no trap needed | :495-515 |
| A4 | Monitor truncates any stdout line at 500 chars | :504 |
| A5 | Monitor batches lines written together into ONE notification | :508-511,550-551 |
| A6 | ≤440-byte body lines survive A4 with headroom | :522 |
| A7 | ws frame = one notification, but ~3000-char tail truncation (measured) → `wsFrameSafe 2800` | main.go:202-204 |
| A8 | Monitor accepts a ws source `{url, protocols, description, persistent}`; token = subprotocol | :285-292 |
| A9 | Network loss surfaces as `[WebSocket closed: 1006]` on the described Monitor → re-arm trigger | bus-join.md:26-33 |
| A10 | `description: cbus:<addr>` convention lets TaskStop find the right Monitor | bus-rename.md:19-22 |
| A11 | Persistent Monitors survive turns; events wake idle sessions (push) | bus-join.md:37; :832 |
| A12 | SessionEnd hook: JSON on stdin with `session_id`; env may be absent; graceful exits only | :675-689 |
| A13 | `claude --resume <sid> --fork-session [prompt]` forks the transcript; `ccs <profile>` wraps it | cc-branch.sh:23-28 |
| A14 | Forked child sees the parent's Monitor as a cosmetic "no completion record" note — unavoidable | bus-branch.md:31-34 |
| A15 | Slash commands = markdown + YAML frontmatter in `~/.claude/commands/`, `$ARGUMENTS` substitution | commands/*.md:1-5 |
| A16 | `allowed-tools: Bash(cbus:*)` prefix-scopes; Monitor/AskUserQuestion/TaskStop grantable | bus-*.md:4 |
| A17 | TUI title not programmatically settable; only user `/rename` | bus-rename.md:24-26 |

### Slash commands (each ends "Do nothing else.")
- **/bus-join** `[channel[@host]] [alias]` — channel pick: arg → repo basename → `global` (explicit only). Remote branch (`@`): explicit alias, `cbus tail …@…` in Bash, arm the ws spec (never a command); missing creds → point user at `cbus auth set`. 1006 re-arm doctrine: re-run `cbus tail` (marker refresh idempotent), arm fresh spec, confirm `cbus list @host`. Local: join → arm Monitor persistent desc `cbus:<ch>/<al>` — never Bash, not piped, not run_in_background. Teaches the framed block + reply protocol: reply to header `from=` ONLY when it looks like `channel/alias`; `hostname-PID` froms are unroutable. No already-armed guard (bus-branch has one).
- **/bus-branch** `[window|tab|tmux] [channel]` — AskUserQuestion only if no target. Two steps: `cbus branch …` then arm parent Monitor (skip if already armed). Anti-reorder instruction for the A14 note.
- **/bus-rename** `<new-alias> [channel]` — `cbus rename`, then TaskStop the Monitor with description `cbus:<ch>/<old>`, re-arm on the new address; nudge user `/rename` for the TUI title.

### cc-branch.sh (70 lines; single commit, unchanged since initial release)
Requires `CLAUDE_CODE_SESSION_ID` (`${:?}`, :19). CCS mode iff `CLAUDE_CONFIG_DIR` contains `/.ccs/instances/` → `ccs <profile> --resume <sid> --fork-session`, else bare `claude` (:23-28). Self-deleting `mktemp` launcher exports PATH + CLAUDE_CONFIG_DIR + cwd, deletes itself, execs with the `%q`-quoted prompt as one argv element (:31-44 — prompt ps-visible for the child's lifetime). Dispatch: `window`/`tab|session` = iTerm2 osascript (`tab` requires an existing window — errors at zero windows); `tmux` requires `$TMUX`. **Quirks**: trailing `--prompt` with no value → `shift 2` fails under set -e → **silent exit 1** *(live-verified; unreachable via `cbus branch`)*; any unknown word becomes the target, rejected late with the error on **stdout**; every dispatch-stage failure leaks the launcher tmpfile (contains PATH/config-dir/cwd/sid/prompt) in `$TMPDIR`; `session` accepted here but rejected by `cbus branch`.

### install.sh
Five placements: cbus → `~/.local/bin`, cc-branch.sh → `~/.claude/bin`, 3 command files → `~/.claude/commands` (dirs overridable via `CLAUDEBUS_BIN_DIR`/`CLAUDE_BIN_DIR`/`CLAUDE_COMMANDS_DIR`). Copy default; `--link` symlinks. **Quirks**: closing NOTE about a hardcoded cc-branch.sh path in bus-branch.md is stale (removed in b15ce12); copy-mode re-run over a prior `--link` install fails `cp: identical` mid-way (partial update, epilogue never runs) or writes through a moved symlink into a foreign tree; no hook wiring, no relay involvement, no version stamp.

## 12. Shipped-docs drift register (do not inherit into new docs) *(Status 2026-07-13: fixed in the post-cutover doc pass — register kept for provenance.)*

| Shipped claim | Truth |
|---|---|
| README:313 "requires … tail -F" | No `tail(1)` since a38999b; python follower is the whole delivery engine |
| README:69-80 / CHEATSHEET:126-128 "listenerPid is the tail process", "tail -n +1 -F" | Mechanism renamed: the exec'd python follower; semantics preserved (internal comments equally stale: :461,:495-497,:510,:513,:703) |
| README:56-57 "Both paths … ⚠truncated notice" | Relay-only; local silently cut (changelog admits it) |
| README:187-188 "no duplicate delivery on handover" | At-least-once; narrow handover race delivers to both tails (main.go:315-340) |
| README:227-228 "cbus prune sweeps markers when their session dies" | Only bare `cbus prune`; channel-scoped never does |
| README:60 "from resolved from $CLAUDE_CODE_SESSION_ID" | 5-step chain incl. `$CBUS_ALIAS` and `hostname-$PPID` (§7.1) |
| README:174 "epic in progress; client side lands separately" | Shipped (692af95); epic closed |
| README:241 `--force` unscoped | No-op on `@host` targets |
| README env list (3 vars) / usage heredoc | Missing `CBUS_SITE_<HOST>_URL`, `CBUS_RELAY_LOCAL_URL` (README) and `CBUS_ALIAS` (everywhere) |
| whoami descriptions (README:245, CHEATSHEET:31, heredoc :874) | Omit remote-marker lines + exit-1-on-empty |
| CHEATSHEET:85 `ws://localhost:8090` | Printed spec is literally `ws://127.0.0.1:8090` |
| CHEATSHEET:68,90 "wss… + CF Access" | The ws /tail leg carries NO CF headers (path-scoped bypass; token-only) |
| bus-join.md:31-32 / CHEATSHEET:134 "nothing is lost" | Sleep-window loss (~90-120 s) — see architecture §7 |
| install.sh:34-35 NOTE | Stale since b15ce12 |
| Presence + hook-exit + `peers` alias | Absent from all shipped docs (changelogs only) |
| "No broadcast" caveat (README:312) | No *user-facing* broadcast; presence uses an internal one |

## 13. Consolidated quirk registry (beyond those inline above)

Preserve-or-rethink flags for a port; dispositions in [port-map.md](port-map.md).

1. Idempotent join ignores a requested alias — you silently keep the old name (:399-406).
2. Bare-alias resolution is alphabetical-glob-order dependent; ambiguity silent (:107-114).
3. Explicit-alias join reclaim destroys the dead peer's inbox with no broadcast; rename's reclaim broadcasts — asymmetric (:423-427 vs :729-731).
4. Send-then-join-prune can discard mail: a >10-min never-armed peer is accepted by send yet prunable (:319 vs :460-462).
5. meta.json writes are non-atomic in-place; torn reads read as field-absent (self-healing flicker) (:71-78).
6. `channels`/`whoami`/`hook-exit` silently drop extra args (:843,846,849); `list_remote` drops everything after `$1` (:296).
7. `leave` broadcasts before removal (safe only because the subject is skipped); prune removes then broadcasts (:665-666 vs :374-375).
8. `hostname -s || hostname` portability dance ×3 (:259,433,478); relay's empty-from spelling is `"unknown"` — two unroutable-sender spellings system-wide.
9. Column widths (`%-28s`) are cosmetic contracts; do not parse `list` by column (:308,596,608).
10. ~~Deprecated surfaces kept: `register` (≡ join global), `peers` (≡ list, documented nowhere)~~ — **dropped, M6.1 (`75e352d`, v0.7.0)**: zero programmatic callers meant the drop needed no compat shim; both now answer to unknown-command. Legacy v1 entries and legacy machine-global markers are untouched by this drop and remain live surfaces.
11. Marker `ownerPid` fallback `$PPID` outside a claude tree = transient shell pid → marker is immediate sweep-bait (:284).
12. The `.remote/<host>/<name>` namespace rule: FILE at channel level = legacy/garbage, swept unconditionally; DIR = per-session markers (:193-221).
13. `cbus tail` is two verbs in one name: local = blocking Monitor source; remote = instant Bash spec-printer with identity side effects (:487-488).
14. The 1006 re-arm is model-memory driven — nothing watches the ws; a forgotten instruction leaves the tail down until noticed (`cbus list @host` shows off/absent).
15. Frame markers unescaped in-band; body lines starting `◀ cbus ` are spoofable (consistent with trust posture).
16. Never-Bash warning duplicated across 3 skills + 5 CLI sites; the bootstrap prompt deliberately single-sourced in the binary.

## 14. The anchor model (resume era)

Added after the v1 sections above; semantics of the reboot-recovery chain
(`formation resume`, `apply --mode`, the launch-intent guard, always-anchor).

- **Always-anchor.** An envelope without an `anchorAlias` is a defect, not a
  style gap: the anchor is the restore seat. Save refuses to *mint* one
  (refuse-over-autopick: a wrong auto-anchor becomes the wrong restore seat
  later), but a *refresh* of a legacy anchorless file saves-and-warns — a hard
  rule in `Validate` would fail `LoadFormation` at the refresh gate's
  refuse-to-overwrite load and strand every legacy file the invariant exists
  to converge. That is why the warning lives in `Formation.AnchorWarning()`,
  outside `Validate`, permanently.
- **First hop.** `formation resume` launches only the anchor, from a bare
  shell, with the recorded profile forcing `ccs <profile>` (a fresh
  post-reboot shell has no CCS env; a plain `claude --resume` against the
  wrong config dir comes up blank under the same session id — the failure
  that motivated profile capture). All identity prohibitions are enforced at
  a third parity site (`resumeAnchorWorld`, beside `decidePeer` and
  `BootstrapPeer`); a change to one must visit all three.
- **The guard has two lifetimes on purpose.** The intent *marker* must
  SURVIVE its writer (the launcher CLI exits on the success path), so it is a
  file with a TTL and a same-sid-join clear — launcher liveness plays no role
  in clearing (a liveness rule would void the guard milliseconds after every
  write). The *reclaim window* must DIE with its holder, so it is a kernel
  lock (`flock`, released on death by construction). Same file, two
  mechanisms, one reason each; the claim itself is a single-syscall
  first-writer-wins `os.Link` and needs no serialization. Reclaim OVERWRITES
  the corpse by rename and never unlinks: an absent path is the claim signal,
  so any reclaim that unlinks first reopens the race it heals.
- **The decision brief carries stable facts only.** Saved mode, origin,
  transcript-at-compose-time, machine — all fixed when the brief is composed.
  Liveness is the one volatile fact, and a stored liveness marker lying in
  both directions is this tool's founding lesson; the brief names the
  dry-run as where present-right-now comes from. Examples are **runnable as
  written**: present-transcript AND on-this-host (blank machine means here,
  the exact negation of apply's skip), capped, absent when empty. The roster
  may honestly render a foreign peer's transcript as present while the
  example declines to name it — the roster states facts, the example
  promises actions.
- **`--mode` is a per-run override, never a write.** In-memory peer-mode
  rewrite on the `--channel` precedent; the planner's gates run byte-unchanged
  underneath, which is the entire safety argument.
- **Profile capture is fleet-floor semantics.** Join stamps the session's own
  CCS instance (structural check on `CLAUDE_CONFIG_DIR`); save refreshes it
  like `cwd`. An older binary's meta rewrite drops the unknown key (the
  typed-struct rewriter class), so the field is best-effort until the fleet
  floor includes it; hand fills in envelopes survive regardless.

## 15. Codex integration semantics (multi-harness)

The load-bearing rules, beyond the verb surface (command-reference has that):

- **Discovery is a protocol notification, not a hook.** SessionStart hooks do
  not fire in the app-server/`--remote` topology (live-probed), so `cbus codex`
  learns the TUI's thread from a passive initialize-only connection receiving
  `thread/started` — which also rules out `thread/list` (it returns the user's
  whole history; "the one live thread" is not knowable from it).
- **The bridge is the peer's listener.** It arms with its own pid as the
  liveness signal and tails the inbox with the shared follower loop; a codex
  peer therefore has real structural liveness like any other, and must never
  run `cbus tail` itself.
- **One frame = one injection, and injections are expensive.** Each framed bus
  message becomes exactly one codex turn — steer when a turn is active, else
  open one (resuming the thread if the server forgot it). Presence frames are
  skipped on purpose (a full model turn is too costly for join/leave
  ceremony) while the cursor still advances over them.
- **The stop-hook treats timeout as failure, never as signal.** It long-polls
  under the codex hook limit; traffic returns a block decision codex injects
  as a continuation turn; no traffic returns nothing and the stop proceeds.
  The wait must stay under codex's own timeout or the hook dies for nothing.
