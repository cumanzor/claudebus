# Changelog (detailed)

## [2026-07-13 01:48:02 UTC] [Port/Go] Phase 1 (4/N) — remote send/list client (M7), explicit timeouts, no retry

[Attempt #1]

Fourth Phase 1 milestone: the remote HTTP client (M7) and its first live
proof against the real relay. Approved clean — reviewer independently
re-ran the live differential and got byte-identical output.

[Files Changed]

- `internal/client/remote.go`, `remote_test.go` (new) — in-process
  `net/http` client with explicit timeouts (4s connect, 20s total) and NO
  retry (`POST /send` is non-idempotent and unkeyed — no idempotency token
  exists on the wire to dedup a retry against; Go's transport never retries
  a POST regardless). `ResolveRemote` builds the front door URL plus auth
  headers: `Authorization: Bearer` always; the `CF-Access-Client-Id/Secret`
  pair only in public mode (local/loopback mode skips it, matching the
  relay's asymmetric auth model). Missing credentials are hard errors
  pointing at `cbus auth set <host> ...`. `RemoteSend` wraps `POST /send`
  with `core.SendReq`; `RemoteList` wraps `GET /peers` into
  `core.PeersResponse`.
- `internal/client/identity.go` — `RemoteFromDefault` (session marker →
  `<shorthost>-<ppid>` fallback chain) and `ShortHostname`. Deliberately
  never consults local registrations, matching bash's split from-default
  chains (protocol.md §7.1/§3.1) — a by-design remote/local asymmetry, not
  an oversight.
- `cmd/cbus/main.go`, `main_test.go` — `send <ch@host/al>
  [--from|--force] TEXT` (`--force` accepted and ignored remotely — the
  spool always queues) and `list [<ch>]@<host>` (sorted rows: listen/off,
  queued count, lastSeen — RFC3339Nano round-trip verified); `active`
  reproduces the remote dead-quirk (no active-only remote view exists,
  matching bash's structurally-dead `active ch@host`).

[Testing Notes]

- Hermetic `httptest`-driven tests via `CBUS_SITE_<TESTHOST>_URL` (never
  binds real `:8090`): public mode sends CF headers, local mode skips them,
  missing creds error correctly, `/peers` decodes.
- Reviewer's double-`-` drain-once rider pinned as a test (a second `-`
  value in one `auth set` invocation must not silently drain stdin twice).
- **First live public-mode differential test**: ran `list p1diff@nuc` and
  `send --from EQ ...` through both bash `cbus` and `cbus-go` against the
  real NUC relay — byte-identical output on both. Leaves throwaway
  `p1diff` channel residue on the relay (no spool GC exists, §11 of
  protocol.md — expected, not a bug).
- Incidental proof of contract A6 (credential store locations): the test
  run read real Keychain-stored creds successfully via the injectable
  runner path.

[Possible Ripple Effects]

- None functional — read/send paths only, no state-mutating verbs
  (join/tail/prune/etc.) exist yet.
- Ruled fix, riding P1.5: an explicit `--from ""` (empty but present) must
  be a hard error, not silently fall through the from-default chain — parity
  with bash's intent over a silent, surprising fallback.

## [2026-07-13 01:37:47 UTC] [Port/Go] Phase 1 (3/N) — auth set/status credential store (M8)

[Attempt #1]

Third Phase 1 milestone: the credential store (M8) and the `auth
set`/`auth status` verbs. Approved clean — reviewer re-ran the keychain
integration test himself and confirmed the leak checks are negative.

[Files Changed]

- `internal/client/cred.go`, `cred_test.go` (new) — `CredStore` over a
  platform backend: macOS shells to `security(1)` (service
  `cbus-relay-<host>`, account = field name; the secret rides `security -i`
  stdin, never argv, matching bash exactly) or Linux XDG files (`0600`,
  parent dir `0700`, no trailing newline). Deliberately keeps shelling to
  `security(1)` rather than an in-process Keychain API — defers the native
  ACL re-auth prompt (port-map.md open question #4). The security runner is
  injectable so unit tests assert the exact argv/stdin without ever
  executing `security`.
- `cmd/cbus/main.go`, `main_test.go` — `auth set <host>
  [--token|--cf-id|--cf-secret V]` (`V='-'` drains stdin once per
  invocation; all whitespace stripped) and `auth status [host]` (masked
  last-4 per field). `auth status`'s host argument is now validated —
  closing the bash gap where an unvalidated host let a Linux `../` path
  traverse the credential dir (behavior.md §2.5/§12); recorded as a
  documented, deliberate Class-C delta rather than bug-compatible
  reproduction.

[Testing Notes]

- Mock-runner tests assert exact argv/stdin for every field/host
  combination, including the explicit-keychain path.
- File backend: permission bits and round-trip verified in a tempdir.
- `StripWhitespace`/`MaskTail` unit tests; `auth set`/`status` handlers
  tested end-to-end via a file store.
- An opt-in integration test (env-gated `CBUS_KEYCHAIN_IT`, skipped by
  default) round-trips through the real `security(1)` binary against an
  explicit temp keychain path on every call — never the login keychain or
  the default search list — and deletes the keychain on cleanup; skips
  cleanly if `security` is absent. Ran once: PASS, search list unchanged,
  no entry leaked into the login keychain. Reviewer independently re-ran it
  with the same result.

[Possible Ripple Effects]

- None functional yet — still no verb wired to real network I/O.

## [2026-07-13 01:27:53 UTC] [Port/Go] Phase 1 (2/N) — address resolution (M2) + bare-alias resolution; probe no-redirect fix

[Attempt #1]

Second Phase 1 milestone: full address grammar (M2) and session identity
(M3 storage half), plus the P1.1 probe-redirect finding fixed. Approved
clean — no findings; reviewer mutation-verified the redirect guard.

[Files Changed]

- `internal/client/addr.go`, `addr_test.go` (new) — `IsRemote` (`@` anywhere
  in the target = remote); `ParseLocal` (split at the first `/`, keeping the
  empty-channel-skips-validation quirk so `/alias` ≡ `alias`; a second `/`
  makes the alias invalid, e.g. `a/b/c` → bad alias); `ParseRemote` (split at
  the first `@` then the first `/`, each present part validated, empty parts
  skipped); `Parse` wires bare-alias resolution through an injected
  `Resolver`. Name validation reuses `core.ValidName`; malformed names are
  hard errors under the approved soft→hard ruling (unlike bash's
  non-fatal-die-in-substitution). Table tests lifted from protocol.md §1.2.
- `internal/client/identity.go`, `identity_test.go` (new) — `CBUSDir`,
  `SessionID`, `ResolveSelf` (`$CBUS_DIR/*/*/meta.json` scan keyed on
  `sessionId`, dot-dir blind, same glob order as bash's `find_peer_channel`),
  `FindPeerChannel` (first own channel holding a bare alias), torn-meta-read
  tolerant (a mid-write read is treated as absent, matching `jget`'s
  exception-swallowing). Tests run over a seeded temp `$CBUS_DIR`.
- `internal/client/endpoint.go`, `endpoint_test.go` — P1.1 finding fixed:
  `probeLocalOK` now sets `CheckRedirect` → `ErrUseLastResponse` so the
  loopback trust-by-port probe never follows a 3xx off-loopback to an
  ok-serving host; new `httptest` 302 case asserts the probe falls back to
  public mode instead of trusting the redirect target.

[Possible Ripple Effects]

- None functional yet — still no verb wired to real network/disk I/O in
  `cmd/cbus`.

[Testing Notes]

- `go test ./...` green.
- Reviewer mutation-verified the redirect guard (flipped `CheckRedirect` back
  to default and confirmed the new 302 test fails), confirming it's load-bearing
  rather than incidental.

## [2026-07-13 01:21:30 UTC] [Port/Go] Phase 1 (1/N) — cbus-go skeleton + front-door/endpoint resolution; conformance rig hardened

[Attempt #1]

First Phase 1 milestone: the client binary skeleton and its first ported
module (M7 front-door/endpoint resolution), plus reviewer's m5
conformance-rig hardening (finding F1: test-cache staleness).

[Files Changed]

- `cmd/cbus/main.go` (new) — verb-dispatch skeleton for the ported client
  (installs as `cbus-go`): `--help`, honest not-implemented stubs for every
  verb, unknown verb → exit 1.
- `internal/client/endpoint.go`, `endpoint_test.go` (new) — `SiteURL` (built-in
  `nuc` host table + `CBUS_SITE_<HOST>_URL` env-mangling, keeping the
  strip-one-trailing-underscore quirk; unknown host promoted to a hard typed
  error per the approved P1 soft→hard ruling); `ResolveFrontDoor` (0.3s
  loopback `/healthz` probe, exact-`ok` line → local mode/no CF Access, else
  public); `WSURL` scheme swap (empty string on non-http(s) schemes, matching
  bash). Unit-tested: env-mangling table, WSURL table, local-vs-public
  selection via `httptest` (never binds real `:8090`, per the hermeticity
  ruling).
- `relay/internal/conformance/conformance_test.go`, `doc.go` — reviewer F1
  rider: `trackSources` reads `relay/` + `internal/core` sources at test
  start so `go test`'s cache invalidates whenever the runtime-built relay
  changes (verified empirically: a `main.go` edit now re-runs the rig
  instead of returning a stale cached PASS); the caching gotcha is
  documented in `doc.go`. Added negative-path assertions: bad bearer → 401,
  invalid channel name → 400.
- The standing `TestGofmtClean` gate (m4) caught and required a fix for a
  formatting slip in the new test files — its first live save.

[Possible Ripple Effects]

- None functional yet — `cmd/cbus` is a stub binary, no verb does real work.
- One finding from review: the front-door probe doesn't yet pin
  redirect-following behavior against curl's defaults — rides P1.2.

[Testing Notes]

- `go test ./...` green; conformance rig re-verified to actually invalidate
  its cache on relay changes (previously a silent staleness risk).

## [2026-07-13 01:05:20 UTC] [Port/Go] Phase 0 (5/N) — wire conformance rig + m4 riders + relay wire-struct adoption — PHASE 0 CLOSED

[Attempt #1]

Final Phase 0 milestone: proves the shared core wire structs match the live
relay contract end-to-end, closes the m4 reviewer riders, and single-sources
the relay's wire structs onto `core`. Reviewer ran the rig himself
(`-count=1`, green in 1.1s) and confirmed `reframe_test.go`'s byte-diff is
EMPTY across the whole phase (5e71ddc..253f4a2). P0 is now closed.

[Files Changed]

- `relay/internal/conformance/conformance_test.go`, `doc.go` (new) — hermetic
  wire conformance rig: builds and runs the real `cbus-relay` binary in a
  sandbox (temp spool, ephemeral loopback port, token via env — no
  `~/.claude-bus`, no NUC contact) and drives `POST /send`, `GET /peers`,
  `GET /tail` (ws) against the shared core wire structs. Proves
  `core.SendReq`/`core.PeersResponse`/`core.Message` match the live relay:
  `/send` accepts a marshaled `SendReq` (200 `{ok,id}`); `/peers` decodes into
  `PeersResponse` with the queued peer; `/tail` delivers exactly
  `core.Reframe` of the reconstructed stored line, and `core.Message`
  round-trips it. Reuses the in-repo `wire.Dial` ws client (as `wstail`
  does); std-lib only, `-short`-skippable.
- `internal/core/testdata/emit_golden.py` — m4 reviewer riders: restored the
  `◀` (U+25C0) escapes in the head/end so the lifted `wrap()`/`emit()` diffs
  byte-exact against bin/cbus:522-551; the BEGIN marker now declares the one
  structural omission (bin/cbus:521's argv setup, N/A to a stdin driver) —
  provenance now reads "byte-exact modulo one declared omission". Added a
  `sys.stdin` UTF-8 reconfigure in the driver code (not the lift) so a
  C-locale regen can't crash.
- `internal/core/testdata/gen_corpus.py` — relabeled the in-band-marker case
  as `(spoof, framed)`.
- `internal/core/testdata/corpus.jsonl`, `corpus.golden` — regenerated,
  byte-identical to before.
- `relay/cmd/cbus-relay/main.go` — replaced the relay's private `sendReq` and
  presence structs with `core.SendReq`/`core.PeersEntry` (identical json
  tags), single-sourcing the `POST /send` body and `GET /peers` entry shapes
  with the package the future Go client imports. Pure refactor: byte-identical
  wire output.

[Possible Ripple Effects]

- None functional. The relay's wire output is byte-identical pre/post
  refactor, guarded by the conformance rig (re-run green against the
  rebuilt binary) and `reframe_test.go` (byte-unchanged).
- Carry-forward for P1: the conformance rig's build cache can go stale
  across runs without `-count=1` (self-invalidating cache risk) — rides the
  first P1 commit. Also carried: rig negative-path assertions, and a
  deploy.sh NUC-cleanup check.

[Testing Notes]

- Reviewer independently ran the rig (`-count=1`, green in 1.1s) and diffed
  `reframe_test.go` across the entire phase (5e71ddc..253f4a2) — EMPTY.
- **Phase 0 complete: shared core + conformance harness, 7 code commits
  (cc17a16, 347e8c8, 48afb14, c7db0ab, 7031c5b, 253f4a2 + the earlier
  4cac62f restructure), all reviewer-approved, HEAD 253f4a2.**

## [2026-07-13 00:51:45 UTC] [Port/Go] Phase 0 (4/N) — golden framer parity corpus + oversize flip-point + gofmt gate

[Attempt #1]

Phase 0 golden gate pinning `core.Reframe` byte-for-byte to the bash follower's
`emit()`/`wrap()` (bin/cbus:515-551), closing m3's two conditional findings (F1
gofmt, F2 oversize flip-point) in the same milestone. Reviewer independently
reproduced both the live-follower capture and the D8 cross-parse assertion
byte-identically.

[Files Changed]

- `internal/core/testdata/emit_golden.py` (new) — verbatim lift of the bash
  follower's `wrap()`/`emit()` plus a stdin driver, used only to generate the
  golden corpus (no runtime dependency).
- `internal/core/testdata/gen_corpus.py` (new) — builds `corpus.jsonl` the way
  the client does (python `json.dumps`), covering the plain tool-authored
  shapes where local `emit()` and relay `reframe()` agree.
- `internal/core/testdata/corpus.jsonl`, `corpus.golden` (new, 56 lines/6861B)
  — the pinned input/output pair.
- `internal/core/golden_test.go` (new) — `TestGoldenCorpusParity` asserts
  `golden == Reframe(line)+"\n"` per message. Header records two one-time
  validations: (1) the python lift matches a live hermetic `cbus tail`
  follower byte-for-byte (no env/encoding drift); (2) the D8 cross-parse
  assertion — `emit()` frames Go-marshaled lines identically to
  python-marshaled ones, so canonical-Go bytes hold safely during
  coexistence.
- `internal/core/frame_test.go` — added `TestReframeOversizeFlipPoint`,
  pinning the protocol.md §4.4 header-less-total quirk (warn fires at total
  > `WSFrameSafe`, header excluded from the count) so a future
  header-counting fix can't silently change the threshold.
- `internal/core/name_test.go`, `frame_test.go` — `gofmt -w`; new standing
  `TestGofmtClean` gate over the module (closes F1).
- `internal/core/message.go`, `message_test.go` — D8 comment fixes:
  `flexString` swallows objects/arrays as raw text (not just numbers/null);
  bytes are canonical-Go, not python-identical.

[Possible Ripple Effects]

- None functional — test/tooling-only commit. `reframe_test.go` remains
  byte-unchanged.
- The golden corpus is now the trip-wire for any future framer change on the
  plain-shape domain; degenerate-input divergences stay pinned separately in
  `frame_test.go` (m3).

[Testing Notes]

- `go test ./...` green; reviewer independently reran the live-follower
  capture and the D8 cross-parse check and reproduced both byte-identically.
- One provenance rider (byte-exact `◀` U+25C0 restoration + a declared
  argv-line omission in the generator's BEGIN marker) plus two nits are
  deferred to m5.

## [2026-07-13 00:37:30 UTC] [Port/Go] Phase 0 (3/N) — degenerate-input matrix + rune-safe 440B wrap property tests

[Attempt #1]

Third Phase 0 milestone: pins the relay-side framer divergence matrix
(protocol.md §4.5) as table tests, plus property tests for the rune-safe
440-byte body wrap. Conditionally approved by reviewer — findings F1 (gofmt)
and F2 (oversize flip-point pin) were folded into m4 and are closed there.

[Files Changed]

- `internal/core/frame_test.go` (new) — 8-case degenerate-input matrix
  against `core.Reframe`: empty/missing/non-string/null `text` and any
  non-string field all assert byte-identical passthrough; missing `from`/`to`
  frames with empty routing fields; `kind` is dropped on the relay path.
  Property tests for the rune-safe wrap across 1/3/4-byte runes x lengths
  0-300 x limits {1,3,4,7,440}: byte-exact reassembly, valid UTF-8 per piece,
  no over-limit multi-rune piece (a lone over-limit rune is the sole
  exception). Full-framer rune-safety test at 440B.

[Possible Ripple Effects]

- None — test-only commit, no production changes.

[Testing Notes]

- All new tests green; `reframe_test.go` untouched.
- Reviewer's two findings (gofmt formatting on this file + `name_test.go`;
  the oversize flip-point wasn't yet pinned as its own test) were addressed
  in m4 rather than a follow-up commit — see the m4 entry above.

## [2026-07-13 00:33:32 UTC] [Port/Go] Phase 0 (2/N) — shared domain types + single-sourced ValidName

[Attempt #1]

Second Phase 0 milestone: the shared type layer both the relay and the future Go
client build on (port-map.md §5 A1/A3 wire+line shapes). Types-first was ratified by
the orchestrator (the golden harness constructs Messages, so they are a prerequisite).

[Files Changed]

- `internal/core/message.go` (new) — `Message{From,To,TS,Text,Kind,Event}`, the domain
  shape of a local inbox line / relay stored line / presence variant. Decoding is
  key-order-agnostic (JSON) and lenient via a `flexString` shadow type: a JSON number
  or null in any string field decodes to its literal / "" instead of erroring — the
  "json.Number for legacy int aliases" tolerance from the brief, applied to typed
  string fields (Decoder.UseNumber only reaches interface{}). Marshal emits the
  plain-line shape `{from,to,ts,text}` with kind/event omitted unless set (presence).
  Non-object JSON still errors (caller treats as "not a message", mirroring the framer
  gate). Plus wire structs `SendReq` (POST /send body) and `PeersEntry`/`PeersResponse`
  (GET /peers), matching the relay verbatim.
- `internal/core/name.go` (new) — `ValidName`, the one name rule shared verbatim by the
  client (bin/cbus:24) and the relay, with the §1.1 property quirks documented.
- `internal/core/{name,message}_test.go` (new) — ValidName property table (all-digit /
  leading-dot / leading-hyphen accepted; "."/".."/empty/separators rejected);
  lenient-decode matrix (number→string, null→"", key-order, missing fields, presence);
  marshal byte-shapes; /peers decode incl. the zero-time sentinel.
- `relay/cmd/cbus-relay/main.go` — adopted `core.ValidName` at both call sites
  (handleSend, handleTail); removed the private `nameRe`/`validName` and the now-unused
  `regexp` import. Name validation is now single-sourced (parity is a compile-time fact,
  not a hand-synced regex).
- `relay/deploy.sh` — reviewer note n1: added a guarded `rm -rf` of the pre-restructure
  `$DEST/src/cmd` and `cbus-relay.service`, which `rsync --delete`'s per-directory scope
  leaves stale on the NUC (and the stale cmd/ still compiles under the new module).
- `internal/core/frame.go` — reviewer note n2: qualified the `main.go:207/239/204`
  constant anchors as "(pre-extraction anchor)" so they don't read as live refs; the
  package doc's claim of types/wire-structs/validation is now accurate (they landed).

[Possible Ripple Effects]

- `relay/cmd/cbus-relay/reframe_test.go` remains byte-unchanged and green; the relay test
  package still passes (validName behavior is identical through core.ValidName).
- `core.SendReq`/`PeersEntry` are defined to MATCH the relay wire structs, but the relay
  still uses its own for now; the Phase 0 conformance rig (a later milestone) will
  cross-check them against the live binary, after which they can be single-sourced too.
- No wire / on-disk / behavior change; the relay's validation is byte-identical.

[Testing Notes]

- `go build ./...`, `go vet ./...`, `go test ./...` pass (core 0.360s, relay 0.548s).
- deploy.sh simulation re-run after the main.go change: native + `GOOS=linux
  GOARCH=amd64` builds both OK from the rsync'd self-contained layout.

## [2026-07-13 00:23:31 UTC] [Port/Go] Phase 0 (1/N) — root module promotion + shared framer extraction

[Attempt #1]

Phase 0 of the bash→Go port (port-map.md §6): stand up a shared Go package that both
the relay and the future Go client import, with no deployment and no behavior change.
This commit does the module restructure plus the highest-value shared-code move (the
framer). Assigned via the cbus `go-port` channel (coder role); the module layout was
ratified as Option A (single root module) by the orchestrator before restructuring.

[Files Changed]

- `go.mod` (moved from `relay/go.mod`) — module path `claudebus/relay` → `claudebus`.
  The module root moves to the repo root while the relay tree stays at `relay/`, so the
  relay's three imports (`claudebus/relay/internal/{spool,wire}`) resolve UNCHANGED.
  `internal/` now sits at the repo root, importable by both `relay/cmd/cbus-relay` and a
  future `cmd/cbus` client. Verified: only those 3 import sites reference the old path,
  and all stay valid.
- `internal/core/frame.go` (new, ~110 lines) — `Reframe` + `wrapBytes` + the measured
  constants (`MonitorLineCap=500`, `BodyWrap=440`, `WSFrameSafe=2800`), moved verbatim
  from main.go:202-252. Provenance comments carry the 2026-07-13 live re-measurement and
  the newly-observed harness behavior: truncation now emits an explicit `...(truncated)`
  marker at BOTH the per-line cap and the ~3000 notification ceiling (previously a silent
  cut on the local path). The header-less oversize-total quirk (protocol.md §4.4) is
  preserved verbatim. Documents the no-trailing-newline output contract.
- `relay/cmd/cbus-relay/main.go` — removed the local `reframe`/`wrapBytes`/`wsFrameSafe`;
  `reframe()` is now a one-line wrapper delegating to `core.Reframe`. Added the
  `claudebus/internal/core` import; dropped the now-unused `unicode/utf8` import (it was
  used only by `wrapBytes`). Net −45 lines.
- `relay/deploy.sh` — retargeted for the root-module layout: rsync now ships the repo
  root's `go.mod` + `internal/` + `relay/` (keeps the module self-contained on the NUC);
  build paths become `./relay/cmd/{cbus-relay,wstail}`; the systemd unit is copied from
  `src/relay/cbus-relay.service`.

[Possible Ripple Effects]

- `relay/cmd/cbus-relay/reframe_test.go` is intentionally UNCHANGED and still passes —
  it exercises the framer through the package-local `reframe` wrapper, which is the gate
  proving this is a pure refactor. `git diff` on that file is empty.
- The framer is now single-sourced: any future frame change is one edit in
  `internal/core`, and (Phase 4) the Go client shares it — frame parity becomes a
  compile-time property instead of two hand-synced implementations.
- deploy.sh is NOT exercised until the next real relay deploy (tracked separately, e.g.
  spool-GC). Its correctness here rests on the local simulation below; the first live
  deploy post-restructure is the real test.
- No wire, on-disk, or behavioral change: `core.Reframe` returns byte-identical output
  to the prior `reframe` (no trailing newline; the ws OpText frame is unchanged).

[Testing Notes]

- `go build ./...`, `go vet ./...`, `go test ./...` all pass; the relay test package is
  green (0.225s) with reframe_test.go byte-unchanged.
- deploy.sh simulation (orchestrator-required, so the reviewer can check the layout
  builds without a live deploy): rsync'd `go.mod` + `internal/` + `relay/` into a temp
  `src/` mirroring the NUC layout, then ran the exact deploy build commands against it.
  Native (`go build ./relay/cmd/cbus-relay` + wstail) → OK; NUC-target cross-compile
  (`GOOS=linux GOARCH=amd64`) → OK (ELF x86-64, statically linked).
- Go toolchain: none existed on the MBP (the relay was always built on the NUC via
  deploy.sh's ssh). Installed go1.26.5 via Homebrew (user-approved) for local iteration
  across the port.

## [2026-07-12 22:14:17 UTC] [Docs] Full behavioral audit → architecture docs + port map

[Attempt #1]

Complete functionality-and-behavior audit of the project at HEAD `f213e26`, run as a
31-agent workflow (4.1M tokens, 0 errors): five parallel subsystem mappers
(client surface, client internals, relay/cross-machine path, CC integration, design
intent + all 24 commits) → three completeness critics (client coverage,
relay/failure modes, docs-vs-code drift) over two rounds, finding and filling **56
coverage gaps** → two port analysts (module decomposition, unix-primitive
inventory) reconciled by a synthesis pass → six doc writers. Explicitly NOT a bug
hunt: quirks are documented with a preserve-or-rethink disposition, not fixed.

[Files Changed]

- `docs/architecture/overview.md` (new, 417 lines) — what cbus is and why (closed
  teammate mailbox research), component map + Mermaid architecture/sequence
  diagrams, cross-machine topology, security model (trust boundary, asymmetric
  CF Access auth, subprotocol bearer), design decisions with rationale, known
  limitations (delivery semantics truth table incl. the ~90-120 s sleep-window
  loss, no spool GC, no curl timeouts).
- `docs/architecture/command-reference.md` (new, 1152 lines) — every subcommand
  with args/flags/env/stdin/outputs/exit codes/error strings (both error
  dialects), address grammar, Monitor-arming contract, deprecated v1 aliases,
  slash commands, cc-branch.sh, install.sh, hook-exit flow.
- `docs/architecture/protocol.md` (new, 1012 lines) — the compatibility contract:
  `$CBUS_DIR` layout, meta.json/inbox.jsonl/marker/credential formats, atomicity
  idioms, framing grammar (440-byte wrap, wsFrameSafe=2800, framer divergence
  matrix), follower/replay semantics, liveness predicates, prune GC, presence
  protocol, relay HTTP API, RFC 6455 ws subset + displacement + keepalive,
  Maildir spool, constants/invariants checklist.
- `docs/architecture/port-map.md` (new, 481 lines) — why bash is outgrown, module
  decomposition (M1-M12), primitive→invariant→replacement inventory, bash-only
  workarounds a port deletes, contract classes A (frozen) / B (semantic) / C
  (free), phased migration (P0 shared core + golden framer tests → P1 remote
  verbs side-by-side → P2 local transport/follower per-machine cutover → P3
  post-homogenization semantics → P4 wire-touching relay work), Go
  recommendation (shared framer package makes frame parity compile-time).
- `~/dev-docs/projects/claudebus/{index,architecture,behavior-spec,port-map}.md`
  (new, canonical LLM tier) — dense, file:line-anchored mirror incl. the
  shipped-docs drift register and consolidated quirk registry.
- `simple_changelog.md`, `detailed_changelog.md` — this entry.

[Possible Ripple Effects]

- Pure documentation; no runtime behavior changed. No NUC propagation needed
  (nothing under `bin/` or `commands/` changed).
- The docs freeze today's measured harness constants (500 chars/line, ~3000/
  notification, ~200 ms batching → 440/2800) with provenance; if the Monitor
  changes, protocol.md §4 and the port plan's P0 re-measure step are the anchors.
- README/CHEATSHEET stale claims (e.g. residual `tail -F` requirement, missing
  presence/hook-exit) are catalogued in behavior-spec §12 rather than fixed —
  a follow-up doc pass can normalize the shipped docs against the register.

[Testing Notes]

- Spot-verified doc claims against source: CBUS_DIR default (bin/cbus:16),
  python3 gate (:22), host table (:140-143), BYTES=440 (:522), wsFrameSafe=2800
  (main.go:204), subprotocol prefix `bearer.cbus.` (main.go:28), listen addr
  127.0.0.1:8090 (main.go:391), endpoints /send /tail /peers /healthz
  (main.go:413-416), go 1.26 (go.mod) — all match.
- Critic round 2 converged (no new gaps after fill round 2 of 2).
- ~27 open questions unanswerable from code (CF Access config, live Monitor cap
  drift, NUC spool state, SessionEnd-on-/clear semantics) are listed in
  port-map.md §8 / index.md rather than guessed at.

[Attempt #2 — follow-up to the presence commit]

A second adversarial diff review by the Fable-5 peer (`dev@nuc/reviewer`) against
71348a7 found four real concurrency/robustness bugs and two cleanups. All fixed in
`bin/cbus`:

- **Reap temp escaped the namespace [reviewer #1].** `prune_channel`'s claim used a
  SIBLING temp `alias.reap.$$`, which the `*/` glob (here and in
  `broadcast_presence`) rescans as a peer — a concurrent prune could mv-claim it and
  broadcast a mangled alias, and a crash between mv/rm orphaned it as a permanent
  phantom. Now **dot-prefixed** (`$chdir/.reap.$$.<peer>`); `*/` skips dotdirs (same
  idiom as `.remote`), so a half-reaped/orphaned dir is inert.
- **mv-claim TOCTOU [reviewer #2].** `mv` claims whatever is at the path *now*, not
  what `peer_dead` checked: a concurrent explicit-alias join could `rm -rf`+`mkdir` a
  FRESH registration in the same slot, which the reaper would then mv out — silently
  unregistering a just-joined peer + a false `departed`. Now the reaper
  **re-verifies `peer_dead` on the moved dir** and, if it's alive, restores it
  (`mv` back) or drops the temp if the slot was reclaimed again.
- **rename-reclaim self-echo [reviewer #3].** `broadcast_presence` conflated the
  event *subject* (`from=`) with the *actor to skip*. Rename-reclaim broadcasts
  `from=new` while the actor still sits at `old`, so `old`'s still-armed stale tail
  popped the event on its own Monitor. Split into `<from>` + optional `<skip>`
  (defaults to `<from>`); rename-reclaim passes `skip=old`.
- **`set -e` append hazard [reviewer #4].** `printf >> inbox` aborts the whole
  command if the target dir vanished (concurrent prune/leave). In `cmd_leave` the
  broadcast runs *before* the `rm`, so an abort left the session registered. Guarded
  with `2>/dev/null || continue`.
- **Cleanups [reviewer #5,#6].** One `now()` per event (was per-recipient → one event
  carried differing timestamps); corrected the comment that overclaimed a "compact
  one-liner" render (the follower renders the normal frame with `kind=` in the header).

Filed **cbus-8no** for the pre-existing, presence-amplified rename mv→re-arm window
(the re-arm seeks END, dropping anything appended to the new inbox meanwhile)
[reviewer #7]. NUC propagation [reviewer #8] already done.

### [Testing Notes]

Isolated re-test: no `.reap` dirs leak (visible or hidden); `departed` fires exactly
once for a dead peer; rename-reclaim self-echo count = 0 for the actor, 1 for a
bystander. `bash -n` clean.

## [2026-07-12 20:04:18 UTC] [Core] Presence announcements — local (cbus-2r7)

[Attempt #1]

Peers came and went silently — you had to run `cbus list` to notice. This adds
join/leave/rename/departed broadcasts so a session's armed Monitor surfaces roster
changes live. App-agnostic by design (no tmux/iTerm2): the three voluntary events
are explicit cbus commands, and involuntary departure rides the existing
pid-liveness prune. Planned + reviewed adversarially by a Fable-5 peer
(`dev@nuc/reviewer`); all 10 findings folded in (see the plan file).

### [Files Changed] — all in `bin/cbus`

- **`broadcast_presence <channel> <self> <event> <text>`** (new helper): appends
  `{from,to,ts,kind:"presence",event,text}` to every peer in the channel except
  `self`. Targets on **`!peer_dead`** — the SAME rule `cmd_send` uses — NOT
  `meta_listener_alive` [reviewer #3]: a joined-but-unarmed peer (null listenerPid,
  in grace) accepts sends and replays its inbox on first arm, so it must receive
  presence too; a liveness-only broadcast would skip it forever.
- **`cmd_join`**: after the peer registers, `broadcast_presence … join`.
- **`cmd_leave`**: `broadcast_presence … leave` *before* `rm -rf` each registration.
- **`cmd_rename`**: a `rename` event (old→new) after the mv, AND a `departed` event
  when it reclaims a dead name-holder (`rm -rf "$newdir"`) [reviewer #2].
- **`cmd_unregister`** (the manual kick primitive): `departed` after removal
  [reviewer #2].
- **`prune_channel`**: reaps now use an **atomic `mv`-claim** — only the session
  whose `mv <dir> <dir>.reap.$$` wins removes + broadcasts `departed`, so two
  concurrent prunes announce a departure once, not twice [reviewer #8].
- **`cmd_bootstrap`**: dropped the manual `cbus send $parent "joined as X"` line —
  `join` now auto-broadcasts, so keeping it would fire two join events per
  branch/spawn [reviewer #9].

### [Possible Ripple Effects]

- Every join/leave/rename now writes a small presence line to each live peer's
  inbox. Negligible volume; renders as a compact `kind=presence` one-liner via the
  existing tail follower.
- **Remote (@host) presence does NOT work yet** [reviewer #1]: the relay's
  `sendReq`/`reframe` carry only from/to/ts/text, so `kind` is stripped on the wire.
  Local presence only; remote is filed as `cbus-ijx.5` (add `kind` to the relay).
- The NUC runs its own cbus copy for loopback bridge peers → must re-run
  `install.sh` there or NUC-local prune/presence diverges [reviewer #10].

### [Testing Notes]

- Isolated `CBUS_DIR`, five simulated sessions: confirmed bob (armed-equivalent)
  and **dave (joined, never armed)** both received join/rename/leave/departed
  events in order; self-announcements are skipped; a dead "zombie" peer produced
  exactly one `departed` when a later join triggered the prune. `bash -n` clean.

## [2026-07-12 05:51:32 UTC] [Relay] Remote ws-tail reframing — cross-machine parity with the local tail fix (cbus-mjz)

[Attempt #1]

The 2026-07-12 local fix reframed `cbus tail` so long messages beat the Monitor's
500-char line cap — but the REMOTE path (`<channel>@<host>`) was untouched: the
relay pushed each stored message as a single raw-JSON ws frame, so long
cross-machine messages still truncated at 500 with no local inbox to re-read
(the body lives in the relay's Maildir). This brings remote to parity.

### [Measured the ws truncation mechanics first]

The local fix relied on stdout specifics; ws frames differ, so I probed them with
a throwaway localhost ws server (pure-python, no prod risk) feeding a Monitor
`ws:` source three frames:
- single-line ~777 chars -> truncated at ~500 (per-line cap applies to ws too).
- MULTILINE ~900 chars, lines <450 -> arrived WHOLE. **A multiline ws frame is
  capped PER-LINE at 500, not 500 total.** This is the key that makes a
  server-side reframe work.
- MULTILINE ~2500 chars -> truncated mid-frame. Led to finding a SECOND,
  independent cap: a ~3000-char per-NOTIFICATION ceiling. Confirmed on the real
  local path (graduated CEIL probes): 2600/2800/2900 whole, 3000/3100 truncated
  => ~3000 total incl. framing. THIS CEILING IS SHARED BY BOTH LOCAL AND REMOTE
  (the local follower's wrapped lines batch into one notification too).

### [Files Changed]

- `relay/cmd/cbus-relay/main.go`:
  - New `reframe([]byte) []byte`: parses the stored `{from,to,ts,text}` and emits
    the same block the local follower does — `◀ cbus msg from=… to=… ts=…` header,
    body split on the text's newlines then `wrapBytes`-wrapped at 440 bytes, and a
    `◀ cbus end from=…` marker — joined by `\n` into ONE OpText frame. Non-JSON /
    text-less payloads pass through unchanged (defensive).
  - New `wrapBytes(string,int) []string`: rune-boundary byte-wrap (never splits a
    multibyte rune); empty -> `[""]`.
  - If the framed block would exceed `wsFrameSafe` (2800), the header gets a
    `⚠truncated~<N>B` suffix (N = full text byte length). The suffix is in the
    header, which is delivered first, so it survives the ~3000 cut — turning
    silent truncation into a visible signal on both over-ceiling paths.
  - `handleTail` delivery: `conn.WriteFrame(wire.OpText, reframe(payload))`.
  - Added `unicode/utf8` import.
- `relay/cmd/cbus-relay/reframe_test.go` (new): short/long-wrap/unicode-no-split/
  newlines-preserved/bad-json-passthrough/oversize-warns; asserts every emitted
  line <500 and that a long body reassembles to the original.
- `relay/deploy.sh`: **bug fix** — used `systemctl enable --now`, which only
  STARTS a stopped unit and is a no-op on an already-active one. Every deploy
  since the service first came up rebuilt the binary but kept the OLD process
  serving (caught live: the running pid had ~61h uptime right after a "successful"
  deploy). Now `enable` (boot persistence) + `restart` (load the new binary).
- Docs (`commands/bus-join.md`, `README.md`, `CHEATSHEET.md`): remote is no longer
  "not reframed" — now describes server-side reframe + the shared ~2800 ceiling
  and the `⚠truncated` notice.

### [Possible Ripple Effects]

- Remote ws consumers now receive a framed text block instead of raw JSON —
  identical change to the local path; `from=` is in the header for replies.
- The `~3000` per-notification ceiling is NEW knowledge that also bounds the
  already-shipped LOCAL fix: a single message whose framed form exceeds ~2800
  chars truncates on either path. Remote now warns in the header; the LOCAL
  follower does NOT yet emit that warning — tracked as a follow-up (chunked
  multi-notification delivery + local over-size warning).
- Relay restart drops live ws tails (1006); receivers re-arm per the sleep-rearm
  guidance. Done with no live remote peers.

### [Testing Notes]

- `go vet ./...` clean + `go test ./cmd/cbus-relay/` all pass on the NUC (isolated
  /tmp build) BEFORE deploying — deploy.sh aborts on build failure (set -e) so a
  bad build never reaches systemd.
- Live NUC->relay->MBP after the real restart: 2429-char message arrived framed
  and WHOLE (all 90 segments + REFRAMED2-END-OMEGA), vs the raw-JSON cut-at-500
  seen from the stale pre-restart binary. Health `ok`, new pid uptime ~1s.

## [2026-07-12 01:40:50 UTC] [Core] `cbus tail` reframes messages to beat the Monitor 500-char line cap

[Attempt #1]

Carlos kept seeing receivers narrate "New sub-task from orchestrator, truncated
again. Let me read the full message." — a wasteful double roundtrip. Root cause
(measured live, not assumed): the Claude Code **Monitor** tool truncates any
single stdout line at **exactly 500 characters**, and the old `cbus tail` piped
`tail -F` straight through, emitting **one JSON line per message**. The JSON
envelope alone (`{"from":…,"to":…,"ts":…,"text":"`) burns ~82 chars, so anything
past ~418 chars of text was cut, and the receiver had to re-read the inbox.

Key measured facts that shaped the fix:
- Cap is **per-line**, not per-notification: two >500-char lines each truncate
  independently at their own 500 boundary.
- The Monitor **batches lines emitted within ~200ms into a single notification**.
  => emit a long message as several short (<500-char) lines and it arrives whole,
  in one event, with nothing truncated.

### [Files Changed]

- `bin/cbus` — `cmd_tail` (local path only; `cmd_tail_remote`/ws untouched):
  - Replaced `exec tail -n "$from_line" -F "$inbox"` with an **`exec`'d python
    follower** (`exec "$PY" -c '…' "$inbox" "$from_line"`). `exec` preserves the
    single-process liveness model — the follower's argv contains the inbox path,
    so `meta_listener_alive`'s `ps -ww … | grep -qF "$inbox"` still recognizes it
    (verified: `cbus list` shows `listen` while armed, `off` after kill). No child
    `tail` process, so nothing to orphan on SIGKILL.
  - Follower behavior: reads the inbox line-by-line (accumulates partial lines
    until newline), reframes each JSON message into
    `◀ cbus msg from=<from> to=<to> ts=<ts> [kind=<kind>]` + body + `◀ cbus end
    from=<from>`. Body = the text split on its own newlines, each segment
    **byte-wrapped at 440 bytes** (safe under the 500 cap whether it counts bytes
    or chars; never splits a multibyte char). Non-JSON / non-dict / text-less
    lines pass through raw (defensive).
  - Lifecycle parity with the old `tail -F`: first arm replays from start (`+1`,
    since `join` truncated the inbox); a re-arm (listenerPid already set) follows
    from end (`0`). Reopens the inbox on **inode change or size shrink** to survive
    a rejoin's truncate or a `rename` dir move.
  - Locale-hardened: `sys.stdout.reconfigure(encoding="utf-8", errors="replace")`
    so a `C`/`POSIX`-locale host can't crash the listener on unicode message text;
    the `◀` marker is written via the ASCII `◀` escape so the `-c` **source**
    stays pure ASCII (no argv-decode/tokenize dependency on locale). Verified under
    `LC_ALL=POSIX PYTHONUTF8=0`.
- `commands/bus-join.md`, `README.md`, `CHEATSHEET.md` — replaced the "incoming
  message is a raw JSON line" contract with the framed-block format + the `from=`
  reply note; documented that remote `@host` ws tails are NOT reframed yet.

### [Possible Ripple Effects]

- **Any consumer that parsed the raw JSON event line** (agents, docs, muscle
  memory) now sees a framed block instead. Reply flow is unaffected — `from=` is
  in the header — but anything machine-parsing the event must adapt. All shipped
  docs/commands updated.
- **Remote channels unchanged**: `<ch>@<host>` tails arm a Monitor `ws:` source
  fed raw JSON frames by the relay; those still hit the 500-char cap for long
  messages. Flagged in docs; a relay-side reframe is a separate follow-up.
- **Delivery latency**: EOF poll sleeps 0.2s (was `tail -F`'s ~1s default) — if
  anything, snappier. Very large messages (many wrapped lines) rely on the
  Monitor batching them into one notification; a pathologically huge single
  message could still hit an unknown total-notification cap (no worse than before,
  where it truncated outright).

### [Testing Notes]

- Standalone follower harness: replay-from-start, live append, embedded newlines,
  unicode (`✓ é 你好`) intact, in-place truncate → reopen → continued delivery,
  longest emitted physical line = exactly 440 bytes. Re-ran under `LC_ALL=POSIX
  PYTHONUTF8=0` with empty stderr (no UnicodeEncodeError).
- Isolated end-to-end (`CBUS_DIR` sandbox): `join` → arm → `cbus list` = `listen`
  (liveness accepts the follower) → send short + 1500-char messages (both framed,
  long one wrapped) → kill → `cbus list` = `off`. `bash -n bin/cbus` clean; whole
  file re-verified pure-ASCII in the `-c` block.

## [2026-07-08 21:30:00 UTC] [Core] Session-scoped bridge identity — cbus-ny8

[Attempt #1]

Fixes the bug Carlos hit dogfooding unrelated sessions: the remote identity
marker was machine-global and ownerless, so (a) every session's `whoami`
reported every bridge on the machine, and (b) — the sharper edge — any
session sending to a bridged channel auto-filled the marker owner's alias,
impersonating it and misrouting replies to the wrong session's Monitor.

### [Files Changed]

- `bin/cbus`
  - Marker layout: `.remote/<host>/<channel>` (single file, machine-global) →
    `.remote/<host>/<channel>/<sessionId>` holding `{alias, ownerPid, ts}`.
    Per-session slots: two sessions can hold different aliases on the same
    remote channel; O(1) self-lookup via `marker_file`. Sessions without
    CLAUDE_CODE_SESSION_ID key on `nosession-$PPID`.
  - `cmd_tail_remote`: writes the session-scoped marker (owning claude pid via
    find_owner_pid, $PPID fallback); a legacy FILE at the channel path is
    removed and replaced by the dir (migration).
  - `cmd_send_remote`: from-fill reads only the caller's marker — an unrelated
    session falls back to the unroutable hostname-PID form instead of
    impersonating.
  - `cmd_whoami`: lists only this session's markers, worded
    "remote from-default — reachability: cbus list @<host>" (a marker never
    proved a Monitor was armed; the relay's /peers is the truth source).
  - `cmd_leave` (remote): removes only the caller's marker.
  - `cmd_prune`: new prune_remote_markers sweep — dead-ownerPid markers and
    legacy machine-global files (unowned by definition) are removed; empty
    dirs cleaned. No grace window needed: ownerPid is recorded at arm time,
    never null (unlike local listenerPid).
- `README.md`, `CHEATSHEET.md`, usage text: session-scoped wording; the
  marker documented as a from-default, not reachability.

### [Possible Ripple Effects]

- Relay untouched (client-only, like .3/.5).
- Existing legacy markers migrate lazily (next arm) or via `cbus prune`.
- whoami output format for remote entries changed (scripts parsing it would
  see the new wording).

### [Testing Notes]

- Multi-session core case: sid-A tails ch@site/alice, sid-B tails ch@site/bob
  → both markers coexist; whoami(A)=alice only, whoami(B)=bob only,
  whoami(C)=nothing remote; send from-fill A→alice, B→bob, C→hostname-PID
  fallback (no impersonation); A's leave removes only A's marker. ALL PASS.
- Migration: legacy file at channel path replaced by dir on next arm. PASS.
- Prune: dead-owner marker swept, live-owner kept, legacy file swept, empty
  dirs cleaned. PASS.
- Live: this session's real legacy marker (bridge@nuc) migrated by re-arming;
  whoami shows the new wording with the session-keyed file on disk. PASS.

## [2026-07-08 19:45:00 UTC] [Core] Review-hygiene fix set — cbus-foc.5 (closes the relay epic)

[Attempt #1]

The three findings from the .3 review (zen workflow self-audit), applied now
that rename-task's 485740e freed bin/cbus.

### [Files Changed]

- `bin/cbus`
  - `relay_auth_args` (eval-based argv assembly) replaced by
    `relay_auth_config`: auth headers are printed as a curl config and fed via
    `curl -K -` on stdin in `cmd_send_remote`/`cmd_list_remote` — bearer and
    CF Access credentials never appear in any process argv (`ps`-visible
    before). The refactor also deletes the eval outright (the nameref
    alternative would have broken macOS's bash 3.2).
  - `auth_put` (Darwin): Keychain writes go through `security -i` with the
    command on stdin instead of `-w <secret>` in argv.
  - `site_public_url`: the CBUS_SITE_<HOST>_URL variable name now maps every
    non-alphanumeric to `_`, so hosts with dots (allowed by valid_name)
    resolve instead of dying on bad substitution under set -eu.

### [Testing Notes]

- `security -i` round-trip (set/status/delete on a scratch site). PASS.
- Dotted host: CBUS_SITE_MY_HOST_URL resolved for host `my.host`
  (`ws://x.example:1/...` in the arm spec). PASS.
- Static check: no curl call site carries Authorization/CF-Access material in
  argv. PASS.
- Live public loop with the new path: `cbus list @nuc` through the CF front
  door, then a self-send (Keychain → config-on-stdin → CF Access → relay →
  public wss) arrived on this session's armed Monitor as a turn event. PASS.

## [2026-07-08 17:09:00 UTC] [CLI/Commands] `cbus rename` — legible local aliases

[Attempt #1] (scope confirmed with Carlos via AskUserQuestion: true rename over a
cosmetic label; feature #2 "auto-set the CC session title" dropped after research
showed the live TUI title is not externally settable — see below. Rebased onto
fork-2's 692af95, coordinated live over the cbus channel before touching the file.)

Auto-picked aliases (`main`, `fork-N`) make a busy channel hard to read once
several sessions join. `cbus rename` gives a session a meaningful local name.

### [Files Changed]

- `bin/cbus`
  - `cmd_rename <new-alias> [channel]` (inserted before `cmd_auth`, ~line 517):
    refuses `@host` targets up front (remote aliases are relay-side, out of
    scope per fork-2's identity-marker design); `valid_name` on both args;
    resolves this session's registration via `resolve_self`, selecting the one
    in `[channel]` or the sole membership — dies asking for a channel when the
    session is joined to more than one. No-ops with a message if new == old.
    Guards the destination: refuses if `meta_listener_alive` (won't clobber a
    live peer), else `rm -rf`s a stale/dead holder to reclaim the name. `mv`s
    `<ch>/<old>` → `<ch>/<new>` (inbox history + meta preserved), then `jset`
    rewrites `meta.alias`. Prints a re-arm reminder because the live `tail -F`
    still follows the OLD inbox path and goes stale after the mv.
  - Dispatch: `rename)` case added next to `leave`/`unregister`; `--help`
    usage line added under `cbus leave`.
- `commands/bus-rename.md` (new): `/bus-rename <new-alias> [channel]` slash
  command — runs `cbus rename`, then TaskStops the existing
  `cbus:<ch>/<old-alias>` Monitor and re-arms a persistent Monitor on
  `cbus tail <ch>/<new-alias>`. Notes the CC title stays a manual `/rename`.
- `install.sh`: installs `bus-rename.md`.
- `README.md`, `CHEATSHEET.md`: usage lines + command-file table row.

### [Design notes / why]

- Re-arm, not silent follow: `tail -F` follows by path, so `mv`ing the dir
  strands the old listener on a path that never reappears. A true rename
  therefore MUST re-arm the Monitor — trivial in the slash-command loop, which
  already owns the Monitor task.
- Inbox on rename follows-from-end: the re-arm sees `listenerPid` already set,
  so it uses `tail -n 0` (no whole-inbox replay → no duplicate old events).
  Trade-off: a message landing in the sub-second mv/re-arm gap isn't replayed;
  acceptable for a deliberate low-traffic op, re-send if it matters.
- Feature #2 (set the CC conversation title to the alias) was dropped after
  research: native command is `/rename` (no `/name`), interactive-only; the
  name lives in `<config>/sessions/<pid>.json` (`name`/`nameSource`) but the
  running TUI holds it in memory and ignores external writes (verified live —
  the value changed on disk, the prompt box did not). Only `claude -n <name>`
  at launch is programmatic. Left as a manual `/rename` nudge.

### [Possible Ripple Effects]

- `resolve_self` globs `$CBUS_DIR/*/*/meta.json`; `.remote/` markers are dot-dir
  and excluded, so rename never sees remote identities. Confirmed.
- A peer mid-`cbus send` to the old alias during the mv gap could hit a moved
  path; send resolves the target dir at call time, so it either lands in the
  old inbox (moved with the dir) or errors "no such peer" — no corruption.

### [Testing Notes]

Verified in an isolated `CBUS_DIR`: basic rename (dir moved, `meta.alias`
updated, inbox preserved, whoami/list reflect it); remote `@host` refusal;
same-name no-op; dead-peer name reclaim; multi-channel ambiguity error +
explicit-channel success; not-joined error; and refusal to clobber a peer held
open by a real `tail -F` (own registration left intact on refusal). `bash -n`
clean.

## [2026-07-08 17:05:00 UTC] [Core] Remote (relay-backed) channels in the cbus client — cbus-foc.3

[Attempt #1] (scope settled after two crossed-message rounds with the parent
session: explicit aliases, relay untouched — Carlos cut the registry/presence
build as over-engineering)

Client-only wiring that turns the .1 relay into usable cross-machine channels.
The relay binary is byte-identical to f64753e; everything below is bin/cbus.

### [Files Changed]

- `bin/cbus`
  - `split_remote` + routing hooks at the top of `cmd_send`/`cmd_tail`/
    `cmd_list`/`cmd_leave`: `<channel>@<host>/<alias>` addresses divert to the
    remote paths; local behavior untouched.
  - `cmd_auth` (`set`/`status`): credentials in macOS Keychain via security(1)
    (service `cbus-relay-<host>`, accounts token/cf-id/cf-secret) or 0600 files
    under ~/.config/cbus/ on Linux (NUC has no Keychain). `-` reads stdin;
    status prints masked last-4 only. No tokens in code — per the standing
    requirement; ssh-reading the NUC token file remains dev-only.
  - `relay_base`: zero-config front-door pick — probe 127.0.0.1:8090/healthz
    (300ms); reachable = loopback mode (no CF headers, ws://), else public
    (https://bus.example.com + CF-Access-Client-Id/Secret headers). Env
    overrides: CBUS_SITE_<HOST>_URL, CBUS_RELAY_LOCAL_URL.
  - `cmd_send_remote`: curl POST /send with bearer (+CF when public); `from`
    auto-fills from the channel's identity marker (routable), else
    hostname-PID fallback (documented unroutable, mirrors local unjoined).
  - `cmd_tail_remote`: prints the Monitor `{ws:}` arm spec (url + `bearer.
    cbus.<token>` subprotocol + description/persistent) instead of exec'ing a
    process, and records the identity marker ($CBUS_DIR/.remote/<host>/<ch>).
  - `cmd_list_remote`: thin read of the relay's existing /peers — connected/
    queued/lastSeen per peer; no server-side additions.
  - `cmd_leave` remote form drops the marker only (relay untouched, queued
    mail stays); `cmd_whoami` lists remote identities alongside local.
- `README.md`: "Using remote channels from cbus" section + CLI reference block.
- `CHEATSHEET.md`: cross-machine section.
- `commands/bus-listen.md`: remote-channel path (arm Monitor ws from the spec).

### [Possible Ripple Effects]

- Alias collisions are handled by convention + visibility (relay keeps one
  active tail per peer; displacement drops the loser's Monitor) — deliberate,
  reserve-on-join deferred.
- Identity markers are machine-level, last-arm-wins — two local sessions
  claiming different aliases on the same remote channel share the default
  `from` (override per send with --from).
- The Monitor arm spec necessarily prints the relay token (it IS the ws auth);
  transcripts are local, accepted trade-off.

### [Testing Notes]

- Offline: address validation (`..`, missing alias, unknown host), Keychain
  round-trip (set/status masked/delete), public-mode correctly demanding CF
  creds, arm-spec content, marker lifecycle (tail records → whoami shows →
  leave removes). All pass.
- Live (ssh -L 8090 = the documented dev/local mode): send queued pre-tail →
  Monitor armed from the printed spec → queued message REPLAYED as a turn
  event; live send delivered with marker-based routable from=probe@nuc/mbp;
  `cbus list @nuc` flipped off→listen and queued 1→0; cleanup verified.
- NOT yet tested: the public CF front door (needs cf-id/cf-secret seeded from
  1Password) — that is exactly the .4 acceptance round-trip, pending a live
  NUC-side session.

## [2026-07-07 16:00:00 UTC] [Relay] cbus-relay daemon on the NUC — networked leg of the bus

[Attempt #1]

First subtask of the networked-relay epic: a std-lib-only Go daemon that carries
bus messages across machines. Send = HTTP POST; receive = WebSocket consumed by
Claude Code's Monitor `{ws:}` source, so delivery stays turn-native with no
polling. Auth split per the endorsed design: bearer token on /send (CF Access
service token fronts it in prod), `Sec-WebSocket-Protocol: bearer.cbus.<token>`
on /tail (k8s-apiserver pattern; Monitor can't send custom headers).

### [Files Changed]

- `relay/go.mod`, `relay/internal/wire/ws.go` — hand-rolled minimal WebSocket:
  server upgrade (GET+version+key validation, subprotocol echo in the 101),
  client dial (handshake deadline, strict 101 parse, accept-key check), frame IO
  (masking-direction enforcement, RSV/opcode/control-size validation, 1MiB cap,
  per-write deadlines via Conn.WriteTimeout).
- `relay/internal/spool/spool.go` — Maildir store tmp→new→cur (atomic renames,
  enqueue-ordered names). Process-crash-safe; explicitly NOT fsync/power-loss
  durable (documented decision).
- `relay/cmd/cbus-relay/main.go` — /send, /tail, /peers, /healthz; presence hub;
  single-active-tail displacement with per-message done-checks and ENOENT-
  tolerant mark (dup-free handover); 30s ping / 90s pong-grace heartbeat;
  constant-time token compares; name validation (no `.`/`..`); 5s
  ReadHeaderTimeout.
- `relay/cmd/wstail/main.go` — test client mirroring Monitor's behavior.
- `relay/cbus-relay.service`, `relay/deploy.sh` — systemd unit (vuegraf
  template) + idempotent rsync/build/token/enable deploy.
- `README.md` — "Networked relay (NUC)" section.

### [Review]

zen codereview (gpt-5.5, high thinking): 1 HIGH (displacement/drain overlap
could duplicate delivery and kill the displacing tail on a mark race) — fixed +
regression-tested; 4 MEDIUM applied (write deadlines, masking enforcement,
frame validation, pong payload echo); 2 MEDIUM declined with rationale
(fsync power-loss durability — overkill for a session bus, documented instead;
seq-first spool names — breaks cross-restart ordering); LOWs applied
(version/key validation, ReadHeaderTimeout, Peers error propagation, wstail
strict status parse + handshake deadline).

### [Possible Ripple Effects]

- NUC gains a systemd service `cbus-relay` on loopback :8090 and a token file
  `/home/relay/cbus-relay/token` (0600). Not yet exposed via CF (epic .4).
- Message shape over the relay is identical to local inbox lines, so the .3
  client work needs no translation layer.
- At-least-once delivery: crash between WS write and new/→cur move redelivers.

### [Testing Notes]

On-NUC suite (loopback :8091, throwaway spool): healthz; 401 unauth; queue→new/;
name validation 400; wrong WS token refused pre-upgrade; subprotocol echoed;
in-order replay; new/→cur moves; live push while connected; presence
connect/disconnect flips; kill -9 → restart → queued message replayed; systemd
revive after SIGKILL (pid changed, service active). Displacement regression:
7 msgs across an A→B tail handover → 7 delivered, 7 unique, 0 leftover.
Mac-side contract test: ssh -L forward, real Monitor `{ws:}` with subprotocol
token armed in a live session, curl POST /send → message arrived as a Monitor
turn event. This is the exact .3 consumption path, proven before CF wiring.

## [2026-07-07 04:05:00 UTC] [CLI / Commands] `cbus branch` — collapse /bus-branch parent-side turns

[Attempt #1]

/bus-branch was slow: the parent side alone took 3-4 model turns (join, think,
fork, think, arm), each a full round-trip. The dominant child-boot cost
(`--fork-session` re-reads the whole parent transcript) is inherent to forking
and deliberately left alone — the user chose to slim the parent side only.

### [Files Changed]

- `bin/cbus`: new `cmd_branch [window|tab|tmux] [channel]` — derives the channel
  from the git repo basename when omitted (fallback `global`), joins
  idempotently, resolves the parent alias, and invokes the fork helper with
  `--prompt "$(cbus bootstrap <ch> <alias>)"`. Helper path defaults to
  `~/.claude/bin/cc-branch.sh`, overridable via `CC_BRANCH` (also the test
  seam). Dispatch + usage + env docs updated.
- `commands/bus-branch.md`: reduced to two tool calls — `cbus branch <target>
  [channel]`, then arm the Monitor. AskUserQuestion only when no target passed.
  `cc-branch.sh` (and its per-user absolute path) dropped from `allowed-tools`
  entirely — `Bash(cbus:*)` now covers the whole flow, which also resolves the
  review finding about the hardcoded path breaking other users.
- `README.md`, `CHEATSHEET.md`: `cbus branch` in CLI reference, fork-flow
  description updated, obsolete hardcoded-path caveat replaced with `CC_BRANCH`.

### [Possible Ripple Effects]

- Channel derivation moved from skill prose (model-executed) into shell — same
  rule (`basename $(git rev-parse --show-toplevel)`, sanitized), now
  deterministic and prompt-drift-proof.
- Skills installed to `~/.claude/commands` must be reinstalled for the slimmed
  /bus-branch to take effect.

### [Testing Notes]

- Stub helper via `CC_BRANCH` (no real windows): repo cwd derives channel
  `claudebus`; non-repo cwd falls back to `global`; explicit channel wins;
  idempotent re-branch keeps the same parent alias; bad target and missing
  helper produce clear errors; bootstrap prompt passed through intact. PASS.

## [2026-07-07 03:04:06 UTC] [Core / CLI / Commands] Review fix set — send gate, join atomicity, re-arm replay, bootstrap subcommand, fork-ordering revert

[Attempt #1] (fork-ordering is Attempt #2 — the first attempt, arm-after-fork,
was disproven empirically; see below)

Fixes from the high-effort code review of 4f62917 plus the landscape research
(inter-session-comms report), implemented by a forked session coordinating with
the parent over cbus itself.

### [Files Changed]

- `bin/cbus`
  - `cmd_send`: a peer whose `listenerPid` is null (joined, never armed) is now
    always accepted — its first `tail -n +1` arm replays the inbox, which is the
    documented design; previously `send` refused exactly the window the replay
    mechanism exists to cover, making the /bus-branch child announce racy. Only
    a dead ex-listener is refused (or `--force`d, now documented best-effort).
  - `cmd_join`: auto-picked aliases are claimed with a bare `mkdir` (atomic on
    POSIX) in a retry loop — two concurrent joins can no longer both pick `main`
    and have the loser truncate the winner's inbox. Explicit aliases keep the
    liveness check then recreate the dir.
  - `cmd_tail`: replays the whole inbox (`-n +1`) only on the FIRST arm
    (no prior `listenerPid`); a re-arm follows from the end (`-n 0`), so a
    Monitor restart no longer redelivers every past message. Trade-off: a
    `--force` line queued while the listener was dead is skipped by a re-arm —
    documented in help/README/CHEATSHEET rather than hidden.
  - `valid_name` rejects `.` and `..`; `split_target` validates both parts —
    closes `cbus unregister ../x`-style path escapes (self-inflicted only, but
    a one-line guard).
  - New `cmd_bootstrap <channel> [parent]`: prints the canonical fork-child
    bootstrap prompt (join, arm, announce, report-back, ignore the inherited
    "no completion record" note). Keeping the prompt in the binary stops it
    drifting from CLI behavior across skill-file copies.
- `commands/bus-branch.md`: reverted to **arm-before-fork**. The previous
  ordering (fork first, arm later) was built on a wrong model: `--fork-session`
  reads the parent transcript when the child *boots*, not when the fork helper
  runs, so a listening parent always leaks a "no completion record" note into
  the child — confirmed empirically (a child forked under the new ordering
  still showed the note). Arm-before-fork additionally closes the child-announce
  race from the parent side. The note is documented as cosmetic; the skill now
  says not to reorder steps to chase it. Fork step now uses
  `--prompt "$(cbus bootstrap <channel> <parent>)"`.
- `commands/bus-listen.md`: reply guidance warns that a `hostname-PID` sender
  (unjoined process) has no inbox — replies only work to `channel/alias` peers.
- `README.md`, `CHEATSHEET.md`: first-arm vs re-arm replay semantics, best-effort
  `--force`, bootstrap subcommand, corrected fork-ordering explanation.
- `simple_changelog.md`, `detailed_changelog.md`: this entry.

### [Possible Ripple Effects]

- Messages can now land in the inbox of a peer that never arms; they sit there
  until the peer arms or is pruned (10-min grace, then swept). Harmless — the
  dir is removed on prune.
- `--force` to a dead ex-listener changed from "delivered on next `-n +1` arm"
  to best-effort (a re-arm skips it, a re-join truncates it). The old guarantee
  was mostly illusory (re-join truncated anyway); docs now match reality.
- Skills installed to `~/.claude/commands` must be reinstalled (`install.sh`)
  for the bootstrap-based /bus-branch to take effect.

### [Testing Notes]

- `send` to joined-never-armed peer: accepted, line lands in inbox; dead
  ex-listener: refused without `--force`. PASS.
- 8 concurrent `cbus join race` → 8 distinct aliases (`main`, `fork-1..7`),
  no truncation. PASS.
- First arm replayed `msg-1 msg-2`; after kill + `--force` queue + re-arm, only
  a live `msg-4` was delivered (no redelivery of old messages; queued-while-dead
  line skipped, as documented). PASS.
- `..` rejected as channel and alias across `join`/`send`/`unregister`. PASS.
- `cbus bootstrap myrepo main` prints the canonical prompt. PASS.
- Live end-to-end: this fix set was implemented in a forked child session that
  claimed the work over the bus (`cbus send claudebus/main "taking the fix
  set…"`) and got a parent ack back — the coordination path being fixed was the
  one used to fix it.

## [2026-07-07 00:20:44 UTC] [Core / Liveness / CLI / Commands] Initial release — channel-based session bus with hardened liveness

[Attempt #1]

Initial commit of claudebus: a tiny file-based message bus that lets two or more
live Claude Code sessions talk to each other (e.g. a parent session and a forked
window), built entirely on supported primitives — the `Monitor` tool plus plain
files under `~/.claude-bus` (`CBUS_DIR`).

### Design highlights

- **Named channels.** Store layout is `<channel>/<alias>/{meta.json,inbox.jsonl}`.
  Peers are addressed as `channel/alias`; a bare `alias` in `send`/`tail` resolves
  within the sender's own channel(s). The channel name `global` is reserved by
  convention as the machine-wide bus for a master orchestrator. `/bus-listen` and
  `/bus-branch` default the channel to the current git repo's basename, so two
  sessions in the same repo find each other with no pairing step.
- **Auto-recycled aliases.** `cbus join <channel>` auto-picks `main`, then the
  lowest free `fork-N`; it is idempotent for a session already in the channel and
  prunes dead peers in that channel *before* picking, so alias numbers get reused
  instead of growing forever. A peer that joined but hasn't armed its Monitor yet
  (listenerPid null) gets a 10-minute grace window so auto-prune can't sweep a
  sibling mid-setup.
- **Hardened liveness.** `listenerPid` is the real `tail` process (via `exec`, so
  the recorded pid *is* the listener). Two failure modes are guarded:
  - *pid recycling* — a live pid is only trusted if its process args still
    reference this peer's inbox (`ps -ww -o args=` grep), so an unrelated process
    that inherited the number is not reported as a false `listen`.
  - *crash-orphaned listener* — on arm, `cbus tail` records `ownerPid`, the owning
    `claude` process (found by walking parents from `$PPID`). On a hard kill the
    `tail`/`zsh` subtree orphans together with a still-live pid, but `ownerPid` is
    gone, so the peer reads `off`. (A clean session exit stops the Monitor, which
    kills the tail directly — this covers the abnormal path.)
- **No lost messages during setup.** `join` truncates the inbox and the listener
  replays from line 1 (`tail -n +1 -F`), so anything sent between join and arming
  the Monitor is still delivered.

### [Files Changed]

- `bin/cbus` — the CLI. Subcommands: `join`, `tail`, `send` (`--from`/`--force`),
  `list [--active] [channel]`, `active`, `channels`, `whoami`, `inbox`, `prune`,
  `leave`, `unregister`; `register` kept as a deprecated v1 alias for
  `join global`. Helpers: `find_owner_pid` (walks parents to the `claude` pid) and
  `meta_listener_alive` (pid + inbox-args + ownerPid checks) centralize liveness
  for `list`, `channels`, `send`, `join`'s taken-check, and `prune`.
- `bin/cc-branch.sh` — session fork helper (relaunches through `ccs <profile>`
  when a CCS config dir is detected).
- `commands/bus-listen.md` — `/bus-listen [channel] [alias]`: join a channel and
  arm the Monitor listener.
- `commands/bus-branch.md` — `/bus-branch [window|tab|tmux] [channel]`: join the
  parent, fork the conversation, then arm the parent's Monitor. Ordering matters:
  the fork happens *before* arming so the child's transcript snapshot doesn't
  contain a live background-task record (which showed as a stale "no completion
  record was found" note in forked sessions under the old ordering).
- `install.sh` — copies (or `--link` symlinks) `cbus`, `cc-branch.sh`, and the two
  commands into `~/.local/bin` and `~/.claude/`.
- `README.md`, `CHEATSHEET.md` — docs for the channel model, liveness guarantees,
  and CLI.
- `.gitignore` — ignores runtime `.claude-bus/` state.

### [Possible Ripple Effects]

- v1 flat-registry stores (`<alias>/meta.json` directly under `CBUS_DIR`) are
  detected as "legacy v1 entry" by `list` and removed by `prune`; `register`
  still works as a `join global` alias for any callers using the old verb.
- Liveness now shells out to `ps` per peer during `list`/`channels`/`send` — a
  negligible cost at interactive scale, but not free if scripted in a tight loop.
- `ownerPid` detection depends on a `claude`-named ancestor process; if none is
  found (unusual launcher), it falls back to pid + inbox-args liveness only.

### [Testing Notes]

- Verified the listener process tree under a real Monitor: `tail → zsh → claude`;
  `ownerPid` correctly captured the real `claude` pid.
- pid-recycling case: pointed `listenerPid` at a live non-tail process → reported
  `off` (inbox args don't match).
- crash-orphan case: real tail alive + args match but `ownerPid` killed → `off`;
  `send` correctly refused with "not listening".
- Exercised join/idempotent re-join/explicit-alias-taken, multi-session channel,
  `active` filter, bare-alias vs full-address send, `--force` queueing, `prune`
  grace window + legacy-entry cleanup, dead-listener sweep on next join, and
  `leave`. Round-trip send delivered as a Monitor event end-to-end.
