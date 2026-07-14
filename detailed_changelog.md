# Changelog (detailed)

## [2026-07-14 06:22:09 UTC] [Docs] Redact prior-art-and-cc-internals.md (declassified aesthetic)

[Attempt #1]

Carlos-directed cosmetic redaction of docs/prior-art-and-cc-internals.md into a
"declassified document" look using █ (U+2588) full-block bars. (Black bars via
CSS/HTML don't survive GitHub's sanitizer, and a literal `<redacted>` is parsed
as an unknown HTML tag and stripped — so full-block chars are the portable trick.)

[Files Changed]
- docs/prior-art-and-cc-internals.md — §1 sibling project names / GitHub handles /
  star counts / version blacked (CCS deliberately KEPT: it is claudebus's own
  runtime, named openly in README + code + the §2/§5 `~/.ccs/` paths); §2 title
  kept, entire body blacked (SendMessage included); §3 & §4 a scattered "sprinkle"
  of redactions plus all six §4 decision-log commit-hash refs; §5 the internal
  bd-dashboard-bug bullet + both `scratchpad/` file paths (cbus-foc / cbus-oq9
  epic IDs left readable).

[Possible Ripple Effects]
- Style, not secrecy: the CC-internals findings are still stated openly in README
  ("Why not the built-in teammate mailbox") and overview.md, so the blackout hides
  nothing. The "see prior-art" pointers in overview.md / command-reference.md /
  port-map.md now lead to a redacted doc — accepted as-is per Carlos (aesthetic);
  a full docs reconciliation pass is planned in a separate session before the push.
- HISTORY: deferred to the pre-push quiesce window as op #4 of the filter-repo
  pass — a `--blob-callback` keyed on the doc's *enumerated blob-ID set* (via
  `git log --follow`, not a title signature, so an early differently-titled version
  cannot slip through unredacted) swaps every historical version for the redacted
  bytes.

[Testing Notes]
- Doc-only; no build/test impact. Verified: zero remaining occurrences of the
  targeted sibling names/handles, `SendMessage`, `bdx-xk1`, or `scratchpad/` paths
  in the tip; CCS-as-runtime references (README + §2 + §5) intact; §3 and §5 (`## 3.`,
  `## 5.`) section titles preserved.

## [2026-07-14 05:07:23 UTC] [CLI] branch/spawn children titled by ALIAS — parent-side reservation

[Attempt #1]

Follow-up superseding the channel-default half of the previous --name entry, per
Carlos's ruling ("the name should actually be set to the alias of the new session")
+ the orchestrator handoff spec (--name sets title AND bus alias, parent pre-picks).
The blocker was that a child self-picks its alias at join, after the --name flag is
already burned into the launch argv. Resolution: the PARENT claims the alias first.

[Files Changed]

- `internal/client/store.go` — new `ReserveAlias(ch, want)`: claims an alias for a
  not-yet-booted child. want="" runs the same atomic claimAlias mkdir dance as Join
  (sibling-excluding, collision-safe); explicit want mirrors explicit-join rules
  (ValidName, live-listener refusal, stale reclaim) plus an own-alias guard (the
  reclaim would eat the parent's registration). Writes a placeholder meta —
  sessionId "reserved", null pids, fresh lastActivity — that (a) the child's
  explicit `cbus join ch alias` reclaims (a reservation is never listener-alive),
  (b) PruneChannel spares for the 10-min unarmed grace, (c) dies via that sweep if
  the child never boots. No presence broadcast (the child's join announces). New
  `Unreserve` drops the placeholder when the terminal fork itself fails.
- `internal/client/bootstrap_prompt.go` — `bootstrapPromptAliased` +
  `BootstrapPromptAliased(ch, parent, child)`: join line becomes `cbus join $ch
  $alias` (reclaims the reservation), tail/description addresses concretized. The
  canonical auto-pick template stays byte-exact for `cbus bootstrap ch parent`.
- `internal/client/spawn.go` — `spawnPromptLocalAliased` / `spawnPromptRemoteAliased`
  + `SpawnPromptAliased`. Spawn returns (addr, childAlias): local always reserves
  (auto or --name) and titles = alias; remote with --name pre-assigns the relay
  alias in the prompt (no reservation — the relay has none); remote without keeps
  child-picks + address-as-title. Doc contract updated honestly: the spawning side
  no longer "mutates NOTHING" — it reserves.
- `internal/client/harness.go` — Branch reserves after the parent join, returns
  (ch, parentAlias, childAlias), forks with the aliased bootstrap + --name=child;
  Unreserve on fork failure.
- Validation shift: --name is an ALIAS now — core.ValidName + leading-dash reject
  (the free-text titles e353af2 allowed are gone; a space in --name errors).
- `cmd/cbus/main.go` — runBranch prints parent AND child addresses; runSpawn prints
  `addr/child` when fixed, the picks-its-own note when remote+unnamed;
  `cbus bootstrap <channel> [parent] [child-alias]` prints the aliased variant
  (print-only, no reservation).
- `cmd/cbus/usage.go` — branch/spawn/bootstrap help updated to the new semantics.
- `commands/bus-{branch,spawn}.md` — --name = alias+title, child address known up
  front (no more "announces its own alias"), remote-unnamed fallback documented.
- Tests: spawn_test.go largely rewritten (all local-spawn tests now need a temp
  CBUS_DIR — they write reservations), TestSpawnNameFixesAlias,
  TestSpawnAutoReservesAlias (main then fork-1, placeholder meta shape),
  TestBranchNameFixesAliasAndDefault (explicit join in prompt, own-alias refusal),
  TestSpawnPromptAliasedContent, TestReserveAliasReclaimAndUnreserve (join-over-
  reservation reclaim, Unreserve rmdir); harness_test asserts child≠parent and
  --name=child; flags_test bootstrap junk case shifted to arg 4.

[Possible Ripple Effects]

- `cbus list` shows a reservation as an unarmed peer (host/cwd accurate — the child
  inherits the parent's cwd) for the seconds until the child joins; sends to it
  before the join are DELETED by the child's truncate-at-join reclaim — same as any
  explicit-join reclaim, and peers shouldn't message before the join presence event.
- Two parents explicitly reserving the SAME --name can still race (second reclaims
  the first's reservation — explicit names are the caller's to manage, mirroring
  explicit-join semantics); the auto-pick path is mkdir-atomic and collision-safe.
- Behavior change: `--name "runner 2"` (free text) now errors — was legal for ~25
  minutes on e353af2; nothing shipped that used it.

[Testing Notes]

- `go test -race -count=1 ./...` green; gofmt clean. Smoke via scratchpad binary:
  aliased bootstrap prints the reclaiming join line; `--name "bad name"` rejected
  pre-fork. Live child-boot validation deferred: the DEPLOYED binaries stay pinned
  at 1a5821d until the quiesce window (rebuilding now would break @nuc for live
  sessions lacking CBUS_SITE_NUC_URL), so a real fork would exercise the old
  binary anyway. Validate child join-reclaim live after the window's redeploy.


## [2026-07-14 04:50:45 UTC] [Port/Go] Generalize relay-host resolution — drop the built-in `nuc`

[Attempt #1]

Part of the push-review / public-readiness work (cbus-kt3). To keep a personal
relay hostname out of the shipped code before the repo can go public, the built-in
host table is removed: every `@host` now resolves ONLY through its
`CBUS_SITE_<HOST>_URL` env override, and an unset override is a hard
`UnknownHostError` (previously `nuc` fell through to a hardcoded base).

First deliberate modification to bin/cbus since the cutover — the retired bash
client is the rollback artifact, and it now depends on the env var exactly like
the Go client, so a rollback also requires `CBUS_SITE_NUC_URL` to be set (already
provisioned on the MBP; the NUC is loopback-served and needs it only as insurance).

[Files Changed]
- internal/client/endpoint.go — `SiteURL` loses the `switch host { case "nuc" }`
  built-in; returns the env override or `UnknownHostError`. Doc comment updated.
- bin/cbus — `site_public_url` loses the `nuc)` case; falls straight to `die`.
  Comment updated ("no built-in hosts").
- internal/client/endpoint_test.go — `TestSiteURL` now asserts nuc-without-override
  is `UnknownHostError` (not a base) plus the override + unknown-host cases;
  `TestResolveFrontDoor` sets `CBUS_SITE_NUC_URL` so the public-mode branch resolves.
- docs/architecture/{command-reference,overview,protocol}.md — the "built-in table:
  only nuc" claims annotated as the since-retired port-verified behavior.
- README.md — new "cross-machine requires a relay / adding a machine" note; the
  address-form + endpoint-autodetect prose reworded to env-based resolution.
- (cmd/cbus/usage.go help line "no built-in hosts" already landed in e353af2, which
  shared the file with the --name hunks; this commit makes that text accurate.)

[Possible Ripple Effects]
- Any client that addressed `@nuc` via the built-in now REQUIRES
  `CBUS_SITE_NUC_URL` in its environment. Running sessions carry their start-time
  env (the var is unset there), so the LIVE binaries are intentionally NOT rebuilt
  yet — current builtin binaries (1a5821d) stay on both machines until the
  pre-push quiesce window, when rebuild+redeploy + a coordinated restart happen
  together.
- The relay host itself is unaffected (the loopback probe short-circuits before
  `SiteURL`, so it never needs the override).
- The bin/cbus rollback path now also needs the env var (documented above).

[Testing Notes]
- `go build ./...` clean; `go test ./...` green (client pkg incl. the reworked
  endpoint tests).
- The git-history scrub of the personal relay hostname to a placeholder, and the
  author-email rewrite, are DEFERRED to a single Carlos-scheduled `git-filter-repo`
  pass in the pre-push quiesce window; this commit only generalizes the tip.

## [2026-07-14 04:42:50 UTC] [CLI] `--name` on branch/spawn — children launch pre-titled

[Attempt #1]

Follow-on to `--model` (same shape, user-requested): `cbus branch` and `cbus spawn`
accept `--name <n>`, forwarded to the child launch as the CC CLI's `--name` flag
(v-current: "Set a display name for this session (picker, and terminal title)").
This is the programmatic `/rename` equivalent — verified against `claude --help`,
code.claude.com/docs/en/sessions.md, and the CC changelog before wiring (session
COLOR, by contrast, has no flag/SDK/persistence — interactive `/color` only, so it
cannot be automated). Removes the "child runs /rename by hand" step from bus flows.

Defaults ON: when `--name` is omitted, branch children title as the channel they
join, spawn children as the channel (local) or channel@host (remote). The child's
bus ALIAS is not known at fork time (it self-picks at join), so the address is the
most specific deterministic title the parent can stamp; explicit `--name` covers
role-titling (orchestrator spawning "tester2" etc.).

[Files Changed]

- `internal/client/harness.go` — `Branch` gains a name param (defaulted to ch after
  derivation); `forkLaunchArgv` inserts `--name <n>` after `--model`, before the
  prompt positional.
- `internal/client/spawn.go` — `Spawn`/`freshLaunchArgv` likewise (default: resolved
  addr, so remote children title as `ch@host`).
- Validation differs from --model deliberately: a name is free text (a display
  title — spaces legal, shQuote handles them through the launcher script), so only
  the flag-shaped leading-`-` trap is rejected pre-fork; no core.ValidName.
- `cmd/cbus/main.go` — `extractModel` generalized to `extractFlag(args, flag)` +
  `extractForkFlags` (pulls `--model` and `--name` in one call); usage strings
  extended on both verbs.
- `cmd/cbus/usage.go` — `--name` sub-lines under branch/spawn; header comment's
  post-bash-divergence list now reads "--model/--name flags".
- `commands/bus-{branch,spawn}.md` — argument-hints + body: pass `--name` when the
  user titles the child, note the channel-default otherwise.
- `internal/client/spawn_test.go` — TestSpawnNameFlagAndDefault (explicit name w/
  space, remote default = ch@host, bad-name rejection, prompt stays final
  positional), TestBranchNameFlagAndDefault (explicit, default = channel, bad-name);
  existing Spawn/Branch call sites updated for the new signatures.
- `internal/client/harness_test.go` — call-site signature updates only.

[Possible Ripple Effects]

- Every branch/spawn child now carries a title where before it had none — visible
  in the CC resume picker, prompt bar, and terminal tab. Cosmetic-only; no wire or
  state change. Multiple children on one channel share the default title (the alias
  isn't knowable at fork time) — distinguish via explicit `--name`.
- `ccs <profile>` passthrough assumed for `--name` exactly as already proven for
  `--model`/`--resume` (same argv tail).

[Testing Notes]

- `go test -race ./...` green (client + cmd suites; new tests exercise both verbs'
  argv placement + defaults). Smoke via scratchpad binary: `--name` missing value
  dies with usage, `--name -x` dies `bad name "-x"`, both pre-fork (no window).
  Full `--help` block renders both new sub-lines.
- Committed after Carlos released the hold (push-review was waiting on THIS
  commit; orchestrator notified with the hash on go-port@nuc). NUC propagation
  still pending: cross-compile + scp per the post-cutover doctrine, commands/
  copied by hand.


## [2026-07-14 04:05:15 UTC] [CLI] `--model` on branch/spawn — child sessions on a chosen model

[Attempt #1]

Small post-cutover feature (user-requested): `cbus branch` and `cbus spawn` accept
`--model <m>`, forwarded to the child launch as the CLI's `--model` flag so a fork
or fresh spawn starts on a specific model (sonnet / opus / fable today).

[Files Changed]

- `internal/client/harness.go` — `Branch` gains a model param; `forkLaunchArgv`
  inserts `--model <m>` after `--fork-session`, before the prompt positional.
- `internal/client/spawn.go` — `Spawn`/`freshLaunchArgv` likewise.
- Both validate the token shape only (`core.ValidName` + a leading-`-` rejection):
  the real model set is the CLI's to validate, so future models pass through — but a
  flag-shaped token would be swallowed as an option by the child CLI and die as an
  instant-close window (the P2.6 smoke lesson), so that shape is rejected pre-fork.
- `cmd/cbus/main.go` — `extractModel` pulls `--model <v>` from anywhere in the verb
  args (leading or trailing), then the existing positional parsing applies.
- `cmd/cbus/usage.go` — `--model` sub-lines under branch/spawn; header comment
  extends the fenced post-bash-divergence list.
- `commands/bus-{branch,spawn}.md` — skills pass `--model` when the user mentions a
  model; argument-hints updated.
- `internal/client/spawn_test.go` — model-in-argv placement, ccs+claude paths,
  flag-shaped rejection (both verbs); existing call sites updated for the new
  signatures.

[Possible Ripple Effects]

- None to wire/state — launch-argv construction only. Per-role model defaults
  (e.g. documenter→sonnet) deliberately deferred to cbus-vj9 role prompts.

[Testing Notes]

- `go test -race -count=1 ./...` green; gofmt clean. Deployed to MBP + NUC
  (cbus-go 1a5821d), skills scp'd to both `~/.claude/commands/`. Smoke: bad model
  `-bad` rejected with no window spawned.


## [2026-07-14 03:20:41 UTC] [Port/Go] New `cbus spawn` + `/bus-spawn` — fresh session joined to a channel

[Attempt #1]

First post-cutover feature addition: a Go-native verb implementing `cbus-ijx.2`
(peer lifecycle — spawn a blank peer, as distinct from `branch`'s fork). Ships
with its own slash command and is the first deliberate `--help` addition since
the port achieved bash byte-parity.

[Files Changed]

- `internal/client/spawn.go`, `spawn_test.go` (new) — `Spawn` launches a
  **blank** Claude Code session (no `--resume <sid>`, no `--fork-session` —
  unlike `Branch`, which continues this session's transcript) into a new
  terminal. The opening prompt carries everything the child needs to
  self-wire: for a local channel, join instructions + the Monitor-arm
  reminder; for a remote `channel@host`, the ws arm-spec instructions plus
  the 1006 re-arm doctrine baked directly into the prompt text (so a
  spawned remote peer starts already knowing the recovery procedure, not
  just the happy path). The spawning session itself does nothing beyond
  launching the terminal — no join, no arm, no bootstrap print on this
  side, since the child does all of that itself. Reuses `ForkSpec`,
  `TerminalForker`, and the iTerm2 launcher-shim mechanism (the same one
  P2.5-F1 fixed for the tokenizer issue) wholesale — no new terminal-launch
  code path. Local channel resolution: this session's own registration →
  git-repo-derived name → `global`. Remote `channel@host` must be explicit
  with no alias component — the spawned child picks its own alias, matching
  bash's existing remote-alias convention (aliases are always explicit
  remotely; there's no remote registry to auto-pick from). Validation
  (target/channel well-formedness) happens **before** the fork, so a bad
  argument fails fast without ever opening a terminal.
- `cmd/cbus/main.go` — wires the `spawn` verb.
- `cmd/cbus/usage.go` — `--help` now documents `spawn` — the first
  deliberate addition to the help text beyond matching bash's frozen
  strings (every prior help-text change was either byte-parity or a
  documented ruled delta; this is new surface bash never had).
- `commands/bus-spawn.md` (new) — the `/bus-spawn [window|tab|tmux]
  [channel|ch@host]` slash command: asks via `AskUserQuestion` only if the
  target is omitted, defaults the channel the same way the underlying verb
  does, and tells the model there is nothing to arm on the spawning side
  (the child self-arms) — verify membership afterward via `cbus list`.

[Testing Notes]

- 6 new unit tests (exact scope not itemized in the commit message beyond
  count).
- **Deployed**: both the MBP and NUC `cbus` binaries rebuilt at this
  commit; `commands/bus-spawn.md` copied to `~/.claude/commands/` on both
  machines.
- **Live-validated**: a spawned session joined and armed `claudebus/main`
  successfully on the first attempt.

[Possible Ripple Effects]

- None to existing verbs — `spawn` is additive; `branch`'s fork behavior
  and its tests are untouched.

## [2026-07-13 22:02:54 UTC] [Docs] Post-cutover documentation pass (17 files)

[Attempt #1]

Full documentation sweep following the 2026-07-13 cutover, applying a
change map drafted by a Fable 5 subagent (research + spec, not
implementation) against every doc that mentions the bash client. 8
files MUST-CHANGE, 5 BANNER-only (audit-era body preserved as the
verified contract the port was checked against), 4 NO-CHANGE. Applied
in dependency order: user-facing docs (README/CHEATSHEET/bus-join.md)
first, so the dev-docs status lines that claim "drift fixed" would
actually be true when written.

[Files Changed — repo, 4 commits]

- **`e9c6a84`** — README.md + CHEATSHEET.md. Bash→Go implementation
  statements (re-exec follower not python, `go build` install with no
  python3/`tail -F` dependency, native `TerminalForker` branch,
  `cbus --version`, hook-exit + presence subsections, 1 MiB message
  cap) AND 14 pre-existing stale claims catalogued in
  behavior-spec.md §12 fixed at the source rather than carried into
  the new text: the `tail -n +1 -F` mechanism reference, two
  "nothing is lost" overstatements (local ⚠truncated claim and the
  cross-machine relay-replay claim), the relay "epic in progress"
  line (shipped), the displacement "no duplicate delivery" overstate
  (at-least-once, narrow handover race), the `$CLAUDE_CODE_SESSION_ID`-only
  `from` resolution claim (full chain), missing presence/hook-exit
  documentation, and several CLI-reference wording gaps (`whoami`,
  `--force` remote scoping, `prune` marker sweep, env var list).
- **`d5d7c61`** — architecture set. `overview.md`: header banner +
  surgical edits to the component table, mermaid diagram, and
  environment-assumptions section describing the Go client, while
  leaving §1/§4/§5/Dogfooding/most of §6 as the historical rationale
  record (still accurate — the design decisions describe intent, not
  bash mechanics). `command-reference.md` and `protocol.md`: banner
  only, pointing at the port's 10-item intended-deltas list; bodies
  are the frozen behavioral contract the port was differentially
  verified against and stay untouched. `port-map.md`: status banner +
  `— DONE` stamps on the Phase 0/1/2 headings (Phase 2 additionally
  notes the cutover execution date).
- **`1ee6f46`** — `commands/bus-join.md` softens the "nothing is
  lost" overstatement (the relay replays mail queued while no tail
  was attached; the ~90-120s silent-drop window before that can still
  lose mail) — the "execs a follower that never exits" warning stays,
  since it's still literally true of the Go binary. Copied to
  `~/.claude/commands/` on the MBP and propagated to the NUC via `scp`
  by hand (checksums verified identical on both hosts) — explicitly
  NOT via `./install.sh`, which would silently roll back the cutover
  by reinstalling the bash client. `install.sh` itself: header comment
  and closing message rewritten to state plainly that running this
  script now IS the rollback procedure, with the rebuild command to
  undo an accidental run.
- **`485fe80`** — `cutover-decision-package.md` gets a status banner
  and its summary line's tense corrected (was "cutover is user-gated,
  nothing has been executed"; now reads as the record of a decision
  that was already acted on). `compat-deletion-plan.md` gets a status
  stamp: item 5 (self-id rename) done at cutover; items 1-4/6/7 remain
  until P3 fleet homogenization.

[Files Changed — dev-docs, direct edits, no git]

- `index.md`: cutover-executed note after the audit line; Key Facts
  table's Client/Client-install rows rewritten for the Go binary; Doc
  map table annotates behavior-spec.md as frozen/verification-contract,
  port-map.md as executed-through-P2, adds the two new cutover docs to
  the repo-human-docs list, and resolves the "docs lag code" line now
  that the repo fixes have landed; the liveness "five things" list
  item 4 gets a sysctl/procfs parenthetical; Status section rewritten
  from "cutover user-gated" to "cutover EXECUTED."
- `architecture.md`: post-cutover note under the header; §2 component
  table rows for Client CLI/Fork helper/Installer rewritten for the Go
  client; §3 topology diagram's follower annotation updated; §4 data
  flow steps 2 and the fork flow updated (re-exec wording, native
  TerminalForker); §8's copy-install-drift bullet rewritten; §9 commit
  timeline gets a final summary row for the whole go-port epic. §1,
  §5 (decision rationale — still accurate as history), §6 (security),
  §7 (delivery-semantics truth table — unchanged by the port) left
  untouched per the map.
- `behavior-spec.md`: banner declaring it FROZEN as the bash-era
  reference/verification contract, with the same 10-item delta list
  and a Go-side-equivalences paragraph (liveness via sysctl/procfs,
  in-process re-exec'd follower, shared `core.LocalEmit` framer). §12's
  heading annotated as resolved in this doc pass (applied only after
  the repo-side fixes in `e9c6a84`/`1ee6f46` landed, so the claim is
  true).
- `port-map.md`: status banner (phases 0-2 executed, cutover done,
  compat shims inventoried); Phase 2 heading's `— DONE` stamp extended
  with the cutover execution date.

[No-change files, per the map]

- `docs/prior-art-and-cc-internals.md` — historical research record,
  explicitly dated; its one bash-current-sounding line sits inside
  that dated narrative and is accurate for its date.
- `commands/bus-branch.md`, `commands/bus-rename.md` — no bash/
  cc-branch references; their "execs/is a follower that never exits"
  and rename-mechanism claims are still literally true of the Go
  client.

[Testing Notes]

- Every edit was verified against the actual file content before
  applying (line numbers in the source map had drifted slightly from
  a few of my own earlier same-session edits; matched by exact text
  instead). No map instruction contradicted the tree in a way that
  required stopping to flag back.
- `bus-join.md`'s propagation to both machines was checksum-verified
  (`md5`/`md5sum` identical on MBP and NUC) rather than assumed.

[Possible Ripple Effects]

- None functional — this is a documentation-only pass. The one
  behavior-adjacent artifact touched is the *installed* copy of the
  three slash-command skills (not the port itself), which now read
  the corrected `bus-join.md` on both machines.

## [2026-07-13 21:26:48 UTC] [Port/Go] Phase 2 (6/N) — cutover-readiness: installer, version stamp, help parity, decision docs — PHASE 2 CLOSED

[Attempt #1]

Sixth and final Phase 2 milestone: everything needed to make cutover a
human decision rather than a technical blocker — an installer, version
stamping, help/error parity, and the formal cutover-decision package —
plus review riders and the `/bus-branch` smoke-test saga that upgraded
the recommendation to unconditional. Phase 2 closes six-for-six
(P2.1-P2.6).

[Files Changed — 2b49b37, original submission]

- `install-cbus-go.sh` (new) — builds `cbus-go` with a `-ldflags`
  version stamp (`git describe`/hash), places it mode-agnostically
  (`rm -f` then `cp`, fixing the M12 port-map risk where a `cp` through
  an existing symlink would clobber the repo source instead of
  replacing the target), and **reports** (read-only, never writes) the
  current SessionEnd hook wiring state. Touches ONLY the `cbus-go`
  binary path — no bash `cbus` path, no `install.sh` retargeting, no
  `settings.json` writes of any kind.
- `cmd/cbus/main.go` — `main.version` stamped via `-ldflags`; new
  `cbus-go version`/`--version` verb (a readiness delta — bash `cbus`
  has no version verb at all).
- `cmd/cbus/usage.go` (new) — help/error parity (findings F-A/F-B,
  ruling "Option X"): ports bash's `--help` output byte-for-byte
  **except** the now-obsolete `CC_BRANCH` env var line (branch is
  native since P2.5, so that bash env-override line is a deliberate,
  ruled delta rather than a bug); unknown-command error matches bash's
  exact single-quote + `cbus --help` pointer form. `cbus-go` now
  self-identifies as `cbus` in its own help text, so cutover can be a
  pure binary swap at the filesystem level; the resulting coexistence
  oddity (running `cbus-go --help` directly still prints `cbus`) is
  named explicitly in the decision package rather than left as a
  surprise.
- `docs/architecture/cutover-decision-package.md` (new) — the GO
  recommendation, per-machine swap steps, a rollback procedure,
  "what stays bash" (answer: nothing), and the evidence bundle.
- `docs/architecture/compat-deletion-plan.md` (new) — enumerates the
  coexistence shims that get deleted once the fleet reaches Phase 3
  homogenization.
- **Evidence**: Class A/B differential sweep, 27/27, run on BOTH MBP
  (darwin/arm64) and the NUC (linux/amd64, via a temp binary — nothing
  installed there); rollback-safety checks all pass (bash correctly
  reads and operates on cbus-go-written state, including the D3
  `lastActivity` field); the installer was validated including the M12
  over-symlink case specifically. `go test -race -count=1 ./...`
  green. **Zero cutover was executed** — this commit is readiness
  work, not a switch.

[Files Changed — 2826de5, review riders]

- **R1**: greppable `// COMPAT(P3 #N)` markers added to every
  coexistence shim the deletion plan names, so
  `grep -rn 'COMPAT(P3' internal/ cmd/` mechanically enumerates all of
  them: #1 the re-exec (`follow.go`), #2 the raw inbox spelling
  (`follow.go`'s `compatInboxPath` + `liveness.go`'s
  `metaInboxNeedle`), #3 the mtime grace fallback (`liveness.go`), #4
  the `CBUS_PYTHON` help line (`usage.go`). Comment-only change — no
  behavior touched.
- **R2**: moved the P2.6 evidence from session-ephemeral scratchpad
  into `scripts/` under version control — `p26_sweep.sh` (the Class
  A/B sweep, every verb, 27/27) and `p26_rollback.sh` (the
  bash-reads-cbus-go-state check). The decision package's evidence
  bundle now points at these committed scripts (repo-relative, so they
  run from any checkout); the deletion plan documents the `COMPAT(P3`
  grep convention for future auditors. Both scripts re-run green from
  their committed location.

[Files Changed — 638c20b + 2aadf1a, the /bus-branch smoke saga]

- **Window leg** (a real `ccs` fork, not a probe): the terminal window
  stayed open with a live `ccs`→`claude` fork — PIDs captured and
  confirmed alive for ≥90 seconds, directly refuting an earlier
  "session ended instantly" observation. Exact launch command
  recorded; env correctly replicated (personal profile, cwd,
  `CLAUDE_CONFIG_DIR`); the launcher tmpfile self-deleted as designed.
- **Tmux leg**: `cc-branch`'s tmux window ran the same launcher,
  spawning the same kind of live fork; launch command and env
  replication confirmed identical via `ps -wwE`.
- **Diagnosis recorded**: the earlier "session ended instantly" result
  was a **fast-exit PROBE artifact** — a deliberately short-lived test
  invocation — not a defect in the branch/fork mechanism itself (a real
  `ccs` session stays open exactly as expected). Separately, an
  earlier-observed *slow child boot* was traced to the weight of
  **this session's own 200+-turn parent transcript** at fork time, not
  anything the port introduced — normal, shorter parent sessions boot
  their children fast. The live test forks were deliberately killed
  before their heavy boot completed, to avoid burning tokens
  needlessly.
- **Two findings recorded, neither blocking**: `ccs` was confirmed to
  be a real, working binary in this environment (not a stub or missing
  dependency); the launcher tmpfile lives under `$TMPDIR`, which is
  cross-process-readable on this machine — pinning it to `/tmp`
  specifically is filed as a Phase 3 hardening candidate, not a Phase 2
  blocker (the tmpfile already self-deletes immediately after use).
- **Child-boots-and-joins, "(d)" — the last smoke-gate leg** — was
  completed by Carlos directly, running `cbus-go branch` manually in
  his own normal-transcript sessions (both the window and tmux
  surfaces) and confirming that **both** the parent and the newly
  forked child appear in `cbus list` — the child genuinely boots,
  joins its channel, and registers, not just that a terminal window
  opened.
- **Recommendation upgraded to unconditional GO** on the strength of
  this evidence — the earlier caution around the fork mechanism is
  fully resolved.

[Whole-port invariant, restated at Phase 2 close]

- Across all 48 commits of the port so far (`4cac62f`..`2aadf1a`), the
  relay (`relay/cmd/cbus-relay`) has never been behaviorally modified
  — only refactored (the shared framer extraction in Phase 0, the
  `core.MaxMessageBytes` single-sourcing in Phase 2), with byte-parity
  to its pre-port behavior proven at every step. `reframe_test.go`
  remains byte-unchanged from the pre-port audit (HEAD `5e71ddc`)
  throughout the entire effort — the strongest evidence that the
  "relay stays, client gets ported" ground rule (port-map.md's opening
  premise) has held in practice, not just on paper.

[Possible Ripple Effects]

- None functional from this milestone's own changes — installer,
  version stamp, and help text are additive/cosmetic; the decision
  package and deletion plan are docs.
- Real ripple effect is organizational, not code: the project now has
  everything needed for Carlos to flip individual machines over to
  `cbus-go` whenever he chooses. Nothing in this milestone forces that
  decision or its timing.

## [2026-07-13 19:46:44 UTC] [Port/Go] Phase 2 (5/N) — harness layer + real flag parser (P2.5) — CLOSED

[Attempt #2]

The Claude Code harness integration layer (M10) plus a real CLI flag
parser (M9's frozen-verb-set surface). Closed after one confirmed
finding and its fix — a live iTerm2 launch regression traced back to a
mischaracterization in this project's own port-map.md, corrected
separately (`618d171`) before this fix landed. Phase 2 now stands at
5/6 milestones.

[Files Changed — original submission, f393158]

- `internal/client/harness.go`, `harness_test.go` (new) — four
  components:
  - **hook-exit**: `runHookExit` reads `{session_id}` off stdin (env
    fallback), leaves only this session's LOCAL registrations
    (broadcasting presence `left` per channel), and ALWAYS exits 0 — a
    SessionEnd hook must never fail the session regardless of what
    `leave` does internally. Remote `.remote` markers are left alone —
    the relay has no leave endpoint, so they only die via the ownerPid
    sweep, matching bash exactly.
  - **bootstrap**: `BootstrapPrompt` ships the canonical fork-child
    prompt embedded in the binary, extracted BYTE-EXACT from
    `bin/cbus:832` with provenance recorded in a comment (the
    anti-drift property port-map.md flags as worth keeping — a
    binary-embedded prompt can't drift out of sync with a
    hand-copied skill file). `$ch`/`$parent` substitution via
    `strings.NewReplacer`.
  - **branch**: `Branch` absorbs `cc-branch.sh` natively behind a
    pluggable `TerminalForker` interface. The essential function — ENV
    REPLICATION (`PATH`, `CLAUDE_CONFIG_DIR`, `cwd`) — is modeled as a
    `ForkSpec` and unit-testable via a fake forker with no real
    terminal involved. `OSAForker` drives iTerm2 (`osascript`) and
    tmux. **As originally submitted, this built the iTerm2 command
    with native Go quoting and removed bash's mktemp/`%q`
    self-deleting-launcher shim entirely** — this is the decision
    Finding F1 (below) reverses.
  - **flag parser**: `splitVerbArgs` replaces the ad-hoc per-verb
    parsing loops for `send` (both local and remote), with two ruled
    deltas over bash: a `--` terminator ends option parsing (so a
    message body may legitimately start with `-`), and `noExtra` makes
    trailing junk after a fixed-arity verb's arguments a hard error
    where bash silently discarded it. The frozen verb set and every
    output string are unchanged. `auth-set` deliberately keeps its
    existing ordered flag loop rather than moving to the map-based
    parser — ordered, repeatable flags with stdin side effects
    (`--token -` draining stdin) don't fit that shape cleanly.
- Explicitly **no cutover wiring** in this milestone — no
  `settings.json` hook registration, no `install.sh` retargeting.
  That's `P2.6`, and is user-gated (a human decides when to point their
  actual environment at the Go binary).

[Finding F1 — OSAForker iTerm2 tokenizer break, and its root cause]

- **What broke**: iTerm2's AppleScript `command` parameter is tokenized
  by **iTerm2 itself**, not by a shell — it does not honor POSIX-style
  quoting. The quoted one-liner the native-Go-quoting approach handed
  it (`/bin/bash -c '...'`) was mis-tokenized and launched nothing.
  Reviewer proved this live against real iTerm2, twice.
- **Why it happened**: this project's own `docs/architecture/port-map.md`
  §4 item 12 characterized the bash mktemp launcher shim as a
  bash-only workaround for "nested osascript quoting" that a port
  could simply delete by building argv natively. That characterization
  was **wrong** — the shim exists because of iTerm2's own tokenizer,
  not bash's quoting rules — and coder followed that doc's guidance in
  good faith when writing `f393158`.
  - This mischaracterization was independently caught and corrected in
    the docs (both `docs/architecture/port-map.md` here, commit
    `618d171`, and the dev-docs canonical copy) **before** this code fix
    landed — the doc correction and this code fix converged on the same
    true rationale from two directions (a documentation audit and a
    live reviewer probe) without one causing the other.

[Fix — d114688]

- Restores the launcher-script shim, natively: `osaForkITerm` writes a
  `0700` self-deleting launcher script (env exports + `cd` + `rm` self +
  `exec`, all POSIX-quoted) to a temp path, and hands iTerm2 a **bare**
  `/bin/bash <tmpfile>` command — bypassing iTerm2's tokenizer entirely
  by giving it nothing to mis-tokenize. `launcherScript` is a pure
  `(spec, scriptPath) -> script content` function, byte-for-byte
  testable without touching iTerm2.
- `tmux` is unchanged — it execs through `/bin/sh`, which honors
  POSIX quoting correctly, so it never had this problem.
- **A load-bearing comment records the true rationale and the probe
  reference directly in the code**, specifically so a future pass
  doesn't "clean up" the shim again based on the same wrong assumption
  that just caused this regression.

[Testing Notes]

- `f393158`: hook-exit (stdin/env precedence, stdin-beats-env, no-op
  cases, remote-marker survival), bootstrap substitution, branch env
  replication (ccs-profile and bare-claude launch, bad-target error)
  via a fake forker, shell/AppleScript quoting unit tests,
  `splitVerbArgs` (`--` handling, strict/non-strict, missing value),
  `noExtra` + end-to-end trailing-junk rejection + `send --` body
  parsing. Differential vs bash: bootstrap byte-identical across 4
  cases plus the trailing newline; hook-exit exit-0 + empty stdout +
  local-leave + remote-marker survival, matching bash exactly.
- `d114688`: `launcherScript` byte-exact test (injected path),
  `iterm2Command`'s bare-command shape, and
  `TestLauncherScriptExecutes` — actually runs the generated script via
  `/bin/bash <tmpfile>` and proves PATH/CLAUDE_CONFIG_DIR/cwd
  replication, self-delete, and the final exec all happen correctly
  end-to-end. The iTerm2-tokenizer leg specifically is reviewer's live
  probe harness (not reproducible in a hermetic unit test, since it
  requires the real iTerm2 application).
- **Reviewer re-verification against real iTerm2**: a "nasty-spec"
  probe (deliberately awkward env values / paths chosen to stress the
  quoting) confirmed the generated launcher script's content byte-exact
  and confirmed the self-delete behavior actually occurs.
- `go test -race -count=1 ./...` green on both commits.

[Possible Ripple Effects]

- None beyond the fix's scope — `tmux` path untouched; only the iTerm2
  `OSAForker` backend changed.
- The load-bearing comment is the primary safeguard against
  regression; there is no automated test that can fail if a future
  editor removes the shim again believing it to be dead code (short of
  the reviewer's live-iTerm2 probe, which is not part of CI).

## [2026-07-13 19:12:32 UTC] [Port/Go] P2.4-F1 fix confirmed — P2.4 closed fully

[Attempt #1]

Closes the P2.4 verdict addendum's finding (path-spelling compat under a
non-default CBUS_DIR). P2.4 is now fully closed; Phase 2 stands at 4/6
milestones complete.

[Files Changed]

- `internal/client/follow.go` — new `compatInboxPath(dir, ch, al string)
  string` does bash-verbatim raw concatenation (no `filepath.Clean`/`Join`
  normalization), reproducing exactly what bash's `inbox_path()` produces
  for any given `$CBUS_DIR` spelling. `InboxPath` (the argv-facing value)
  now routes through it.
- `internal/client/liveness.go` — new `metaInboxNeedle`, which rebuilds the
  liveness needle `argvContains` searches for from the raw `$CBUS_DIR` plus
  the peer's subpath — the previous version built the needle from a
  cleaned path, so it silently diverged from a Go follower's raw-spelled
  argv under any non-default CBUS_DIR spelling. Handles both legacy v1 and
  v2 layouts.
- `internal/client/follow_test.go` — extended the existing byte-equal pin
  test with a non-clean-spelling case (a trailing-slash `$CBUS_DIR`),
  asserting `argv == needle == bash-verbatim` all three ways. Also adds
  the ruled `TestFollowDirDeletionNeverExits`: deleting the whole alias
  directory out from under a running follower must not make it self-exit
  — it keeps polling and reopens cleanly once the directory is recreated
  (never-self-exit is a load-bearing contract, port-map.md §4 delete-list
  item 10's inverse — this pins it under a harsher-than-normal deletion
  case than plain file rotation).

[Root cause, restated precisely]

`filepath.Join` **cleans** — it collapses a trailing slash, so under
`CBUS_DIR=/tmp/bus/` the Go follower's `--inbox` argv read
`/tmp/bus/ch/al/inbox.jsonl` (single slash) while bash's `meta_listener_alive`
built its search needle as `/tmp/bus//ch/al/inbox.jsonl` (double slash,
preserving the trailing slash bash actually had configured) — a literal
string mismatch. Direction matters here: bash read a **live** Go follower
as `off` (and would have pruned it as dead), and symmetrically Go read a
live bash follower as `off`. Both directions were broken, not just one.

[Possible Ripple Effects]

- None beyond the fix's scope — `compatInboxPath`/`metaInboxNeedle` only
  affect the two named cross-process compat surfaces; normal file opens
  still use clean paths throughout.
- This closes out the port-map.md §3 row-19 regression class for the
  argv-compat surface specifically; the general risk (any future code that
  builds a path for cross-process string-comparison rather than for a
  syscall) remains something to watch for elsewhere in the port.

[Testing Notes]

- `go test -race -count=1 ./...` green.
- **LIVE**: all four bash↔Go liveness directions (bash reads Go, Go reads
  bash, in both the "already correct default spelling" and the
  "trailing-slash CBUS_DIR" configurations) read `alive` correctly.

## [2026-07-13 19:05:02 UTC] [Port/Go] P2.4 verdict addendum — APPROVED with one confirmed finding

[Attempt #1]

Verdict addendum only — the P2.4 entry below (`a7d2a4f`, recorded in
`87d97f9`) stands as written; this documents the review outcome and one
confirmed finding rather than restating the milestone.

[Verdict]

- **APPROVED**, with one confirmed finding (below). The finding does not
  invalidate anything already recorded — it's a coexistence edge case, not
  a defect in the core LocalEmit/ArmLocalTail work.
- Additional verification beyond `87d97f9`'s own testing notes: a
  1574-input fuzz pass over the framer/path-handling surface, green apart
  from the finding.

[Finding — path-spelling compat under a non-default CBUS_DIR]

- Go's `filepath.Join` (used to build `InboxPath`) **cleans** the path —
  collapsing double slashes, trailing slashes, and `.` segments. Bash's
  `inbox_path()` does raw string concatenation and preserves whatever
  spelling `$CBUS_DIR` happens to have.
- Under the **default** CBUS_DIR spelling (no trailing slash, no doubled
  separators), both produce the same bytes — today's live differentials
  and P2.4's own testing all ran under this default, which is why they're
  unaffected and stand as recorded.
- Under a **non-default** spelling (e.g. a trailing slash on `$CBUS_DIR`),
  the two followers would record/search for byte-different argv strings,
  breaking bash↔Go cross-liveness (`meta_listener_alive`'s argv grep) for
  that session — a listener that's actually alive would read `off` to the
  other client.
- This is the **pre-registered "row-19 class"** of port regression
  (port-map.md §3, row 19: "Inbox path deliberately kept in listener
  argv" — flagged risk: *"The subtlest port regression: everything demos
  fine single-session, then every armed listener reads 'off', send
  refuses everything without `--force`, prune reaps live peers"*). It was
  caught here specifically because the reviewer's probe used a
  **non-default** CBUS_DIR spelling rather than the default one every
  other test in this phase has used — exactly the kind of input that class
  of regression hides from.

[Ruling]

- Add `compatInboxPath`, which reproduces bash's raw-concatenation
  spelling **verbatim** (no `filepath.Clean`/`filepath.Join` normalization).
- Use it **only** on the two cross-process compat surfaces: the value
  written into the follower's `--inbox` argv, and the needle
  `argvContains`/liveness matching searches for. Normal internal file I/O
  (opening the inbox, writing to it, etc.) keeps using the clean,
  normalized path — there's no reason to denormalize anything except the
  two surfaces bash-era tooling actually string-compares against.
- Rides a post-verdict commit, together with a whole-directory regression
  test exercising a non-default CBUS_DIR spelling end-to-end (join, arm,
  cross-liveness check) to pin the fix and prevent regression.

## [2026-07-13 18:53:12 UTC] [Port/Go] Phase 2 (4/N) — in-process blocking follower (local `cbus tail`)

[Attempt #1]

The core of the local transport: `cbus tail <channel>/<alias>` (LOCAL, no `@`)
becomes a blocking Monitor follower, streaming framed inbox events to stdout. The
remote tail (`@host`, print-only, P1.5) is untouched. Two halves: a new `core`
framer for local rendering (keeps `kind`), and the client-side arm + follower loop.

[Files Changed]

- `internal/core/frame.go` — extracted shared framer internals so `Reframe`
  (relay) and the new `LocalEmit` (local) have ONE wrap/marker copy:
  `markerMsg`/`markerEnd` constants, `frameHead(from,to,ts)`, and
  `frameBody(text,from)` (the wrap loop + end marker). `Reframe` refactored onto
  them with its bytes UNCHANGED — the header-placeholder oversize quirk and the
  no-trailing-newline contract are preserved verbatim (golden + m3 matrix stay
  green, unedited). New `LocalEmit(payload)`: the local follower's framer. On the
  parity domain (all fields present as strings, non-empty text, no kind) it is
  byte-exact vs the lifted bash `emit()` and equals `Reframe(line)+"\n"`. It
  differs in exactly two byte-visible ways — it KEEPS `kind=` in the header
  (presence is displayed locally; the relay drops it) and appends the single `\n`
  stdout terminator that is part of every local write. No oversize `⚠` warning
  (relay-only). Degenerates follow ruling D6, deliberately NOT the bash follower
  verbatim: a non-JSON payload, any non-string field, or empty/null/missing text
  passes THROUGH plus its terminator (fixing bash's None-leak on null text), and a
  framed line missing from/to renders `?` so the local column is never blank
  (`*string` fields distinguish absent from present; a non-string field
  unmarshal-errors into the same strict passthrough gate as `Reframe`).
- `internal/core/local_frame_test.go` (new) — `TestLocalEmitGoldenParity`
  (parity domain, reuses `corpus.golden` — proves LocalEmit == lifted bash emit()
  there), `TestLocalEmitPresenceGolden` (kind=presence class, its own
  `corpus_local.golden`), `TestLocalEmitParityEqualsReframe` (shared-internals
  lockstep on the parity domain), `TestLocalEmitPresenceCrossParse` (D8 local
  rider: a canonical-Go presence line and its spaced python-form frame
  identically — LocalEmit frames the PARSED values, not the raw bytes), and
  `TestLocalEmitDivergenceMatrix` (D6 degenerate column, citing D6, with the
  explicit assertion that the missing-from/to `?` case DIVERGES from `Reframe`'s
  blank column).
- `internal/core/testdata/{gen_corpus_local.py,corpus_local.jsonl,corpus_local.golden}`
  (new) — presence(kind)-bearing inbox lines + their golden, generated by the same
  lifted `emit_golden.py`. NO degenerate rows (they would fail parity by design —
  the lifted emit() carries bash's None-leak).
- `internal/client/follow.go` (new) — `InboxPath(ch,al)` = `$CBUS_DIR/ch/al/inbox.jsonl`,
  byte-equal to bash `inbox_path()` for the same CBUS_DIR spelling (no
  EvalSymlinks/Abs — the exact string is the Decision 2 compat surface and must
  also equal the needle `MetaListenerAlive` builds). `ReplayMode`
  (ReplayFromStart/ReplaySeekEnd) with bash-identical wire values `+1`/`0`.
  `TailArgv`/`ParseTailFollower` (the hidden `--inbox`/`--from` follower argv).
  `ArmLocalTail`: resolve target (bare alias → own channel via `FindPeerChannel`),
  require inbox to exist (`join first`), read the tri-state replay decision from
  the meta's listenerPid BEFORE overwriting it, `armMeta` (best-effort temp+rename:
  listenerPid=own pid, ownerPid=owning session or null, lastActivity refreshed per
  D3, all other fields verbatim), then `syscall.Exec(self, TailArgv(...),
  os.Environ())` — a TRUE image replace (same pid). `RunFollower`/`follow`: open +
  optional seek-EOF, then a `pend`-to-newline read loop with a 0.2s poll and the
  dev+ino+size-regression rotation triple → `reopenUntilSuccess` (reopen from byte
  0). `follow` takes injectable out/poll/stop seams so the loop is unit-tested in
  process; production passes os.Stdout, 200ms, nil (never stops).
- `internal/client/follow_test.go` (new) — Decision 2 (InboxPath byte-equality,
  argv `--inbox` substring, replay wire), the rotation predicate + reopen-retry,
  the follower loop (first-arm replay-from-0, re-arm seek-EOF, truncate reopen,
  rm+recreate reopen, never-self-exit on vanish), and armMeta (records
  listenerPid/ownerPid/lastActivity preserving other fields; best-effort no-op on a
  missing meta).
- `cmd/cbus/main.go` — `runTail` dispatches: a `tail` carrying `--inbox` is the
  re-exec'd follower (run `RunFollower`, never returns); else remote (`@`) → the
  print-only spec; else local → `ArmLocalTail` (exec-replaces on success).

[Decision 2 — the re-exec crux]

The follower must carry the resolved inbox path in its argv so bash-era liveness
(`meta_listener_alive` greps `ps -o args=` for the inbox) recognizes this Go
follower during coexistence, regardless of the binary name (`cbus-go`). A TRUE
`syscall.Exec` (image replace, not a child fork) keeps the pid, so the
`listenerPid` recorded at arm IS the follower — no re-record, no window. Hard
rules honored: `os.Environ()` passed explicitly (a dropped CBUS_DIR would break the
re-exec); the `--inbox` value byte-equals bash's `inbox_path()` (no
EvalSymlinks/Abs); `// COMPAT`-tagged (the whole re-exec dies with P3 structural
liveness). The compat surface is the path SUBSTRING — flag name/position are free
but kept stable.

[Possible Ripple Effects]

- `Reframe` is byte-untouched — the refactor onto shared helpers preserves its
  output exactly (the oversize-quirk and no-trailing-`\n` contracts). The relay
  path and its conformance matrix are unaffected.
- The follower NEVER self-exits: a permanently-vanished inbox polls forever, and a
  reopen retries until success. The Monitor tool is responsible for killing it
  (matches the bash follower's "exec → the Monitor's kill flips liveness off").
- The re-exec depends on `os.Executable()` resolving; on failure the arm dies
  before exec (nothing recorded that would strand a dead listener beyond the grace
  window).

[Testing Notes]

- `go test -race -count=1 ./...` green (both platforms; darwin verified here).
- LIVE differential vs bash `cbus tail` (hermetic CBUS_DIR, bounded capture — the
  ONE sanctioned Bash use of the local tail): go follower stdout BYTE-IDENTICAL to
  the bash follower across first-arm replay, live append, truncate-in-place
  (size-regression reopen), and rm+recreate (new-inode reopen), including a
  `kind=presence` line — 2112 bytes, 9 frames, `diff` empty.
- Same-pid across the exec: the launched `cbus-go tail` pid == the armed
  `meta.json` listenerPid (the arm records `os.Getpid()`; `syscall.Exec` keeps it).
- `os.Environ()` passthrough: the follower's process env (via `ps -wwE`) carries
  both CBUS_DIR and a sentinel var set only in the launcher's env.
- bash↔Go cross-liveness: `cbus list --active` (bash) shows the Go follower as
  `listen`, and `cbus-go list --active` shows the bash follower as `listen` — the
  argv compat surface works in both directions.

## [2026-07-13 18:15:17 UTC] [Port/Go] Relay adopts core.MaxMessageBytes; coder session handoff

[Attempt #1]

Small follow-on closing the P2.3 anti-drift rider, plus a session handoff
note for continuity.

[Files Changed]

- `relay/cmd/cbus-relay/main.go` — `handleSend` previously hardcoded
  `http.MaxBytesReader(w, r.Body, 1<<20)`; now uses
  `core.MaxMessageBytes`, so the local client's send-size limit and the
  relay's `/send` body cap are single-sourced onto one constant instead of
  two independently-written `1<<20` literals that happened to agree. Pure
  refactor — the value is unchanged (1 MiB); the conformance rig was
  re-run green and `reframe_test.go` is untouched.
- `internal/core/message.go` — refreshed the doc comment on
  `MaxMessageBytes`, which previously pointed at the relay's now-stale
  hardcoded-literal anchor (`main.go:163`); it now describes the adopted
  constant directly.

[Possible Ripple Effects]

- None — behavior-preserving single-sourcing, closing the drift risk
  flagged in P2.3's review rather than introducing a new one.

[Session handoff]

- The coder session hit a context-limit checkpoint after 21 commits in
  this effort (`4cac62f`..`82fd4ac`). The P2.4 spec — including the
  `LocalEmit`-keeps-`kind` note (local delivery must preserve the `kind=`
  header for presence lines it emits, matching the D8-extension-to-presence
  ruling from P2.2) — is preserved in project memory so a successor coder
  session can pick up P2.4 without re-deriving context.

Commit: `82fd4ac`.

## [2026-07-13 18:10:49 UTC] [Port/Go] Phase 2 (2/N) — PeerStore mutations + presence (P2.2) — CLOSED after a concurrent-join fix

[Attempt #2]

Second Phase 2 milestone: local transport mutations (join/leave/rename/
unregister/prune) and presence broadcasts, held after initial review found
a concurrency regression in auto-pick join; now confirmed closed with the
fix in place.

[Files Changed]

- `internal/client/store.go`, `store_test.go` — `Join` (bare-mkdir EEXIST
  claim with a unified pick+claim, inbox truncate-at-join, meta
  temp+rename, dual-write `lastActivity` per ruling D3), `Leave`, `Rename`
  (same-parent `mv` + `meta.alias` rewrite preserving raw pids; alias is
  always stored as a string, dropping bash `jset`'s digit-coercion —
  port-map row 16's ruled Class-C delta), `Unregister`, `PruneChannel`
  (dot-prefixed same-parent rename reap + 3-way re-verify + rmdir),
  `PruneRemoteMarkers`, `Prune`, `LeaveRemote`.
- `internal/client/presence.go` (new) — `BroadcastPresence`
  (join/leave/rename/departed) with the `!PeerDead` recipient filter,
  from-subject vs skip-actor split, once-only `departed` via the reap
  mv-claim; `O_APPEND` single-write appends. Presence lines are
  canonical-Go (D8) — the follower frames them identically to bash's, so
  delivered events are byte-identical even though the raw stored line is
  compact rather than matching bash's shape byte-for-byte. This is the
  D8-extension-to-presence ruling.
- `cmd/cbus/main.go` — wires `join`/`register`/`leave`/`rename`/
  `unregister`/`prune`.

[Testing Notes — original submission]

- Unit tests: join create/truncate/dual-write/auto-pick/reclaim, presence
  join broadcast + skip-self, prune reap + departed, rename, unregister,
  leave.
- Live differential vs bash cbus on a shared `$CBUS_DIR`: `meta.json`
  byte-identical modulo the D3 `lastActivity` line; bash reads Go-written
  metas; bash presence lands correctly in Go peer inboxes; Go's `prune`
  correctly reaps a bash-written dead peer.

[Finding F1 — concurrent auto-pick join loses claims]

- The auto-pick claim loop burned all 50 retries inside a sibling's
  `mkdir`→meta window. Bash's accidental protection here was never
  designed-in: its subshell-per-attempt pick loop takes ~milliseconds per
  try, which happens to act as a backoff; the Go loop runs in microseconds
  and has no such accidental delay. Reviewer reproduced 8 concurrent joins
  losing 3 claims to `"cannot claim an alias"` (5/8 success).
- **Fix** (ruling option b): `claimAlias` now remembers EEXIST-failed
  aliases *per invocation* and excludes them from later picks in the same
  claim loop — only `errors.Is(err, fs.ErrExist)` poisons the exclude-set
  (any other error still propagates as a real failure). This converges in
  one extra round with no sleeps — deterministic, not a timing hack. The
  contract preserved is Class-B: a unique alias per joiner; exact `fork-N`
  *numbering* under concurrency was never deterministic in bash either and
  need not match.
- **Regression**: `TestConcurrentClaimUniqueAliases` — 24 concurrent
  `claimAlias` calls under `-race` → 24 unique aliases, zero errors. Plus
  reviewer's mixed 4-Go + 4-bash concurrent-join harness → 8/8 unique
  aliases on both rounds (was 5/8 pre-fix).

[Resent micro-notes from review]

- `cwd()` uses `os.Getwd()`, which resolves the **physical** path; bash's
  `$PWD` is the **logical** (symlink-preserving) shell variable. This is a
  cosmetic divergence in the `cwd` display field of `meta.json` only — it
  is never used for correctness/liveness — and is noted rather than
  "fixed" since replicating `$PWD`'s exact resolution semantics from Go
  isn't a meaningful goal for a display-only field.
- `setMetaAlias` is a deliberate best-effort no-op-tolerant rewrite (mirrors
  bash rename's `jset ... || true`) — it rewrites only `meta.alias`,
  preserving every other field verbatim, and a failure here is swallowed
  the same way bash swallows it.

[Possible Ripple Effects]

- None beyond the fix's scope — the claim-loop change only affects the
  auto-pick (bare-alias) join path; explicit-alias join was already
  single-shot and unaffected.

**CONFIRMED CLOSED** — reviewer's mixed 4-Go + 4-bash concurrent-join
harness passed 8/8 on both rounds. Commits: `c3db6b5` (original), `0e88056`
(F1 fix).

## [2026-07-13 17:39:14 UTC] [Port/Go] Phase 2 (3/N) — local send + send-gate + MaxMessageBytes

[Attempt #1]

Third Phase 2 milestone: local `send` with the full gate trichotomy,
matching bash's size-limit behavior deliberately differently, and closing
out the presence cross-parse rider flagged during P2.2 review. Approved.

[Files Changed]

- `internal/core/message.go` — new `MaxMessageBytes = 1 MiB`, aligned with
  the relay's `POST /send` body cap (`http.MaxBytesReader`) so both the
  local and remote paths share one message-size invariant instead of two
  independently-chosen numbers.
- `internal/client/send.go`, `send_test.go` (new) — `LocalSend` implements
  the send-gate trichotomy: never-armed (`listenerPid` null) → accepted
  unconditionally; listener alive → accepted; armed-then-dead → refused
  unless `--force`, which queues best-effort with a warning. The local
  from-chain resolves in order: own registration in the target channel →
  first own registration anywhere → `$CBUS_ALIAS` → `<shorthost>-<ppid>`
  (unroutable fallback). Appends via `O_APPEND` single-write. A message
  exceeding `MaxMessageBytes` is **rejected**, never silently truncated —
  a deliberate delta from bash's unguarded stdio write, which has no size
  cliff at all and would just write whatever the shell handed it.
- `cmd/cbus/main.go` — wires the local `send` branch, including the
  `--from ""` null-check parity fix carried over from the remote path.
- `internal/core/golden_test.go` — presence cross-parse rider (flagged
  during P2.2 review): a Go-canonical presence line
  (`{from,to,ts,kind,event,text}`) and its python-marshaled equivalent
  frame byte-identically through the lifted `emit()`, including the
  `kind=` header — proving the D8 canonical-bytes ruling extends to
  presence lines, not just plain messages. Reviewer re-verified this using
  the **real** rename-produced presence artifact from P2.2's join/rename
  work, rather than a synthetic line, closing the flag with a live
  fixture instead of a hand-built one.

[Testing Notes]

- Send-gate unit tests: never-armed/live/dead+force paths.
- From-chain resolution + unroutable-fallback test.
- Max-line reject-not-truncate test (message at/over `MaxMessageBytes`).
- Differential vs bash cbus: `send` stdout byte-identical; a Go-sent and a
  bash-sent line coexisting in the same inbox decode identically on
  either client. Green on both platforms.

[Possible Ripple Effects]

- One anti-drift rider identified during review: the relay should also
  adopt `core.MaxMessageBytes` (currently its own `1 MiB`
  `http.MaxBytesReader` constant, coincidentally the same value but not
  the same symbol) so the two caps can't silently drift apart. Rides the
  next commit.

## [2026-07-13 02:43:35 UTC] [Port/Go] CORRECTION — P2.1-F1 mechanism claim retracted

[Attempt #1]

No code change. Corrects the record set by the two prior entries
(`dd86250` "record P2.1", `effe3e4` "record P2.1 F1 fix"), both of which
stated the mechanism as "darwin `KERN_PROCARGS2` serves a zombie process's
cached argv with `err == nil`."

[What changed]

- The reviewer re-ran the repro conclusively and **retracted** that
  mechanism claim: the original repro window caught the child process while
  it was still technically running (a timing artifact), not in a genuine
  zombie state. On a real darwin zombie, the `KERN_PROCARGS2` sysctl call
  fails naturally with `EINVAL` — it does NOT return stale cached argv with
  a success code, contrary to what was recorded.
- Practical consequence: the pre-fix code (before `bf11d46`) was likely
  **already outcome-correct** — a true zombie's argv read would already have
  failed and the argv clause would already have read dead, without needing
  the explicit `SZOMB` check.
- The fix itself (`bf11d46`'s `procZombie`/`SZOMB` detection) and its
  regression test (`TestArgvClauseZombieDead`) are **retained** — not
  reverted. Explicit zombie detection is a fail-open hedge that doesn't rely
  on an unstated/unverified kernel error-code behavior, and the regression
  test still documents the intended contract (zombie → argv-dead)
  regardless of which code path currently enforces it.
- The code comments in `procinfo_darwin.go` and `liveness.go` that describe
  the (retracted) "serves cached argv" mechanism are now inaccurate and are
  queued for a reword at the top of P2.2.

[Why this is recorded]

- Mechanism claims in this changelog are treated as load-bearing fact, not
  narrative color — a retracted claim gets its own entry rather than a
  silent edit to the original, so the record shows what was believed, when,
  and why it changed.

## [2026-07-13 02:40:05 UTC] [Port/Go] Phase 2 (1/N, F1 fix) — darwin zombie reads argv-dead

[Attempt #1]

Closes the one confirmed finding from P2.1's conditional approval: the
darwin zombie liveness inversion.

[Files Changed]

- `internal/client/procinfo_darwin.go` — added `procZombie`, which reads
  `pbi_status` (offset 4 of the `proc_bsdinfo` buffer `procParent` already
  fetches) via `proc_pidinfo`/`PROC_PIDTBSDINFO` and compares against
  `SZOMB` (5, from `<sys/proc.h>`). `procArgs` now checks `procZombie` first
  and returns `syscall.ESRCH` for a zombie, instead of letting
  `KERN_PROCARGS2` return the process's stale cached argv with `err == nil`.
  Corrected the prior (false) comment claiming `KERN_PROCARGS2` already
  errors for zombies — it doesn't; that was the bug.
- `internal/client/liveness.go` — updated `argvContains`'s doc comment to
  describe the darwin zombie case accurately (was asserting behavior that
  didn't hold).
- `internal/client/liveness_test.go` — new `TestArgvClauseZombieDead`,
  reproducing reviewer's repro exactly: `exec.Command("true").Start()` left
  unwaited becomes a zombie; `pidAlive` (`kill -0`) still succeeds, but the
  argv clause must read dead. Without the `SZOMB` guard this test fails on
  darwin (stays argv-alive); it passes with the fix.

[Testing Notes]

- New regression passes on both darwin (MBP) and linux (NUC).
- This closes the P2.1 conditional-approval finding; P2.1 is now fully
  closed (no further caveat).

[Possible Ripple Effects]

- None beyond the fix itself — the zombie edge case was the sole gap
  identified, and normal kill/exit/crash liveness paths were already
  unaffected.

## [2026-07-13 02:30:12 UTC] [Port/Go] Phase 2 (1/N) — liveness off ps-spawns (sysctl/procfs) + peer_dead

[Attempt #1]

First Phase 2 milestone: moves the three-clause liveness predicate and
`find_owner_pid` off `ps` subprocess spawns onto native syscalls (Decision
1a — procfs/libproc rather than `ps`, per the D1 ruling, still keeping the
argv-fingerprint contract through Phase 2). Conditionally approved — one
confirmed finding, fix binds at P2.2.

[Files Changed]

- `internal/client/procinfo_darwin.go` (new) — reads argv via `sysctl
  KERN_PROCARGS2` and ppid/comm via `proc_pidinfo(PROC_PIDTBSDINFO)`.
- `internal/client/procinfo_linux.go` (new) — reads argv via
  `/proc/<pid>/cmdline` and ppid/comm via `/proc/<pid>/stat`.
- `internal/client/liveness.go` — `argvContains` returns `false` on ANY read
  error (ESRCH/EPERM/zombie) so the argv clause reads DEAD with no invented
  leniency (preserves condition (iii) / edge D1 exactly). `pidAlive` stays
  `kill -0`. `PeerDead`: never-armed peers get the 10-min grace, preferring a
  dual-written `lastActivity` field over meta mtime (D3 — the mtime fallback
  drops in Phase 3); armed peers are dead iff `!alive`. Removed the
  `ps`-shelling helpers entirely.
- `internal/client/marker.go` — `OwnerPID` now goes through the new
  `procParent` walk instead of shelling to `ps`.

[Testing Notes]

- Validated on BOTH platforms: self-parse `procArgs(self) == os.Args`
  (condition (i)) on MBP darwin and NUC linux; the real-process liveness
  tests (`tail -f` with the inbox path in argv) run through the new
  sysctl/procfs path (condition (ii)); a dead/nonexistent pid reads as an
  error, which reads as dead.
- **CONFIRMED FINDING (reviewer-reproduced empirically): darwin zombie
  liveness inversion.** `KERN_PROCARGS2` serves a zombie process's *cached*
  argv (the kernel doesn't clear it at defunct), so a zombie listener would
  read `alive` under the new path — inverted from bash's `ps`-based check,
  where a defunct process's args column reads empty and the predicate
  correctly reads it as dead. The fix (detect zombie state explicitly,
  probably via the same `proc_pidinfo` call's status field) is scheduled to
  land at the top of P2.2.
- Offsets and struct layouts for both platforms were otherwise statically
  verified (matching kernel headers); the live differential (real listener
  processes, both platforms) was byte-identical to bash cbus outside the
  zombie edge case.

[Possible Ripple Effects]

- Until the P2.2 fix lands, a killed-but-not-reaped listener process
  (zombie) on darwin would be misread as alive by the liveness predicate —
  scoped entirely to that edge case; normal kill/exit/crash paths are
  unaffected (confirmed by the real-process test matrix).

## [2026-07-13 02:06:41 UTC] [Port/Go] Phase 1 (6/N) — read-only local verbs (whoami/inbox/channels/list) — PHASE 1 CLOSED

[Attempt #1]

Sixth and final Phase 1 milestone: the read-only local verb set plus the
full differential closeout proving cbus-go/bash parity end-to-end. Phase 1
closed six-for-six: 40c82f0..b474c0d, all six milestones reviewer-approved.

[Files Changed]

- `internal/client/liveness.go`, `liveness_test.go` (new) —
  `MetaListenerAlive`, the three-clause liveness predicate (listenerPid
  alive + its argv references the inbox path via `ps -ww` + ownerPid alive
  if recorded), faithful to bash's `meta_listener_alive`. `ReadPeerMeta`
  tolerantly reads listenerPid/ownerPid/host/cwd (torn-read tolerant,
  matching `jget`'s exception-swallowing).
- `internal/client/identity.go` — `SessionMarkers` (this session's remote
  from-default markers, feeding `whoami`'s second line class).
- `cmd/cbus/main.go`, `main_test.go` — `whoami` (local channel/alias
  registrations + remote marker lines; exit 1 when neither exists, matching
  bash's probe semantics); `inbox <ch>/<al>` (prints the path, no trailing
  newline, bare alias refused); `channels` (peer count + listening count per
  channel); read-only local `list` (listen/off + pid + host + cwd; `--active`
  filter; legacy-v1 entry line). `active`/`peers` aliases wired.

[Testing Notes]

- `MetaListenerAlive` tested against real processes: a live `tail -f` with
  the inbox path in its argv reads `listen`; null pid, dead pid,
  argv-mismatch, and dead-owner cases all read `off`.
- `ReadPeerMeta` tolerance test; verb tests over a seeded `$CBUS_DIR`.
- **MBP seeded differential**: `whoami`/`inbox`/`channels`/`list`/`list
  dev`/`list --active` all byte-identical between cbus-go and bash cbus.
- **NUC live local-mode differential**: coder cross-compiled a temp
  `cbus-go` binary, ran it directly on the NUC against real state, confirmed
  byte-identical output, then deleted the temp binary (no install).
  Reviewer did not independently reproduce this leg — it crosses a
  permission boundary the reviewer doesn't have (no NUC shell access) — and
  accepted coder's session transcript as the evidence of record. This is
  expected, correct behavior for the review setup, not a gap.
- A1/A3/A5/A6 contract-class evidence bundle now complete for everything
  Phase 1 touches.

[Possible Ripple Effects]

- None functional — read-only verbs only; no local join/tail/send/prune
  yet (Phase 2).

**Phase 1 complete: cbus-go remote+read-only client side-by-side with bash,
6 commits (40c82f0, e98834b, c9464e1, d0150ab, 0273f41, b474c0d), all
reviewer-approved, installed at ~/.local/bin/cbus-go.**

## [2026-07-13 01:57:22 UTC] [Port/Go] Phase 1 (5/N) — remote tail arm-spec (A5) + .remote identity markers (A3); --from "" now dies

[Attempt #1]

Fifth Phase 1 milestone: the remote `tail` verb (print-only arm-spec) and
session-scoped `.remote` identity markers, plus the P1.4 ruled fix. Approved
clean — reviewer reproduced all evidence live, including the end-to-end
deep path (bash↔Go marker interop).

[Files Changed]

- `internal/client/marker.go`, `marker_test.go` (new) — `WriteRemoteMarker`
  writes this session's identity marker at
  `.remote/<host>/<channel>/<sessionId>` = `{alias, ownerPid, ts}`,
  pretty-printed via `json.MarshalIndent` (verified byte-identical to
  python's `json.dump(..., indent=2)`); a legacy machine-global FILE marker
  at that path is replaced first, matching bash's behavior. `OwnerPID` walks
  `$PPID` up to a `claude`-named ancestor via `ps` (parity with
  `find_owner_pid`, 16-hop cap), falling back to raw `$PPID` when no
  ancestor matches. `Now()` = `date -u` second precision.
- `internal/client/remote.go` — `RemoteTailSpec` builds the Monitor `ws:`
  arm-spec byte-identical to `cmd_tail_remote`'s bash heredoc (ws-scheme
  swap, subprotocol token, the em-dash in the description line).
- `cmd/cbus/main.go`, `main_test.go` — `tail <ch>@<host>/<al>` prints the
  arm-spec and writes the identity marker (print-only, token-only side
  effect — matches bash's "not a process" contract). The local
  blocking-follower `tail` stays deferred to Phase 2 by design (M6 follower
  work is local-transport scope, not remote).
- `cmd/cbus/main.go` — ruled fix from the P1.4 verdict: an explicit
  `--from ""` (present but empty) now dies with a usage error, matching
  bash's `${2:?}` null-check, instead of silently falling through to the
  from-default chain.

[Testing Notes]

- Marker indent=2 golden test, write/read round-trip, legacy-file-replace
  test, unroutable-fallback test, arm-spec format test, `--from ""` die
  test.
- **LIVE differentials vs bash cbus**: A5 (arm-spec) byte-identical output;
  A3 (markers) proven in BOTH directions — bash `cbus send` successfully
  read a Go-written marker, and `cbus-go send` successfully read a
  bash-written marker; marker JSON bytes are Go==python identical.
- Local marker test residue was cleaned up after the run; only the relay's
  spool residue remains (no GC exists server-side — expected).

[Possible Ripple Effects]

- None functional — remote `tail` is still print-only; no local listener
  exists in cbus-go yet (Phase 2).

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
