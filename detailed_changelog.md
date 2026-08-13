# Changelog (detailed)

## [2026-08-13 20:15:00 UTC] [Roles/Commands] opus token pinned to claude-opus-4-8 (temporary)

[Attempt #1] `6920262` on `chore/opus-48-pin` (branched from main at `b5dab23`, kept
off windows-port on purpose: the port branch is mid-flight and this pin should be
mergeable/revertable on its own).

[Motivating problem]
The harness's short alias `opus` now resolves to Opus 5, so every seat whose role
file or formation envelope said "opus" silently changed model generation. Carlos's
ruling from live formations: the Fable-5-orchestrator + Opus-4.8-coder pairing
beats Opus 5, so the token is pinned to the full id `claude-opus-4-8` until
further notice.

[Files Changed]
- roles/coder.md:3, roles/orchestrator.md:3 -- `MODEL: opus` -> `MODEL:
  claude-opus-4-8`. The full id passes roleModel's screen and Spawn's pre-fork
  gate unchanged (`^[A-Za-z0-9._-]+$`, no leading dash), so it flows to
  `claude --model claude-opus-4-8` with zero code change.
- commands/bus-spawn.md, commands/bus-branch.md -- the "valid values today"
  instruction now names claude-opus-4-8 and tells the skill to pass the full id
  verbatim when the user says "opus", never bare `opus`.
- internal/client/role_test.go -- TestLoadRoleRepoToplevel's literal moved from
  "opus" to "claude-opus-4-8". The expectation is a literal that guards the
  committed file's ruled value; it moves WITH the ruling, in the same change.

[Live-store edits, outside this commit]
LoadRole resolves repo-first only when spawning from inside a checkout;
everywhere else reads $CBUS_DIR/roles, and a formation member's explicit model
beats the role default. So the pin was also applied by hand at every live layer
on both machines (2026-08-13):
- $CBUS_DIR/roles/{coder,orchestrator,tester}.md on the MBP and the NUC
  (tester is a runtime-only role with no repo copy).
- Saved formation envelopes: `"model": "opus"` -> `"model": "claude-opus-4-8"`,
  33 fields across 17 MBP .formations JSONs + 3 fields in the NUC's; every
  touched file re-parsed as valid JSON, zero `opus` model tokens left.
- Installed /bus-* skills (~/.claude/commands/bus-{spawn,branch}.md), both
  machines.

[Possible Ripple Effects]
- The next `cbus selfupdate` / `install-roles` from a release that does NOT
  carry this commit restores `MODEL: opus` in $CBUS_DIR/roles and the installed
  skills. The .formations edits persist (user data, never overwritten by
  install). Durability requires this commit riding a release; reverting is the
  same substitution in reverse, with the formations the only fan-out layer.
- The parked orch-fable re-flip (`25189d1`, branch chore/orch-fable) edits the
  same orchestrator.md MODEL line; when it lands, fable wins that seat per the
  concluded eval and the conflict is a one-line resolution.
- profiles/opus5.md is Opus-5-specific tuning; a claude-opus-4-8 seat has no
  profile file, which is the documented safe state (profiles/README: absence is
  safe, the seat runs on its role file alone). Orchestrators following process
  rule 15 should NOT send opus5.md to a 4.8 seat.
- formations/dev-trio.json members all defer (`"model": ""`), so the committed
  starter inherits the pin through the role files with no edit.

[Testing Notes]
`go build ./...` clean; `go test ./internal/client/ ./cmd/cbus/` green after
the literal move, including 3x `-count=1` reruns of internal/client (one
uncaptured red on the first post-checkout run did not reproduce; this diff has
no timing surface). The roles doctrine canary passes -- the MODEL header sits
outside the shared doctrine block, whose 4x duplication is untouched. Grep
verification on both machines' live stores: `MODEL:` histogram reads 3x
claude-opus-4-8, 1x fable (reviewer), 1x sonnet (documenter); zero
`"model": "opus"` remaining in .formations.

## [2026-08-09 23:10:00 UTC] [Formation/Resume] blank-profile envelopes: instance sweep

[Attempt #1] `7903eb1`. Found by Carlos's first field test of resume on an older
formation: `cbus formation resume 'android-waiver-scan'` from a bare shell
refused with "aged out or lives under another profile" while the transcript sat
1.6MB under ~/.ccs/instances/work. Filed as cbus-kl4 with a store census: 16 of
17 envelopes record an empty profile on every peer (only dd-rollout, re-saved
post-capture, records one) -- including dd-cleanup saved the day before, because
a save captures what the seat metas hold and pre-capture joins contribute
nothing. The whole back catalog was button-unresumable.

[The defect]
transcriptRoots builds the instance root from HOME + the RECORDED profile
(fb948f8). With a blank profile there is nothing to build from, and a bare
shell adds no cfg root, so only ~/.claude/projects was searched -- structurally
empty on a CCS machine. The refusal's second clause was the truth; "aged out"
was not.

[The fix]
internal/client/transcript.go: InstanceProfiles(sid) -- a deliberate, named
sweep of ~/.ccs/instances/*/projects (plus the cfg-sibling base) returning the
distinct owning profiles, sorted; same sidRe screen as TranscriptPath (the sid
is untrusted text used as a glob). NOT folded into TranscriptPath: a lookup
that silently matched other profiles would let a caller find a transcript
under one profile and launch under another -- the blank-under-same-sid failure.
internal/client/formation_resume.go: when the anchor's recorded profile is
blank and HasTranscript misses, the gate consults the sweep. One owner: adopt
for the gate AND anchorLaunchPrefix AND peerEnv, return it as inferredProfile.
Several: refuse naming them. None: refuse stating the search was exhaustive.
A recorded profile never sweeps -- its miss refuses exactly as before.
cmd/cbus/formation.go prints the inference under the launch line; the anchor's
next `formation save` stamps the profile and the envelope heals.

[Files Changed]
- internal/client/transcript.go (+InstanceProfiles, sort import)
- internal/client/formation_plan.go (PlanWorld.InstanceProfiles seam, wired in
  GatherPlanWorld beside HasTranscript)
- internal/client/formation_resume.go (gate + launch plumb; ResumeAnchor and
  resumeAnchorWorld gain the inferredProfile return)
- cmd/cbus/formation.go (operator notice)
- tests: transcript_test.go, formation_resume_test.go, launch_intent_test.go
  (signature sites)

[Possible Ripple Effects]
`show`/SidState and apply's decidePeer deliberately do NOT sweep: show still
reads a blank-profile peer as STALE where resume now launches, and apply run
from a foreign profile still templates such peers. Both are recorded on
cbus-kl4 as residual surfaces; the dominant case self-heals because the
resurrected anchor runs under the inferred profile, so ITS apply sees the
transcripts through the cfg root. The decision brief's roster may read GONE
for fleet peers at bare-shell compose time; the brief already hands liveness
and presence to `apply --dry-run` by name, which re-reads from the anchor's
healed env.

[Testing Notes]
go test ./... green (gofmt gate included); GOOS=linux amd64+arm64 compile
gates pass (windows/amd64 fails on main's pre-existing close.go/codexwrap.go
syscall use -- the windows-port branch's remit, untouched here). Five new
tests: the sweep itself (owners sorted, ambiguity reported, injection screened),
inference adopted for argv AND child env against a WRONG-profile shell (env
would make it pass vacuously), both refusal shapes launch nothing, recorded
profile never sweeps (sweep stub t.Errors on call), and an end-to-end run with
the real sweep + real TranscriptPath closure against a real temp store in the
android-waiver-scan shape. Two mutants killed on their aimed assertions:
launch-from-recorded-profile (argv asserts fired) and always-sweep (sentinel +
refusal + forked-1 all fired).

## [2026-08-09 21:55:00 UTC] [Transcript/Fix] bare-shell resume of profiled formations

[Attempt #1] `fb948f8`, ships as v0.9.1. Found by the ccresume:// handler work
(cbus-phh): the full-launch field test refused a warm formation through the real
LaunchServices door, and the coder traced it to transcriptRoots rather than the
handler.

[The defect]
transcriptRoots derived the CCS profile root from $CLAUDE_CONFIG_DIR. Bare
post-reboot terminals and GUI-launched shells have no such variable (ccs sets it
per session), so with cfg unset only ~/.claude/projects was searched and every
profiled formation's resume refused with no-transcript-found — the northstar
scenario itself, broken on CCS machines, masked in every prior field test
because those ran from this session's env-carrying shell. Scope precisely: only
PROFILED formations; blank-profile envelopes store under ~/.claude and were
always fine (attribution per the coder's evidence-strength correction: the path
facts are theirs, the masking claim verified by the orchestrator against its
own shell env).

[The fix]
The profile root resolves from HOME + the recorded profile
(~/.ccs/instances/<profile>/projects), structurally — the envelope is the
authority, the env is a hint. Same trust move anchorLaunchPrefix made for the
launch prefix; the gate now matches the launcher. cfg-derived roots are kept
for non-standard layouts, deduped as before.

[Testing Notes]
Regression test pins the bare-shell profiled lookup (HOME override, env
explicitly empty) and the blank-profile inverse (bare shells still cannot see
instance roots for pre-profile envelopes — the documented caveat, unchanged).
Full suite 7/7; linux amd64+arm64 builds.

## [2026-08-08 20:15:00 UTC] [Formation/AnchorModel] always-anchor + the decision brief

[Attempt #1] `b78f1bc` (cbus-j4i) + `9bfd332` (cbus-zhj) + test riders `2d262c2`
(c1), `324af80` (c2/c3), `76cc0a2` (example same-host filter). Built by the
anchormodel formation (coder on opus, reviewer on fable, orchestrator session),
based on the mode+guard lineage because the brief renders real --mode commands.

[Motivating problem]
The northstar ruling: a formation envelope always has an anchor, and the restored
anchor decides -- or is prompted to decide -- how the rest comes back, per peer.
That makes the dashboard resume button safe by construction (it only ever launches
the decider), but it needs the invariant enforced at save time and the kickoff
upgraded from instruction to decision brief.

[Files Changed]
- `internal/client/formation_save.go` -- mint of a NEW anchorless envelope refuses
  with both remedies named and nothing written; a refresh of an existing anchorless
  file saves and sets AnchorMissing. anchorDefault still heals joined re-saves.
- `internal/client/formation.go` -- Formation.AnchorWarning() (deliberately NOT a
  Validate error: Save validates every write, and LoadFormation feeds the
  refuse-to-overwrite gate, so a hard rule would make the heal impossible).
- `internal/client/formation_resume.go` -- anchorRoster builds rows from the ONE
  gathered world (same HasTranscript closure the gates consumed); anchorKickoff
  renders the decision brief: roster, per-peer ruling request, operator
  confirmation, apply-flag examples, reconvene re-save.
- `cmd/cbus/formation.go` -- show renders a missing anchor as a named DEFECT row;
  the save door warns at write time.

[Ruled semantics worth knowing]
- Refuse-over-autopick on mints; warn-and-save on refreshes; heal on joined
  re-saves. Three paths, three different answers, each reasoned on the bead.
- The roster carries STABLE facts only. Liveness is the one volatile fact, and a
  stored liveness marker lying is the founding lesson of this codebase -- the
  brief names the dry-run as where present-right-now comes from.
- Examples are runnable-as-written: present-transcript AND on-this-host (blank
  machine means here, the exact negation of the apply skip), capped at two,
  absent when empty. The roster may honestly say a foreign peer's transcript is
  present while the example declines to name it: the roster states facts, the
  example promises actions.
- The anchor renders as the deciding seat, never as a peer awaiting a decision.

[Testing Notes]
- Whole-repo go test ./... green; GOOS=linux amd64+arm64 builds; gofmt clean.
- Sixteen pre-registered reviewer gates (A1-A8, B1-B8), all passed, with
  reviewer-run mutants proven on disk and baseline-restored at every step --
  including the pick-condition mutants in both directions and a structural proof
  that a second world gather is impossible.
- The c2 fixture needed separate stores for its two composes because the
  launch-intent claim from the first refused the second: the cbus-rze guard
  working inside a test, recorded so nobody loosens it for fixture convenience.

## [2026-08-08 07:35:00 UTC] [Formation/ModeGuard] apply --mode + the launch-intent guard

[Attempt #1] `1d4488f` (cbus-osr) + `f4bb204`/`60dc945`/`aa4b70f`/`a780785` (cbus-rze,
one milestone, two reviewer findings fixed in ruled parts) + `dede3f5` (unrelated
pre-existing gofmt). Built by the modeflags formation -- coder (opus) and reviewer
(fable) under an orchestrator session -- using cbus itself for every dispatch,
verdict, and ruling.

[Motivating problem]
The dd-rollout reboot proved the resume story end to end but left two gaps: choosing
resume-vs-fresh per peer required hand-editing the envelope (the literal user
question "how do I do that?"), and between `formation resume` forking the anchor and
the child re-joining, liveSids is blind, so a second resume double-launches two
processes onto one transcript.

[Files Changed]
- `internal/client/formation_apply.go` -- ApplyOptions.Mode + overrideMode() beside
  overrideChannel: per-run, in-memory, envelope never written. decidePeer is
  byte-untouched BY DESIGN; the safety argument is that the gates run unchanged.
- `internal/client/launch_intent.go` (new) -- the guard. Channel-dir dot-file
  marker; ClaimLaunchIntent is first-writer-wins via os.Link (EEXIST = claimed);
  reclaim OVERWRITES the corpse by rename, never unlinks (an absent path is the
  claim signal, so unlink-first reopens the race); reclaimers and clears serialize
  on a flock'd side token (LOCK_NB, losers refuse/skip, kernel-released on death).
- `internal/client/formation_resume.go` -- refuse-then-claim after every gate,
  before Fork. `internal/client/store.go` -- same-sid Join clears the marker.
- `cmd/cbus/formation.go`, `cmd/cbus/usage.go` -- the --mode flag and help.

[Expensive lessons, recorded so nobody re-buys them]
1. Absence is the signal: any reclaim that unlinks before relinking reopens the
   race it heals. This falsified the reviewer's own fix sketch, measured at 2/16.
2. The marker and the reclaim window want OPPOSITE lifetimes: the marker must
   SURVIVE its writer (the launcher exits on success), the reclaim window must DIE
   with its holder. File+TTL for one, flock for the other. Same file, two
   mechanisms, one reason each.
3. The peer dir cannot hold the marker: Join's reclaim RemoveAll would let whoever
   takes the alias clear the guard protecting the resumed transcript.

[Possible Ripple Effects]
- cbus-yca (apply-side resume window) adopts ClaimLaunchIntent; the racy writer
  was deleted so the unsafe path cannot be adopted by accident.
- BOTH syscall.Flock sites (ledger.go and launch_intent.go) need _windows splits
  when the windows-port branch merges -- recorded on cbus-rze.
- The clear can over-refuse (skip on busy lock, later-launch markers declined);
  bounded by TTL 180s and moot when a live child is armed (live-sid gate first).

[Testing Notes]
- Whole-repo go test ./... green (5/5 uncached at the original tip, re-verified on
  this lineage); -race over client and cmd; linux amd64+arm64 builds + vet.
- Deterministic 16-racer probes, both entry states, exactly-one-forked; SIGKILL
  release test whose discriminating mutant (flock swapped back to the deleted
  O_EXCL token design) reds on "the lock survived its holder's death".
- Reviewer independently reproduced every claim: own mutants proven on disk and
  restored, door probes with the built binary, branch walks of the fixed protocol
  after each change (re-walked, not diffed, at the coder's own request).

## [2026-08-07 21:35:00 UTC] [Formation/Resume] profile capture + the first-hop resume verb

[Attempt #1] `8e30eac` (cbus-yv3) + `409ab7d` (cbus-lsy) — workstream B of the
northstar reset: after a reboot, nobody should hand-copy a session id, a cwd, or a
launcher invocation out of a JSON file.

[Motivating problem]
Field-proven on the dd-rollout reboot: the envelope records everything needed to
resume EXCEPT the CCS profile (so the correct `ccs work --resume` had to be derived
by locating the transcript by hand), and the first hop — rebooted machine to running
anchor — was entirely manual even though everything after the anchor is automated.

[Files Changed]
- `internal/client/store.go` — peerMeta gains `profile` (omitempty; pre-profile
  metas rewrite byte-identically); Join stamps `currentProfile()`.
- `internal/client/identity.go` — `currentProfile()`: structural parent-dir check
  on CLAUDE_CONFIG_DIR (`.ccs/instances/<profile>`), no substring literal, so it
  holds on windows separators and stays out of the port's zero-grep gate.
- `internal/client/liveness.go` — reader-side PeerMeta carries `profile`.
- `internal/client/formation_save.go` — RosterPeer.Profile; capturePeer refreshes
  it like cwd, blank meta never clobbers a hand fill, invalid tokens surfaced via
  the SkippedBirth channel.
- `internal/client/formation_resume.go` (new) — ResumeAnchor + the injected-world
  seam; anchorLaunchPrefix (recorded profile wins even from a bare shell);
  anchorKickoff (restored-session framing + reconcile instruction, no role
  re-brief); launcher-authored ledger restore whose run attribution follows the mec.2 authority rule: the anchor's own surviving claim when one exists (claims outlive processes), blank when none — pinned both ways.
- `cmd/cbus/formation.go`, `cmd/cbus/usage.go` — the `resume` verb and help.
- Tests: join stamping (incl. the structural-vs-substring discriminator),
  save capture/preserve/refresh/garbage, launch shape (ccs prefix, kickoff
  content, blank-run ledger), bare-shell fallback, nine-refusal table with
  launch-nothing pinned.

[Possible Ripple Effects]
- Envelopes saved by pre-yv3 binaries have blank profiles; the resume verb then
  depends on the invoking shell's own profile to find transcripts. dd-rollout was
  hand-filled to profile=work (legal hand fill, preserved by blank-never-clobbers).
- The verb reuses apply's identity prohibitions; any future change there should
  check formation_resume.go for parity (three sites now: decidePeer, BootstrapPeer,
  resumeAnchorWorld).

[Testing Notes]
- Full suites green both packages; linux amd64+arm64 cross-compile PASS.
- Real-store smoke: `resume ghost` errors; `resume dd-rollout` with its anchor
  LIVE drew the live-armed refusal naming dd-rollout/orchestrator — after the
  smoke caught the transcript check firing first (wrong refusal when the
  transcript hides under another profile). Gate reordered, pinned by a test where
  both gates are true at once.
- The launch happy path is fixture-proven (recorder forker); the real-terminal
  acceptance run is the next actual reboot of a saved formation.

## [2026-08-05 02:45:05 UTC] [Formation/Save] repeatable --anchor key=value on save

[Attempt #1] `c1ef5fa` — bdx-7m1.6, the one claudebus-side subtask of the
bd-dashboard formations-page epic.

[Motivating problem]
The formations page links an envelope to its bdx epic via `drift_anchors.bdx`
(anchor precedence shipped dashboard-side in bdx-7m1.3). The key already
survives re-saves — save only owns `git_head`, every other anchor key is the
human's — but setting it meant opening the envelope in an editor. The flag is
that hand edit, minus the editor.

[Files Changed]
- `cmd/cbus/flags.go` — `parsedArgs.multi` collects every occurrence of a valued
  flag in order; new `all(name)`. `opts` keeps last-wins, so the existing
  single-value callers (`has`) are behaviorally untouched.
- `cmd/cbus/formation.go` — save parses positionals-first-then-flags (bootstrap's
  shape): `save <name> [channel] [--anchor key=value ...]`. Malformed pairs
  (no `=`, empty key) die on usage. Channel resolution order unchanged.
- `internal/client/formation_save.go` — `SaveFormation` gains the `anchors`
  param. `checkHandAnchors` refuses `git_head` BEFORE any store work (a refused
  fresh save writes nothing); `setHandAnchors` writes pairs after
  `setGitHeadAnchor`, overwriting same-named keys deliberately.
- `cmd/cbus/usage.go` — save block documents the flag and the bdx convention.
- Tests: parser repeat semantics (`flags_test.go`); client-level write /
  flagless-survival / flag-wins / refusal-writes-nothing
  (`formation_save_test.go`); CLI-door test incl. channel-then-flags order,
  git_head refusal, malformed pairs, trailing junk (`cmd/cbus/formation_test.go`).
  Existing `SaveFormation` call sites gained `, nil` mechanically.

[Possible Ripple Effects]
- A dash-leading second positional (`save name -x`) previously reached the store
  as channel `-x` (ValidName permits `-`); it now dies on usage. Ruled a fix.
- Repeated valued flags on OTHER verbs still last-wins silently, as before; only
  save reads `all()`.
- The bd-dashboard sweep reads `drift_anchors.bdx` as the direct epic link with
  precedence over session-join inference — a wrong hand-set id now has a
  one-flag path in, same blast radius as the hand edit always had.

[Testing Notes]
- `go test ./cmd/cbus ./internal/client` green; `go vet` clean.
- Cross-compile gate: linux amd64 + arm64 PASS. GOOS=windows fails on main
  pre-existing (`close.go`/`codexwrap.go` syscall use; their windows variants
  live on the unmerged windows-port branch); this diff touches neither file.
- Field smoke through the built binary, temp store: save with two anchors,
  `show` renders them beside the machine `git_head`; flagless re-save keeps
  both; `--anchor git_head=x` refuses rc=1 with the machine-owned message.
- Read-only against the real store: 14 envelopes list, `rn-foundry` renders
  with its existing anchor — old envelopes read fine under the new binary.

## [2026-07-24 05:06:40 UTC] [Feat] durable channel ledger + formation_run_id (bdx-mec.2)

[Attempt #1] `0d41228`

Motivating failure: the api36 formation's alias-to-session map exists nowhere
after the fact. meta.json is the only place alias, channel and sessionId bind
together, and PruneChannel destroys it with the peer dir. The ledger records
that binding append-only so a run stays reconstructible after every peer is gone.

[Files Changed]
- `internal/client/ledger.go` (new) — the ledger: event schema + closed-vocabulary
  AppendLedger, per-peer run-claim identity (writeClaim/readClaim/currentRun/
  liveRuns), flock(2) mint lock (acquireMintLock), run resolution (ResolveRunForJoin/
  RunBoundary/commitOrBlank), terminal-event subject capture (readSubject/
  RecordEventForSubject), launcher-authored run sourcing (LauncherRun/SelfAliasIn),
  harness detection (HarnessName).
- `internal/client/store.go` — wired join/resume, self-leave, rename, spawn
  (ReserveAlias), and the reaper-emitted leave in PruneChannel; forced-leave in
  Unregister; run boundary captured before mutation.
- `internal/client/follow.go` — rebind event on arm (armMeta), the only event that
  records which process holds the tail.
- `internal/client/formation_apply.go` — restore event per launched peer, run sourced
  from the applier's own claim.
- `internal/client/formation.go` / `formation_save.go` — formationRunId at envelope
  and per-peer level; ChannelRoster reads each peer's claim; envelope run derived from
  the unique roster claims (dead peers included) with a RunConflict report field.
- `internal/client/identity.go` — metaOrigin helper alongside metaSessionID.
- `cmd/cbus/formation.go` — RunConflict rendered at the save call site (named ids,
  blank envelope), not just stored in a report struct.
- test files: `ledger_test.go` (new), `formation_runconflict_test.go` (new),
  `formation_test.go` (emission-order guard updated for the new key).

[Design — settled over 6 review rounds]
- Placement: `CBUSDir()/.ledger`, outside every peer dir so the reap can't reach it,
  dot-hidden so the current channel walkers skip it — an older binary ignores the
  ledger by construction, not by luck.
- Seven closed event kinds. Unknown kinds and events missing channel/alias ERROR
  rather than drop silently (a dropped durability record reads as "never happened").
  Base fields serialize even when empty (known-absent vs missing). Emitter
  (self/reaper/forced) carries authorship without widening origin's validated enum.
- Run identity is a per-peer CLAIM file. A run exists only while a live peer claims
  it, which removes the stale-run-file class at the root rather than guarding it. A
  historical (alias,sid) binding cannot prove current membership because session ids
  survive resume; inheritance is driven only by live claims. The claim is the
  AUTHORITY, not evidence: a failed write yields a blank run (never a fake nonempty
  id that the next sibling would split against), and a claimless peer's later events
  stay blank rather than infer a sibling's run — the authority principle is per-event.
  A split roster (distinct live claims) is unknowable and handled identically across
  join, save, and events: blank + a named warning, never first-claim-wins.
- Launcher-authored events (spawn, restore) carry the launcher's OWN run explicitly,
  since the launcher is the authority for the child slot it creates.
- Mint lock: flock(2) on an open fd, taken after three rounds of hand-rolled
  file-dance semantics each leaked a new TOCTOU. Kernel crash-release + true
  ownership deletes the whole apparatus (O_EXCL dance, pid+start-token, break/steal).
- formation_run_id is additive under the unchanged cbus-formation/v1 schema: an old
  binary carries the new key through its Extra map (verified against a frozen legacy
  envelope AND peer codec, not raw bytes).

[Possible Ripple Effects]
- Every join/leave/rename/arm/reserve/unregister/apply now also writes a ledger event
  and (for members) a `.run` claim file under the peer dir. Best-effort for the
  ledger line; the claim write is authoritative and its failure degrades to a blank
  run with a loud stderr.
- meta.json is unchanged (the claim is a separate sidecar), so no peerMeta rewriter
  hazard and no byte-compat concern.
- A v0.8.1 binary sharing the bus is safe: additive + append-only + dot-hidden means
  it never reads or is confused by the ledger.

[Testing Notes]
Full suite 7/7 packages, -race clean, git diff --check clean, cross-compiled for all
three shipped unix targets (darwin/arm64, linux amd64/arm64). Revert-audit applied to
every added test (revert the exact production line, confirm the covering test fails);
it caught six false-positive tests across the tranche, all strengthened. Both reviewers
signed off: reviewer1 design conformance, reviewer2 correctness. NOT installed — the
running binary stays cbus-go v0.8.1 pending an explicit operator swap. Not pushed.

## [2026-07-22 05:49:29 UTC] [Fix] codex wrapper — claim listenership at join, print teardown cause last

[Attempt #1]

Two field-driven fixes from cbus-6ij.5's parallel-review experiment: the
codex peer `harness/sol` reviewed the v0.8.1 tranche in parallel with the
authoritative Claude reviewer, whose own independent verdict had been PASS.
Adjudication amended that to FINDINGS after sol surfaced a bug the
reviewer's own mutation coverage had exercised only partially. Two of
sol's three findings survived adversarial adjudication and blocked the
commit; this is their fix, landed as one tranche.

[Files Changed]
- `internal/client/codexwrap.go`, `internal/client/codexarming_test.go`
  (new file) (6c6b85a) — sol-HIGH. The wrapper now claims listenership at
  join, before the bridge finishes arming — `cbus list` previously showed
  the codex peer as `off pid=?` for the tens of seconds arming takes, and a
  sweep misread that window as dead and killed a live TUI. The claim seeds
  the replay cursor at offset 0 first and verifies the round-trip through
  `readCursor`, then records the wrapper's own (pid, start-time) witness.
  The ordering is deliberate: both writes are best-effort, so committing
  the listenerPid witness before an independently-failed seed would leave
  listenerPid-set with the cursor absent — `resolveResume` reads that
  combination as an ever-armed migration and seeks the first arm straight
  to EOF, silently dropping every message queued in the gap. Seeding
  first, and skipping the claim entirely on an unverified seed, makes that
  loss state unreachable by construction rather than merely unlikely. This
  is the concrete fix for the finding recorded on cbus-6ij.5 as "claim-
  before-seed ordering; partial seed I/O failure yields listenerPid-set
  cursor-absent seek-END silent loss."
- `internal/client/follow.go`, `internal/client/follow_test.go`
  (6c6b85a) — the displacement gate now exempts the exact same process
  (pid and start-time both self), so the wrapper's own same-process bridge
  arm isn't refused as if it were a second listener taking over. F3,
  record-only per sol-MED-2: this makes one-arm-per-process a caller-owned
  invariant with no runtime enforcement, and the exemption's contract
  comment now says so explicitly — including that the pre-exemption gate
  was itself the enforcement this change removes. Reviewer spot-checked
  the landed comment wording against the orchestrator's own refined
  phrasing.
- `internal/client/codexwrap.go`, `internal/client/dormancy_test.go`
  (6c6b85a) — sol-MED-1. `killServer` now reaps the app-server first,
  bounded and escalating to SIGKILL, so its dying WebSocket-reset stderr
  flushes before the bridge's own teardown cause, which now prints last.
  Both reap waits are bounded, so an app-server wedged in D-state can't
  suppress the cause line forever — printing outranks reaping.

[Possible Ripple Effects]
- The one-arm-per-process invariant is now explicitly documented as
  caller-owned rather than runtime-enforced; any future caller that arms
  the same process twice on the same peer (accidentally or by a bug
  elsewhere) will not be refused by this gate anymore. No such caller
  exists today; flagged here so a future reviewer knows where to look if
  one appears.
- Confirms the pattern already established across this task: a genuinely
  independent second reviewer (here, a different harness entirely) found a
  real gap in the primary reviewer's own mutation coverage. Worth keeping
  in mind for future gates on this codebase, not just this task.

[Testing Notes]
- Full suite PASS, `-race` PASS, `GOOS=linux` amd64+arm64 compile PASS,
  client tests PASS in a `golang:1.26-bookworm` container. All pins
  mutation-verified. 5 files changed, +482/-33.
- No session trailer, conventional commit format, working tree clean.
  Not pushed (push stays Carlos-gated).

## [2026-07-22 00:16:55 UTC] [Client/Multi-harness] cbus-6ij.4 tranche B — Stop-hook fallback for exec workers (final tranche, increment 4 build complete)

[Attempt #1]

Coder-executed, reviewer-gated, the third and final tranche of the codex
integration build. Where tranche A's app-server bridge covers a
`codex --remote`-launched peer, this tranche covers the other reachable
shape from the epic's Phase-0 spike: a plain `codex exec` worker, which
has no app-server socket to bridge into but does run Stop hooks. This is
the parked Stop-hook mechanism from the Phase-0 spike, finally built now
that it has a defined role (exec-worker fallback) rather than being the
primary delivery path the early spike considered.

[Files Changed]
- `internal/client/codexstophook.go`, `internal/client/codexstophook_test.go`
  (27bb2d9) — `cbus codex-stop-hook [--wait D]`. On a codex Stop event,
  long-polls this session's inbox(es) within the codex hook timeout; on new
  chat traffic, emits `{"decision":"block","reason":<framed>}`, which codex
  injects as a continuation turn. No traffic before `D` (default 550s,
  inside codex's 600s default timeout) or a `stop_hook_active` re-entry
  with nothing new allows the stop — hitting the timeout is always a
  failure path, per the tranche-A doctrine (cbus-6ij.4 notes,
  docs-verification addendum) that a Stop-hook must return before Codex's
  own limit and never rely on timeout-as-signal. Polls every registration
  a worker holds (a worker hears each channel it joined), orders new
  frames across inboxes by timestamp, batches them into one reason per
  fire with each message's frame kept intact for provenance, and skips
  presence/status traffic — a continuation turn per join/leave would burn
  a model turn on ceremony, mirroring the bridge's own presence-skip
  design (8da523c). A dedicated dot-prefixed `.stop-cursor` sidecar tracks
  delivered position per peer directory, deliberately never shared with
  the follower's `.cursor` — sharing it would give the hook and a live
  `Monitor`/tail on the same peer inconsistent re-arm semantics. Best-
  effort throughout: exit 0 always.
- `internal/client/harness.go` (27bb2d9) — minor touch-up alongside the
  new verb.
- `cmd/cbus/main.go`, `cmd/cbus/usage.go` (27bb2d9) — `codex-stop-hook`
  dispatch and usage line.
- `docs/architecture/command-reference.md`,
  `docs/architecture/multi-harness-exploration.md` (27bb2d9) — a Codex
  integration entry with the `~/.codex/hooks.json` snippet wiring
  `SessionStart` to `hook-join`, `Stop` to `codex-stop-hook`, `SessionEnd`
  to `hook-exit` (the last of the three docs-only, `hook-exit` already
  lenient-decodes), plus a one-line pointer from
  multi-harness-exploration.md section 4.

[Possible Ripple Effects]
- This closes the cbus-6ij.4 build entirely. Both delivery paths from the
  epic's design are now shipped: the app-server bridge for `--remote`
  peers, this Stop-hook fallback for `exec` workers. Only cbus-6ij.5
  (spawn/formation integration for codex peers) remains open in the
  multi-harness epic.
- The `SessionEnd`-to-`hook-exit` wiring is documentation only in this
  commit — no code changes, since `hook-exit` already accepts the lenient
  stdin shape. A wrapper-launched peer (tranche A/B's `cbus codex`) still
  has no `SessionEnd` wiring of its own; that gap was recorded as a
  tranche-B rider note on cbus-6ij.4 and stays open, not fixed here.

[Testing Notes]
- Reviewer PASS on record: field loop independently reproduced,
  `STOPHOOKOK` delivered verbatim, bounded exit confirmed (no hang past
  the wait window).
- Full `go test ./...` green, `-race` clean, `GOOS=linux` amd64 and arm64
  built, working tree byte-identical to HEAD after verify, no session
  trailer, conventional commit format.
- Not pushed. Full commit roster for cbus-6ij.4 now on main: `8da523c`
  (A2 bridge), `9ae66d6` (F1 zero-turn-adopt race fix), `d8ef18a` (A3
  wrapper + hook-join), `5ad7ff2` + `c0d5b70` (identity-scrub fix,
  falsified then re-fixed), `27bb2d9` (this tranche), plus five documenter
  changelog commits (`b97b94a`, `af43635`, `7d2292e`, `c7d7faf`, this one).

## [2026-07-21 23:56:19 UTC] [Fix] codex identity leak closed at the execution locus — app-server env scrub

[Attempt #1]

Closes the leak documented in the 23:46:15 UTC amendment immediately
below: `5ad7ff2` scrubbed the wrong process's environment. This entry is
the confirmed fix, verified against the actual live topology rather than a
CLI-door proxy, per the gate doctrine that falsification produced.

[Files Changed]
- `internal/client/codexwrap.go`, `internal/client/codexwrap_test.go`
  (c0d5b70) — `codexCommands` now builds both the app-server process and
  the `codex --remote` TUI process with the scrubbed environment (the
  entire `SessionID()` chain removed) plus `CBUS_ALIAS`/`CBUS_CHANNEL`
  pinned. Previously only the TUI env was scrubbed; the app-server is where
  a model-invoked tool shell (`cbus` among them) actually executes in this
  topology, so it needed the same treatment.

[Possible Ripple Effects]
- None beyond what the original 5ad7ff2 entry already noted (from resolves
  to the bare alias, a recorded v1 limitation) — the locus changes, the
  mechanism and its limitation don't.
- Closes out the gate-doctrine item from the falsification: this fix was
  accepted only on field evidence (see Testing Notes), not a green suite
  alone, matching the acceptance bar the orchestrator set when the doctrine
  was written.

[Testing Notes]
- Field re-gate PASS: the reviewer independently reproduced the failure
  condition (a poisoned launcher env) and observed `from=codexgate` on the
  reply — the actual execution locus, not a CLI-door proxy for it. This is
  the same live-topology check that caught the original leak and falsified
  the first fix.
- Coder-run pre-commit: full `go test ./...` green, `-race` clean, `GOOS=
  linux` amd64 and arm64 built, working tree byte-identical to HEAD after
  verify. No session trailer, conventional commit format.
- Sequence now fully on record: leak found live (dogfood smoke) -> first
  fix (5ad7ff2) -> falsified live (amendment) -> root-caused -> re-fix
  (c0d5b70) -> confirmed live. Not pushed.

## [2026-07-21 23:46:15 UTC] [Amendment] 5ad7ff2 field-falsified — TUI-only env scrub was the wrong locus

[Attempt #1]

Corrects the entry immediately below (2026-07-21 23:43:53 UTC, `5ad7ff2`)
by append, per doctrine — that entry is not deleted or edited, because it
accurately reported what was believed true at commit+gate time. This entry
records what the next round of field verification found.

The orchestrator ran a post-land provenance round-trip against the
relaunched live `harness/codex` peer: a message in, a reply out. The reply
still carried `from=harness/orchestrator`. The fix did not close the leak
it claimed to close.

Root cause: `5ad7ff2` scrubbed the session-id env vars from the `codex
--remote` TUI process's environment. But in this topology, tool shells
(`cbus` among them) that the model invokes execute inside the APP-SERVER
process tree, not the TUI process — the wrapper spawns the app-server with
the launcher's environment fully intact, since only the TUI env was ever a
target. The scrub landed on the wrong process.

[Files Changed]
- None yet. This entry documents the falsification and diagnosis; the
  re-fix (scrub both processes, app-server env primary) is in flight and
  will land as its own commit with its own changelog entry once
  field-confirmed — not folded in here, so the sequence stays legible: what
  was tried, why it looked right, what field testing actually showed.

[Possible Ripple Effects]
- New gate doctrine recorded on cbus-6ij.4: env-mediated properties in the
  `codex --remote`/app-server topology require live-topology verification
  before a fix is accepted — a CLI-door proof of "resolution given a
  scrubbed env" is not evidence about which process actually executes the
  shell. Both the coder's original CLI proof and the reviewer's mini-gate
  tested the former, not the latter, and both missed this.
- This is the fourth exec-mode-vs-remote-topology divergence surfaced in
  one day on this task (after: hooks require exec/interactive and never
  fire over the app-server protocol; `thread/list` is global and not
  per-server scoped; `SessionStart` doesn't fire in the remote topology at
  all). The pattern is now itself the lesson: assume nothing proven under
  `codex exec` transfers to `codex --remote` without a live check.

[Testing Notes]
- Falsified by a live round-trip against the actual relaunched peer, not by
  a unit test or a code read — the same category of check (real topology,
  not a proxy for it) that has caught every divergence on this task so far.
- Acceptance for the re-fix is field-only: the observed `from` field on a
  real reply, stated verbatim in the report, is the bar — not a green
  suite.

## [2026-07-21 23:43:53 UTC] [Fix] codex identity leak — spoofed provenance from an inherited launcher session-id env

[Attempt #1]

Found by the tranche-A live dogfood smoke, run immediately after gate PASS
(per the plan recorded at task creation: "join a live codex peer to this
same harness channel as the integration's own smoke test"). The
wrapper-launched codex peer joined the real "harness" formation channel as
`harness/codex`; the bridge armed and delivered a message into its TUI, the
model ran the requested `cbus send` — and the reply landed on the bus as
`harness/orchestrator`, not `harness/codex`.

[Files Changed]
- `internal/client/codexwrap.go`, `internal/client/codexwrap_test.go`
  (5ad7ff2) — `codex --remote` runs shell commands (`cbus` among them) that
  inherit the wrapper process's environment. If the wrapper's own launcher
  had `CLAUDE_CODE_SESSION_ID`, `CBUS_SESSION_ID`, or `GROK_SESSION_ID` set,
  codex's own `cbus send` resolved through `SessionID()`'s ordered lookup
  (cbus-6ij.1) straight to the LAUNCHER's registration — a session-identity
  seam built for harness-neutral hooks became a spoofing vector for a
  wrapper-launched peer with a shell in the loop. `codexRemoteEnv` now
  scrubs the entire `SessionID()` chain from the TUI's env and pins
  `CBUS_ALIAS`/`CBUS_CHANNEL`, so codex's `cbus` invocations fall through to
  the alias path and self-identify as the peer instead. The bridge's own
  thread id isn't known yet at TUI spawn time (discovery via the passive
  connection finishes after launch, per d8ef18a), so alias identity is the
  only mechanism available here, not a session-id override.

[Possible Ripple Effects]
- Recorded v1 limitation, not fixed here: `--from` on a codex-originated
  send resolves to the bare alias (the pre-existing `CBUS_ALIAS` fallback),
  not a richer per-thread identity. Acceptable for a single wrapper-owned
  peer; would need revisiting if a wrapper ever manages multiple codex
  threads concurrently.
- The same inherited-env shape could recur for any future wrapper that
  shells out on behalf of a launched peer; the scrub-and-pin pattern in
  `codexRemoteEnv` is the model to reuse rather than re-deriving it.

[Testing Notes]
- Found live, not by a written probe or a reviewer read — the dogfood smoke
  is what caught it, underscoring why the field-experiment step was kept in
  the plan rather than treated as optional polish after the gate.
- Coder-run pre-commit: full `go test ./...` green, `-race` clean,
  `GOOS=linux` amd64 and arm64 built, working tree byte-identical to HEAD
  after verify. No session trailer, conventional commit format. Standalone
  commit on top of tranche A (8da523c, 9ae66d6, d8ef18a); not pushed.
- Re-verification against the live `harness/codex` peer is the next step to
  confirm the fix closes the leak end to end (not yet reported as of this
  entry).

## [2026-07-21 23:34:19 UTC] [Client/Multi-harness] cbus-6ij.4 tranche A — Codex CLI as a first-class bus peer (increment 4)

[Attempt #1]

Coder-executed, reviewer-gated, second build increment of the multi-harness
effort, landing on top of cbus-6ij.1's identity/liveness seams. Orchestrated
on the same dogfooded "harness" formation. Two reviewer FINDINGS rounds are
folded into this single entry rather than written piecemeal, per doctrine —
both fixes landed and were confirmed before this record was written.

Phase-0 spike (recorded separately on cbus-6ij.4, not part of this commit
set) had reshaped the build: real-time push via `codex app-server`'s
ws-over-UDS protocol was proven live and promoted to the primary delivery
path, with the Stop-hook verb parked as an exec-flow fallback (`hook-join`).
A follow-up A1 probe round, also pre-commit, fixed three protocol facts the
bridge's delivery ladder depends on: `turn/steer` lands in-turn (steered
content answers within the same turnId, no new turn) but needs an active
turn, so a bare `turn/start` after an init/MCP-load gap returns `-32600
no-active-turn` and needs a bounded retry; a cold-restarted server rejects a
bare `turn/start` on an existing thread (`-32600 thread-not-found`) and
needs `thread/resume` first, except on a zero-turn thread which has no
rollout to resume from, where the bare `turn/start` is the correct
fallback; and only `thread/resume`, not a bare `turn/start`, subscribes to
the full event stream (`turn/started`, item events, `turn/completed`) — a
bare client sees only `thread/status/changed`. These three facts are the
`resume-attach always, steer-first busy, bootstrap-turn-then-resume for
zero-turn threads` shape the bridge ships with.

A2 build then surfaced a harder finding: `SessionStart` hooks configured on
the app-server process, driven purely over the ws protocol, never fire —
Codex hooks are exec/interactive-only, not app-server-topology, and
app-server has no `--dangerously-bypass-hook-trust` flag to even attempt it.
The shipped rendezvous instead uses a passive pre-connected app-server client
that observes the TUI's own `thread/started` notification (carries the full
thread object, id and cwd, at ~190ms) — the interactive TUI launches with
zero hook config and zero trust bypass, a security/UX improvement over the
originally gated hook-based design, not a workaround for it.

[Files Changed]
- `internal/client/codexws.go`, `internal/client/codexws_test.go` (8da523c)
  — stdlib WebSocket-over-UDS JSON-RPC client for a codex app-server
  (masking, ext-length, ping/pong, close, fragmented reads). The handshake
  reader threads into the read loop so a first frame arriving in the same
  segment as the HTTP 101 upgrade isn't dropped (C1); a 16 MiB frame cap
  turns a corrupt length into an error instead of an unbounded allocation
  (C2); hand-rolled base64 replaced with stdlib (C5).
- `internal/client/codexbridge.go`, `internal/client/codexbridge_test.go`
  (8da523c, reworked 9ae66d6) — the bridge. Attaches via `thread/resume`
  (survives a server restart, subscribes to the turn stream) and delivers
  each framed bus message on a steer -> turn/start -> resume+retry ladder,
  steering an in-flight turn when one is active. 9ae66d6 fixes a race on a
  zero-turn thread adopt: the opener `turn/start` returned before the
  rollout flushed to disk, so the immediate re-resume raced it and died
  `-32600 no rollout found`, killing the bridge on every fresh-thread adopt
  (masked earlier because the test fake persisted rollouts synchronously —
  the second fake-fidelity divergence this tranche, after the
  initialize-required gap). `attach` now waits bounded for the opener turn
  to idle (a bare client already receives `thread/status/changed`, no
  subscription needed) before resuming with short bounded backoff; a resume
  error that isn't `no-rollout` surfaces without an opener. The fake gained
  an async-rollout mode (`startFakeCodexAdopt`) plus a regression test
  reproducing the race and a pin on the resume-error gate.
- `internal/client/follow.go`, `internal/client/follow_test.go` (8da523c) —
  new `frameSink` seam so the follower tags each frame by kind. The CLI tail
  path stays byte-identical; the codex sink routes on kind (inject chat +
  the dormancy marker, skip presence) instead of parsing the rendered head —
  closes C4, a confirmed defect where a hostile `--from` value containing
  space-kind-equals could forge a presence token and silently drop a
  genuine message from the codex side while Claude peers still saw it.
  `RunFollower` deleted as redundant (one caller, zero test references at
  ef180c9, no vacuous-test exposure).
- `internal/client/codexwrap.go`, `internal/client/codexwrap_test.go`,
  `internal/client/harness.go`, `internal/client/harness_test.go`
  (d8ef18a) — `cbus codex [--channel CH] [--alias AL] [codex args...]`:
  stands up a per-peer app-server, launches a `codex --remote` TUI attached
  to it, opens its own passive app-server connection before launch and
  bounded-waits (45s + cause-naming diagnostic) for the TUI's own
  `thread/started`, hard-verifies `thread.cwd == wrapper cwd` (mismatch is a
  loud refusal naming both paths), joins the bus as that thread via
  `OverrideSessionID(threadId)` + `Join` (the cbus-6ij.1 seam the
  `--session-id` flag exposes), then bridges. A bridge that fails to start,
  or exits while the TUI still lives, kills the TUI and exits nonzero (F2:
  fail-whole-unit — a deaf bridge must not leave a peer looking healthy).
  Socket at `$CBUS_DIR/.sock/<short>.sock`, dot-prefixed so channel walkers
  skip it, 0700, SUN_LEN-bounded with an `os.TempDir()` fallback and a
  per-launch nonce; teardown group-kills the app-server's native child
  (an npm codex install runs a 3-pid tree — killing only the node parent
  orphans the native server).
- `internal/client/harness.go`, `internal/client/harness_test.go` (also
  d8ef18a) — new `cbus hook-join`: harness-neutral `SessionStart` auto-join
  from lenient stdin (`session_id`/`sessionId`) and `$CBUS_CHANNEL`, silent,
  exit 0 always. Serves exec/stop-hook flows where hooks do fire; optionally
  writes the session id to `$CBUS_CODEX_RENDEZVOUS`, unused by the wrapper
  itself but retained for tranche-B exec-flow tooling.
- `cmd/cbus/codex_bridge_test.go`, `cmd/cbus/codex_wrap_test.go`,
  `cmd/cbus/main.go`, `cmd/cbus/usage.go` — `codex-bridge` and `codex`/
  `hook-join` verb dispatch and usage lines.

[Possible Ripple Effects]
- No `SessionEnd` hook is wired yet, so a wrapper peer lingers as a dead
  listener until lazy prune — earmarked for tranche B / cbus-6ij.5
  (`hooks.SessionEnd` -> `cbus hook-exit`, once hooks fire at all in this
  topology).
- Teardown is SIGTERM-only, no SIGKILL escalation; the timeout path kills
  the TUI without a `Wait`. Both record-only for now.
- `thread/list` was probed and found to return the user's global,
  paginated `~/.codex` history rather than anything scoped to the serving
  process — any future adopt/discovery flow built on it must filter by
  status and cwd itself; this tranche's rendezvous does not use it.
- Fleet/relay untouched — the bridge is a second consumer of the existing
  ws leg, per the design-space.md §7 ruling (f1d4363).

[Testing Notes]
- Two fake-fidelity divergences surfaced this tranche, both the same
  shape: the hand-written app-server fake was too forgiving, so unit tests
  stayed green while the real attach failed. First: app-server requires the
  `initialize` handshake before any thread/turn call (`-32600 Not
  initialized`); the unit fake didn't model this, so the gap was caught
  only by the mandatory real-binary smoke, not by the suite — the fake was
  fixed to pin the initialize-first requirement with a strict test. Second:
  the fake persisted rollouts synchronously where the real server is async,
  masking the zero-turn adopt race described above until a fresh-thread
  field smoke hit it; the fake gained an async-rollout mode
  (`startFakeCodexAdopt`) so the adopt path is honestly testable going
  forward.
- Reviewer gate ran FINDINGS twice before PASS. First pass (pre-existing
  code read): C1/C2/C4/C5 routed to coder as described above, C3 (trailing-
  newline trim on injection) ruled an R2 clarification and not a defect,
  C6 (dormancy marker must inject as a turn, not get filtered) ruled and
  implemented with a test. Post-build re-gate found the async-rollout race
  (fixed 9ae66d6) and the silent-bridge-death gap (closed by the
  fail-whole-unit design). Final re-gate found one small coverage gap: the
  attach resume-error gate (rollout vs other errors) had lost its only pin
  when an obsolete test was deleted — reviewer proved the gap by mutating
  the gate away and watching the suite stay green; closed with one test
  (a non-rollout resume error must abort attach without running the
  opener).
- Mutation testing, all proven on disk and restored: hook-join minus the
  override fails its empty-sid assertion; rendezvous-write removal fails
  its pin; SUN_LEN-bound removal fails the TempDir-fallback pin (sock len
  235 vs 103); the async-race-fix mutant killed with the verbatim field
  error; `TestJoinAs` override mutant killed; disabling `waitOpenerIdle`
  entirely and letting backoff alone ride the flush lag confirmed the
  no-sole-reliance-on-ordering redundancy bound.
- Field smokes on real codex 0.145.0, both coder- and reviewer-run
  independently: a real `cbus send` landed in a live codex thread as the
  verbatim frame and the model replied in-thread; a real join presence
  event reached the peer inbox but zero extra codex turns fired (R3
  verified two-sided); a real fresh-thread opener race was ridden out live
  (wrapper armed in ~22s); steal-displacement killed the TUI within ~2s of
  bridge dormancy and teardown reaped the socket and server, demonstrating
  F2 by observable effect (reviewer noted the exact stderr line/exit code
  weren't captured live since tmux killed first — confirmed instead by
  code-read and the unit pin); app-server's SUN_LEN refusal on a long
  socket path and the 3-pid npm teardown tree were both confirmed live,
  not just in the design ruling.
- Full `go test ./...` green incl. `-race`; `GOOS=linux` amd64 and arm64
  built; each of the three commits verified standalone-buildable (A3's
  remainder stashed, A2 alone builds and tests green; same for the race-fix
  commit against the wrapper commit). Working tree byte-identical to HEAD
  after each verify pass. No session trailers, conventional commit format.
- Not pushed — outward step stays gated. cbus-6ij.1 (802520e, 21f1b37,
  cc115b9) and this tranche (8da523c, 9ae66d6, d8ef18a) now both sit on
  main.

## [2026-07-21 21:17:31 UTC] [Client/Multi-harness] cbus-6ij.1 — harness-neutral identity + liveness seams (increment 1)

[Attempt #1]

Coder-executed, reviewer-gated, first build increment of the multi-harness
effort (cbus-6ij epic) to make cbus harness-neutral ahead of the Codex
integration (cbus-6ij.4). Orchestrated as a dogfooded formation on the
"harness" cbus channel: orchestrator anchor, coder implements, reviewer gates
before commit, documenter (this entry) records. Two spec deltas were
coder-declared and orchestrator-sanctioned before implementation: the hook
no-stdin-sid fallback resolves through the full `SessionID()` chain rather
than `CLAUDE_CODE_SESSION_ID` alone (harness-neutral consistency — the
stray-env trap only applies when stdin carries a sid, which the override
closes); `warnIfSessionless` relocates to after the `--session-id` override
so the flag doesn't trigger a false sessionless warning.

[Files Changed]
- `internal/client/identity.go` (802520e) — `SessionID()` ordered lookup:
  in-process override > `CBUS_SESSION_ID` > `CLAUDE_CODE_SESSION_ID` >
  `GROK_SESSION_ID`. New `OverrideSessionID(sid)` returns a restore func and
  outranks all env.
- `internal/client/harness.go` (802520e) — `HookExit`/`HookCompact` use the
  override instead of the `os.Setenv` round-trip, closing the stray-
  `CBUS_SESSION_ID` shadow trap (ranked ahead of `CLAUDE_CODE_SESSION_ID`, a
  stray exported var could otherwise shadow a hook's stdin sid). Lenient hook
  stdin decode: `session_id` (Claude Code, codex) or `sessionId` (grok
  camelCase), snake wins when both present.
- `internal/client/harness_test.go`, `internal/client/identity_test.go`
  (802520e) — ordered-lookup table tests, camelCase hook stdin mirrors,
  both-fields case.
- `cmd/cbus/main.go`, `cmd/cbus/usage.go` (21f1b37) — `--session-id` flag on
  join/leave/rename via `applySessionIDFlag` (`extractFlag`-based); `send`
  threads it through `splitVerbArgs`' valued-flag set so a message body may
  still contain the literal token. `warnIfSessionless` moved out of
  `runSend` into the send sub-handlers, evaluated after the override.
- `cmd/cbus/session_id_flag_test.go` (21f1b37) — CLI door tests incl.
  flag-beats-env and CBUS-beats-CLAUDE precedence.
- `internal/client/marker.go` (cc115b9) — `isClaudeName` replaced by pure
  `isHarnessComm(base)` over the exact set {claude, grok, xai-grok-pager,
  opencode, codex} plus the `claude-` prefix; basename split factored into
  `commBase()`. `ownerFromPid` applies both to kernel comm and argv[0].
  Codex npm installs exec the native codex child under a node shim; the
  ancestor walk hits codex before node, so an exact entry suffices (node and
  grokd stay false).
- `internal/client/marker_test.go`, `internal/client/close_test.go`
  (cc115b9) — `isHarnessComm` table incl. claude-3 true, node false, grokd
  false.

[Possible Ripple Effects]
- cbus-6ij.4 (codex bridge) depends on this landing first; the epic's
  sequencing already reflects that — held for that assignment, not pushed.
- Fleet binaries pre-dating this change do not recognize non-Claude comm
  names in the `ownerPid` walk; no fleet-compat shim was scoped (matches the
  epic's stated blast radius — grok pilot, opencode plugin, codex stop-hook
  tasks all still pending, this is core-seams only).

[Testing Notes]
- Coder gates: full `go test ./... -count=1` green; `GOOS=linux` amd64 and
  arm64 builds OK; real-CLI `join`/`leave --session-id` verified door-to-door.
- Reviewer independently reproduced all coder gates plus: full suite, linux
  amd64/arm64 builds, CLI door matrix (flag-beats-env, CBUS-beats-CLAUDE),
  six mutations all killed on aimed assertions with tree cmp-restored
  between mutants.
- Reviewer verdict: FINDINGS, not a clean PASS. F1 actionable — the
  stray-`CBUS_SESSION_ID`-vs-hook-stdin trap had no pinning test; reviewer
  proved the gap by reverting `HookExit` to the setenv round-trip on disk
  and watching the full suite stay green regardless. Fix:
  `TestHookExitStdinBeatsStrayCbusEnv` plus a `HookCompact` mirror, confirmed
  to fail under the setenv mechanism. Folded in and closed before merge.
- Three record-only micro-notes, no fix required: n1, `send --session-id`
  verified at the CLI door but has no automated row; n2, `warnIfSessionless`
  wording still names only `CLAUDE_CODE_SESSION_ID`, stale for a
  harness-neutral bus, deferred; n3, explicit empty `--session-id` no-ops to
  ambient identity while `--from` dies on explicit empty — documented-
  intended asymmetry, not a bug.
- No session trailers (repo policy, verified). Not pushed — held pending the
  `.4` codex bridge assignment.

## [2026-07-21 19:38:43 UTC] [Docs] design-space.md §7 — injection boundary generalized for foreign harnesses

[Attempt #1]

Docs-only. Trigger: Carlos flagged that the Codex integration direction (per-peer
`codex app-server` + `--remote` TUI, proven same-day by the cbus-6ij.4 round-2
probes) reads like a contradiction of design-space.md's opinionated file-first
doctrine, and asked for a well-founded reconciliation before any build — plus
answers on the relay story, a provider-pattern trigger, and whether Codex
sessions are join-anytime like Claude's.

[Files Changed]
- `docs/architecture/design-space.md` — NEW §7 (six subsections), appended after
  §6. Core moves: (7.1) §1's constraint 3 ("the Monitor boundary") reclassified
  as a *measurement of Claude Code*, not a design choice — generalized to "any
  transport must terminate in the harness's native injection surface," with a
  four-harness table (Claude/Grok: monitor stdout; Codex: turn/start over
  ws-on-UDS; OpenCode: plugin session.prompt). Everything left of the last hop
  is invariant: inbox.jsonl the only durable hop, the bridge IS a follower
  (same loop/identity/cursor), so "files for some, daemon for others" is
  rejected as the frame in favor of "files for all, native injection per
  harness." (7.2) app-server vs the §5.1 broker bill, line by line: harness-
  owned lifecycle (ships/updates with codex), no spool needed (inbox already
  is one — constraint 1 does the work), one-session blast radius, no ports/
  tokens locally; plus the exploration doc's own "unless a harness leaves no
  alternative" clause firing (plain TUI unreachable, MCP can't push, notify
  send-only, Stop-hook park hostile to a human composer). Honest cost ledger
  kept: +1 process/peer, experimental protocol pinned via generate-json-schema,
  SUN_LEN ~104 cap, unprobed 30-min thread unload. (7.3) relay unchanged —
  bridge dials the existing ws leg as a second source; relay stays harness-
  blind. (7.4) the three reachability tiers answering the join-anytime
  question: spawned (first-class), wrapper-launched (first-class + adoptable
  any time via thread/loaded/list), plain-launched (send-only; push retrofit
  impossible — probed; recovery is a `codex resume` relaunch under the
  wrapper, zero conversation loss). (7.5) provider seam named (identity
  source / launcher argv / delivery sink / prompt template) with extraction
  deferred until the third harness ships — same discipline as the roles
  doctrine duplication. (7.6) reopen conditions: Codex ships a Monitor
  equivalent (delete the bridge), protocol churn beats pinning (fall back to
  the parked Stop-hook), or a third harness fits no sink shape (extract
  early).
- `docs/architecture/multi-harness-exploration.md` — §4 heading struck-through
  and retitled ("full push via app-server"), dated update block inserted
  summarizing the round-2 probe results and pointing at design-space §7 +
  cbus-6ij.4; original analysis preserved below as the pre-probe record. The
  §1 liveness note (follower argv must contain the inbox path) struck and
  corrected — obsolete since M6.2 (9a3a075) made liveness purely structural.

[Possible Ripple Effects]
- design-space §1's constraint-3 wording is now reinterpreted by §7 rather
  than edited in place — a reader of §1 alone still sees the Claude-specific
  phrasing; §7 is the corrective lens. If that trips a future reviewer, the
  fix is a one-line forward pointer in §1, not a rewrite.
- The cbus-6ij epic's increments 4/5 (bdx) now trail the docs: increment 4's
  build shape should follow the task's round-2 notes (app-server bridge
  primary, stop-hook fallback) — the epic design field itself was NOT edited.
- behavior-spec's "Harness assumptions" catalog (A1-A17) does not yet have
  rows for the codex surface; deferred to the .4 build.

[Testing Notes]
- Docs-only; no code, no tests. All probe claims trace to executed spikes
  recorded on cbus-6ij.4 (session artifacts in the session scratchpad):
  Stop-hook chain (ZERO→THREE), 615s hold under timeout=1200, hook: Stop
  Failed on overrun, ws-over-UDS 101 upgrade, turn/start into the TUI's own
  thread rendering PURPLE with composer intact.

[Attempt #1]

Docs-only. A design-review thread (2026-07-20/21, v0.7.0 era) stress-tested the
store/transport choices against the standard alternatives; this capture makes
the analysis durable so the next reviewer starts from conclusions instead of
re-deriving them. Doc written via the topic-capture skill, deep-dive mode: two
discovery agents mapped overlap in the existing docs and symbol-verified every
code claim before writing.

[Files Changed]
- `docs/architecture/design-space.md` — NEW. Six sections: (1) the three
  constraints that pin the design (durable store-and-forward by construction /
  no daemon / Monitor stdout-or-ws boundary) with a table of which constraint
  each alternative breaks; (2) the idle-poll cost ledger (~12-15 syscalls/s,
  page-cache hot, ~0.003% core; latency priced against LLM turnaround) and the
  kqueue/fsnotify rejection; (3) the --steal takeover cadence (identityEvery ×
  followPoll ≈ 1s window, duplicates-never-loss, opt-in-not-last-wins, R-B
  no-lock ruling); (4) the harness truncation boundary (cbus rejects-never-
  truncates at MaxMessageBytes; the two measured caps differ in kind —
  per-line is cooperative/dodgeable via BodyWrap, per-notification is hard —
  and the mark-and-fetch-vs-fragment ruling) plus the size-funnel table
  (1 MiB → ~128 KiB argv → ~3 KB displayed); (5) rejected alternatives — local
  ws broker (the relay is the priced bill), p2p ws (no such protocol mode;
  asynchrony dies), RPC/IPC family (FIFOs/mq/shm/D-Bus/XPC each fail a
  constraint; RPC is the wrong shape — schema + deadline vs freeform + none),
  SQLite (no cross-process notification, one lock domain vs per-peer failure
  domains, glass-box store is load-bearing), Redis (Streams ≈ native superset
  of the M4 machinery incl. XAUTOCLAIM ≈ --steal, but daemon returns and
  durability inverts to configuration); (6) reopen triggers (no-LLM traffic,
  hundreds of peers, sub-100ms fan-out, history-as-asset → SQLite index
  beside the store, not instead of it).
- `docs/architecture/overview.md` — companion-documents list gains the
  design-space.md line (between prior-art and cutover-decision-package).
- `docs/prior-art-and-cc-internals.md` — §5 Pointers gains a lead entry
  pointing to the new doc as the forward-looking companion to §3/§4.
- `simple_changelog.md` / `detailed_changelog.md` — this entry.

[Possible Ripple Effects]
- None functional (no code touched). Doc-graph only: design-space.md links
  behavior-spec §8.6/8.7, protocol §4/§13, prior-art §3/§4, overview §5; those
  anchors are section numbers, so renumbering any of them would orphan links.
- The doc cites symbols (followPoll, identityEvery, rotated, LocalSend,
  appendInbox, handleSend, Reframe, spool Write) rather than line numbers, so
  it survives edits but a rename of any of those would need a doc sweep.

[Testing Notes]
- scan-references.sh validated all 6 code-path references (internal/client{,/
  follow.go,/presence.go,/send.go}, internal/core/frame.go,
  relay/internal/spool/spool.go).
- Symbol verification by discovery agent this session: appendInbox is in
  presence.go (NOT store.go — corrected before writing), opens
  O_APPEND|O_CREATE|O_WRONLY with a single write; relay handleSend uses
  http.MaxBytesReader(core.MaxMessageBytes) at relay/cmd/cbus-relay/main.go;
  spool.Write is Maildir tmp→new.
- Not committed — awaiting Carlos's call per repo practice.
- Post-review fixes (parent session review over the bus, verified against
  code): (1) the truncation-detection redundancy claim scoped honestly —
  triple on the relay path, double on local (the ⚠ marker is Reframe-only;
  LocalEmit adds no oversize warning), with the harness ...(truncated) leg
  flagged as a revocable 2026-07-13 delta; (2) the idle cost ledger labeled
  derived-from-loop-shape-not-measured and corrected to include the 5 sleep
  wakeups (~20 syscalls/s all-in, was ~12-15); (3) the FIFO kernel-buffer
  figure platform-qualified (16-64 KiB, was Linux-only 64 KB).

## [2026-07-20 03:51:31 UTC] [Client] v0.7.0 — M6 deprecation-drop: closes cbus-8k9.4

[Attempt #1]

Three commits (`75e352d`..`1601f13`), M6 of the go-port epic — its final
milestone. `cbus-8k9.4` (P3 homogenization) closes with this release: every
item on its Phase 3 semantic-upgrades list is now DONE.

**M6.1 (`75e352d`) drops the deprecated `register` verb and `peers` alias.**
`register` was a v1 alias for `join global`; `peers` a v1 alias for `list`.
Both now fall through to the existing default and exit 1 with the frozen
`cbus: unknown command '<verb>' (cbus --help)` — no new error path needed.
Nothing programmatic depended on either (verified: not in the usage text, no
test invoked either verb, no skill or script calls them), so the drop needed
no compatibility shim. The tests go through the CLI door on purpose — the
thing being deleted IS a dispatch entry, and calling `runJoin`/`runList`
directly could never prove the verb no longer reaches them. `register`'s own
case is pinned specifically: it must not create the `global` channel on its
way to failing, since a verb that errors after doing its work is the worse
outcome.

**M6.2 (`9a3a075`) deletes the `TRANSITION(P3T2)` argv-grep liveness shim
whole.** `liveness_transition.go` is gone; `listenerIdentityHolds` has one
branch left — the recorded `(pid, starttime)` witness against the process now
wearing the pid. An armed meta with no witness reads dead, the posture R1
already took for a stampless meta, so there is no longer a second answer for
a pre-P3 arm to fall into. Field impact was verified nil before the drop, not
assumed: every armed meta on the Mac carried a `listenerStart` (9 of 9), and
the NUC's store held no peer metas at all — no pre-P3 arm survived anywhere
in the fleet at drop time. `procArgs` itself does not die with the shim —
`transitionArgvIdentity`, `argvContains`, and `metaInboxNeedle` go, but
`procArgs` stays for `close.go:96` (the owner guard) and `marker.go:79`
(`ownerFromPid`); following the dead-code chain one hop further would have
broken owner detection silently. The TRANSITION token is gone from the tree
including the comment explaining its history — the sweep is meant to be a
clean binary check, so the prose now says "the argv-grep branch" instead.

**FLEET COMPATIBILITY STATEMENT (verbatim, also riding the GitHub release
body):** a pre-P3 (pre-v0.4.0) binary arming against a shared `CBUS_DIR`
after this upgrade writes metas the fleet reads as dead — the argv read shim
is gone and listener identity is structural-only; fleet binaries must be
v0.4.0+.

**`cbus-p8g`, closed MOOT at merge.** `TestArgvClauseZombieDead` called
`argvContains` directly and died with the shim file, along with
`TestTransitionNeedleStaysRaw` and `TestPredicateTransitionBranch` (replaced
by `TestPredicateStamplessArmedReadsDead`, which uses a genuinely LIVE pid so
the missing witness is the only possible cause of the verdict). The
zombie=dead contract `TestArgvClauseZombieDead` used to pin is carried
forward by `TestPredicateStructuralZombieReadsDead` — the inheritance chain
is recorded in `9a3a075`'s own commit message, and the reviewer ran that pin
directly in the linux container rather than trusting the message. Separately,
`TestClosePeerIgnoresAForeignListenerPid` is removed as VACUOUS — and that is
this milestone's own find, not incidental fallout: it kept passing after the
collapse while pinning nothing, because its stampless fixture now read dead
for an unrelated reason once the branch it originally exercised was gone. A
test that survives a deletion by accident is worse than one that breaks,
since nothing announces that it stopped testing anything. Its contract is
already held by `TestClosePeerIgnoresARecycledListenerPidStructurally` — post
collapse, a foreign live follower wearing the recorded pid and a recycled
stranger wearing it are the same case (witness mismatch), so re-seeding a
second foreign case would run one path twice under two names.

**`cbus-fi3` (`1601f13`, test-only).** The golden rows render the pid through
a fixed-width field, `pid=%-7s`, and the normalizer replaced only the digit
run inside it — so the trailing padding varied with digit count and the
goldens silently only matched a 5-digit pid. Darwin usually obliges, which is
why this survived to the release gate; a container hands out 2-4 digit pids,
and the M6.2 gate run failed there on all four pid-bearing subtests.
Normalizing the whole padded field fixes it with zero golden churn, because
the placeholder is five characters like a 5-digit pid and pads to the same
column — the four golden constants are byte-identical to before this commit.
A new property test proves width-independence directly over 1 through
4,194,304 (linux `pid_max`), so no live child has to hand out a pid of a
convenient length for the property to hold. What went wrong is worth naming:
the old line was anchored on the `"pid="` prefix on purpose, with a comment
explaining that a bare-digit replace would corrupt other numbers in the
output — that reasoning was right and incomplete. It closed the
false-positive hazard and never asked whether the field around the value was
fixed-width. A normalizer has to own the whole column, not the value inside
it.

**Docs**, all in `docs/architecture/`: `command-reference.md` sweeps every
live `register`/`peers` mention — the bash-era dispatch table rows (§1) gain
forward pointers rather than being rewritten (they remain accurate for the
retired `bin/cbus`, which still has both verbs); the "Reserved / conventional
names" bullet drops `register` from the list of things that target `global`;
the `### cbus register` subsection is rewritten from "deprecated" to
"removed"; `cbus list`'s heading drops the `(alias: cbus peers)` parenthetical;
§15's table rows and port note are struck through with closure notes. Found
during the sweep, not on the coder's flagged-line list: `cbus list`'s own
heading at the old line 754 also carried the `peers` alias mention — caught
by grepping the file myself rather than trusting the flagged-line list was
exhaustive. `behavior-spec.md` gets a matching sweep (the §9 dispatch row, the
§13 quirk-registry item) plus a new dated preamble note closing out the
`TRANSITION(P3T2)` mechanism the 2026-07-19 P3-tranche-2 note had described as
scoped to one release. `port-map.md`'s Phase 3 bullet list and the D1 ruling
table row are marked DONE for every remaining open item (name tightening and
`list --json`, both actually shipped in M5 but never stamped in this file
until now; the deprecated-surfaces drop and the TRANSITION shim removal, both
M6) — Phase 3 as a whole is now stamped fully executed.
`compat-deletion-plan.md` gains a Tranche 3 paragraph and is stamped CLOSED:
all seven original items now have a final, executed disposition (1–2 deleted
tranche 2, 3/4/6 deleted tranche 1, 5 resolved at cutover with no code
change, 7 deliberately kept frozen), and the `TRANSITION(P3T2)` remnant
tranche 2 introduced as a temporary shim is itself deleted in tranche 3.

[Files Changed]
`cmd/cbus/deprecated_drop_test.go` (new), `cmd/cbus/list_golden_test.go`,
`cmd/cbus/main.go` — `cmd/cbus`. `internal/client/close.go`, `close_test.go`,
`dormancy_test.go`, `follow.go`, `follow_test.go`, `formation_plan_test.go`,
`liveness.go`, `liveness_structural_test.go`, `liveness_test.go`,
`liveness_transition.go` (deleted), `meta_rewrite_test.go`,
`rename_invalidation_test.go`, `send_test.go`, `store.go`, `store_test.go` —
`internal/client`. `README.md` (coder's M6.1 commit, one `register` reference
replaced with a removed-as-of-v0.7.0 note; the relay's `GET /peers` HTTP API
docs deliberately untouched — different surface). `docs/architecture/
command-reference.md`, `behavior-spec.md`, `port-map.md`,
`compat-deletion-plan.md` — this docs commit. `simple_changelog.md`,
`detailed_changelog.md` (this entry).

[Possible Ripple Effects]
The FLEET COMPATIBILITY statement above is the load-bearing one: any machine
still running a pre-v0.4.0 binary against a shared `CBUS_DIR` will see its
own arms read as dead by every v0.7.0+ peer, with no shim left to fall back
on. Field-verified nil impact on the MBP+NUC fleet specifically, not a
guarantee for any other `CBUS_DIR` this binary might be pointed at. `register`
and `peers` are now indistinguishable from any other typo — any muscle-memory
use surfaces the standard unknown-command error, not a deprecation notice.

[Testing Notes]
`deprecated_drop_test.go` goes through the CLI door (`run()`), asserting both
the exit code/error text AND that `register`'s failed path left no trace (no
`global` channel created). `cbus-fi3`'s new property test covers pid widths 1
through 4,194,304. Full suite green; `cbus-p8g` verified reviewer-side by
running `TestPredicateStructuralZombieReadsDead` directly in the linux
container rather than trusting the inheritance-chain claim in the commit
message.

## [2026-07-20 03:01:14 UTC] [Client/JSON] v0.6.1 — cbus-vjo: a store-root directory is not a channel

[Attempt #1]

Field finding filed the same day v0.6.0 shipped: `list --json`'s unfiltered
path emitted any peerless directory in the store root as a phantom channel
with an empty `peers` array. `$CBUS_DIR/roles` — written by `install-roles`
beside the channel directories — is the live case, so every real store hit
this, and the shipped v0.6.0 reported it. Text `list` prints no row for one;
`channels` and `channels --json` both skip it; `list --json`'s unfiltered
path was the sole dissenter from three surfaces that already agreed. The
drop rule already existed, just gated on `--active` for no real reason — a
channel every peer got filtered out of and a directory that was never a
channel at all are the same thing to a consumer, and applying the existing
rule unconditionally (`8ee5dbc`) closes both with one line. Legacy v1 stays
exempt: it is peerless BY CONSTRUCTION (predates the alias level), and R18
wants it visible so a GUI can surface the prune remedy — its own regression
test guards that exemption specifically, since a careless version of this
fix is exactly the one that would break it.

Caught because the fix was verified against the REAL store rather than a
fixture. The regression fixture mints the peerless directory the way the
real one is minted — `cbus install-roles`, not `os.MkdirAll` — which is also
why no earlier test reached this state: every test channel came from `join`,
and `join` always writes a peer alongside the directory it creates, so a
peerless channel dir was unreachable in a store built only by joining.

Docs: `behavior-spec.md` Sec 9.1's `--active` bullet gains a parity note —
the zero-peers drop is unconditional, not `--active`-gated, with the same
`$CBUS_DIR/roles` provenance and the R18 legacy-v1 exemption spelled out
(`045a059`). Riding the same commit: a new standing doctrine in all four role
files (orchestrator #11, coder #15, documenter #12, reviewer #17) — bus
message bodies are single-quoted in a shell `cbus send`, never double, since
a double-quoted body command-substitutes backticks and expands `$vars` and
the reporting channel itself can execute or leak what it merely mentions.
Provenance for the doctrine, both from this same evening: a documenter
correction message ate its own text on an unescaped backtick (harmless —
the substituted command just errored); a reviewer gates message with
backticks around an install-roles reference EXECUTED it against the real
store (impact verified nil, but the class of mistake is not).

[Files Changed]
`cmd/cbus/jsonout.go` (the unconditional drop, `legacyV1` doc comment),
`cmd/cbus/vjo_parity_test.go` (new) — coder's commit `8ee5dbc`.
`docs/architecture/behavior-spec.md` (Sec 9.1 parity note), `roles/
orchestrator.md`, `roles/coder.md`, `roles/documenter.md`, `roles/
reviewer.md` (quoting doctrine) — documenter's commit `045a059`, both on
branch `vjo-parity` off `m5-json`. `simple_changelog.md`,
`detailed_changelog.md` (this entry).

## [2026-07-20 00:51:16 UTC] [Client/JSON] M5 (cbus-8k9.4): client-side name tightening + list/channels/whoami --json

[Attempt #1]

Five commits (`d4d34ac`..`5f746bb`), M5 of the go-port epic's Phase 3. Two
independent deliverables land together: `core.ValidStoreName` (M5.1, `d4d34ac`)
rejects leading-dot and leading-dash names at the point the client CREATES
them, and `list`/`channels`/`whoami` gain a `--json` mode (M5.2a/b/M5.3,
`cae64d0`..`6ba1624`, C2 fold `5f746bb`) that absorbs `cbus-oq9.4`.

`ValidStoreName` is additive over `core.ValidName` (`name.go`'s own doc
earmarked it "a later phase may tighten CLIENT-side only"): `ValidName` remains
the wire authority the relay gates `/send`/`/tail` on, unchanged, so a name it
rejects can still arrive from an older or third-party client. The damage it
closes is real today — every client traversal skips dot-prefixed entries to
stay blind to the `.remote`/`.reap` trees, so a `.foo`-named peer or channel
was created successfully and thereafter invisible to `list`/`channels`/
`whoami`; a leading dash is flag-shaped, and with no `--` terminator anywhere
in the CLI, reaches a forked child's CLI as a flag. Wired at the three store
chokepoints every creation path funnels through (`Join`, `ReserveAlias`,
`Rename`) and at the `branch`/`spawn` pre-validators, which now share one
predicate instead of five ad-hoc `HasPrefix("-")` checks — `branch`'s own
pre-validator isn't redundant: without it, a rejected `--name` still left a
parent registration behind, since `branch` joins before it reserves.
Addressing stays permissive (R20): `ParseLocal`/`ParseRemote`, `rename`'s
channel selector, formation `Validate`, and the remote marker tree are
untouched, so `cbus unregister <ch>/.foo` — the only cleanup path for a legacy
bad name — can still name its target. A DERIVED channel is sanitized rather
than rejected: `branchChannelFromGit` strips leading dots and dashes, so a repo
at `~/.dotfiles` keeps deriving a channel instead of hard-failing with no lever
but an explicit channel every call.

M5.2a (`cae64d0`) pins the rendered bytes of `list`, `active`, and `channels`
in a byte-level golden test — the rig M5.2b's refactor needed, since "the
refactor didn't change the output" is otherwise a claim the reviewer has to
verify by reading a diff. M5.2b (`7d066de`) then routes both the text renderer
and a new JSON encoder through one traversal, `client.ScanStore`: the two can
never disagree about who is listening, which is the failure a GUI consumer
would surface first and a text-only test would never see. `ScanStore` is
deliberately not `ChannelRoster` (the rename-path lookup, which drops a peer
whose meta is torn because a save must not record what it couldn't read) —
`ScanStore` keeps that peer with blank fields, matching `list`'s own
long-standing `?`-column behavior, and both functions now carry a comment
naming the difference. The JSON schema is an object at every level, never a
bare array, so a level can gain sibling keys later; `listenerPid` is
`omitempty` (absent, never `0`, when never armed — `0` is itself a real pid);
`scope` is pinned `"local"` now so a consumer written today survives `"remote"`
landing later; a legacy v1 entry is marked explicit with an empty `peers`
array rather than omitted or half-populated. Two riders folded into the same
commit: `--json` no longer falls through `runList`'s last-non-flag-wins loop as
a channel filter (previously `cbus list --json` would have printed "no peers
registered" at exit 0); and `--active`'s filter now tests a bool rather than
comparing against the padded display literal `"off   "`, a coupling the golden
rig exposed. `refuseRemoteJSON` (R15) refuses BOTH ways of asking for `--json`
remotely, since each would otherwise reach a different silently-wrong answer:
`list @host --json` reaches `runListRemote`, which never reads the remaining
args and drops the flag; `list --json @host` never reaches remote detection at
all (it inspects `args[0]` only), so the `@`-target is misread as a local
channel filter. `active <ch>@host --json` inherits the same refusal for free,
since `active` routes through the same `runList`. C1 fold from the M5.1
review: `name.go`'s quirk list no longer claims all four admissions await
tightening, since two of them now have one.

M5.3 (`6ba1624`) gives `whoami --json` one document shape whichever state the
session is in: an unjoined session gets the same keys with empty
`local`/`remote` arrays rather than a different document or the text path's
"not joined in this session" sentence, so a consumer parses one thing. The
exit code is preserved — still `1` when both collections are empty, matching
the probe semantics scripts already branch on — and `joined` is spelled out in
the body so a consumer reading only stdout never has to infer it from two
array lengths. `local` and `remote` are separate keys rather than one list
with a kind field, since they're genuinely different identities: a local
registration has no host, a remote from-default marker always does. The remote
fixture is written by `WriteRemoteMarker`, the same writer `cbus tail
<ch>@<host>` calls, rather than staged by hand; a marker with no local
registration still counts as `joined`, pinned as its own case since the flag
and the exit code have to agree in every combination. The C2 fold (`5f746bb`)
adds the missing audience to `jsonout.go`'s extensibility comment: the DTOs are
a public contract parsed by the oq9.5 menubar GUI, which is what makes "do not
rename these fields" actionable rather than a matter of taste — and closes
`cbus-oq9.4`, whose own notes already pointed here.

Docs: `command-reference.md`'s Name validity section documents the tightening
inline, mirroring `name.go`'s own two-of-four-admissions-tightened framing,
and gains `--json` subsections under `list`/`channels`/`whoami`; explicitly
NOT touched — the Local-arm-mechanics lines under Sec 3, which are
`cbus-2c8`'s separate open item. `behavior-spec.md` gains a new Sec 9.1, JSON
output contract, covering the same envelope/refusal/one-shape facts at the
mechanism level. Three new standing doctrines added to `roles/reviewer.md`,
all self-caught by the reviewer before shipping as wrong findings against
correct code. An absence claim from a partial read is not evidence — before
claiming something is missing, uncited, or undocumented, confirm the read
actually covered where it would live, since a range anchored at a symbol's
declaration excludes the doc comment sitting above it. This effort's second
partial-read miss (the first, a malformed legacy-envelope fixture in M5.1, was
also self-caught); the M5.2(b) review's own `n2` citation-missing finding is
the one this doctrine traces to, and is struck from the verdict — `n1` stands
as ruled. A mutation verdict is evidence only after the mutant is proven on
disk (assert the replace count, grep the mutated line, or check a non-empty
`git diff`) BEFORE reading the test result, and confirmed back at baseline
after: reviewing M5.2a, a heredoc mutation of the `"off   "` display literal
used space indentation against a tab-indented file and silently matched
nothing, while an unchained shell command in the same block still ran the go
test against unmutated code and printed all-PASS — readable as either
"mutation survived" or "golden insensitive," both wrong, since the run never
touched the code path under test (mutation testing's version of doctrine 12:
the run proves the harness executed, not what it tested). And an exit-code
assertion must run the binary bare or read `PIPESTATUS` explicitly, never `$?`
after a pipe: the M5.3 live smoke probed `whoami --json`'s unjoined exit code
with `cbus whoami --json | jq -c .; echo rc=$?`, which reports `jq`'s rc (0,
since the doc parses fine) rather than whoami's — `pipefail` would not have
caught this either, since it reports that some stage failed, not which, and
still reads 0 when the masking stage succeeds.

Reviewer verdict on the docs commit: CONDITIONAL APPROVE, two binding fixes
folded in before merge. F1: `behavior-spec.md` §9.1 mischaracterized
`ChannelRoster` as "the rename-path lookup" — it has no rename call site; it's
the formation-save roster reader (`formation_save.go:51`, called from
`formation_save.go:144` and `formation_plan.go:113`), corrected. F2:
`command-reference.md`'s all-digit-names bullet conflated two different
claims under one "still an open quirk" — name LEGALITY is genuinely still
open, but the `jset` int-coercion MECHANISM is bash-era only: the Go port's
`renameMeta` always writes `alias` as a JSON string (`store.go:383`, a
documented C-delta, port-map row 16), split into two sentences. Class-C fold
C3: the pre-existing "Extra args are silently dropped" line in the `whoami`
section, sitting one line above the new `--json` subsection, has been stale
since the P2.5 harness layer (`581b1c7`) — `whoami`'s `noExtra` guard makes
trailing junk a hard error, contradicting both the top-of-file delta list's
own item 6 (`cbus whoami junk`) and this milestone's own subsection; struck
through with a provenance note. Separately, the orchestrator ruled doctrine 14
(absence-from-partial-read) universal — it traces to two reviewer instances
plus an M4 coder variant, all in the same negative-claim family — and
propagated it to `roles/coder.md` and `roles/documenter.md` as their own
role-specific addition (14 and 11 respectively, since each file's list was a
different length); doctrines 15/16 stay reviewer-only and are marked
reviewer-confirmed per this verdict.

Same false-claim family, ruled in before the reviewer's confirmation pass
rather than deferred: Quirk index item 11 also claimed `channels`/`whoami`
silently drop extra args, alongside the genuinely-still-accurate
`hook-exit`/`hook-compact` half. Split: the first half struck through with the
same `581b1c7` provenance C3 used (both verbs now go through `noExtra` and die
on trailing junk, rc 1); `hook-exit`/`hook-compact` left as-is — verified
neither calls `noExtra` and dispatch never passes their args through.

[Files Changed]
cmd/cbus/jsonout.go (new), jsonout_test.go (new), list_golden_test.go (new),
name_tighten_test.go (new), whoami_json_test.go (new), main.go, main_test.go,
usage.go — cmd/cbus. internal/client/roster.go (new), storename_test.go (new),
formation_save.go, harness.go, spawn.go, store.go — internal/client.
internal/core/name.go, name_test.go — internal/core. docs/architecture/
command-reference.md (Name validity section, --json subsections under list/
channels/whoami, F2/C3 fixes, Quirk index item 11 split). docs/architecture/
behavior-spec.md (new Sec 9.1, F1 fix). roles/reviewer.md (three new standing
doctrines, 15/16 marked reviewer-confirmed), roles/coder.md,
roles/documenter.md (doctrine 14 propagated to each). simple_changelog.md,
detailed_changelog.md (this entry).

[Possible Ripple Effects]
`--json`'s field names are now a public contract consumed by the oq9.5 menubar
GUI — a future rename must bump `schemaVersion` or land as an addition, never
a silent rename. Any peer, channel, or formation with a leading dot or dash
created BEFORE M5 is untouched (`ValidStoreName` only gates new creation) — it
stays exactly as invisible to `list`/`channels`/`whoami` as before, cleaned up
only via the existing bad-name-agnostic `cbus unregister`/`prune` paths, not a
new one. `branchChannelFromGit` already restricted its output to
`[A-Za-z0-9._-]` before this landed, so the added `TrimLeft` of leading
dots/dashes is the only new derived-channel behavior change, and it affects
only repos whose basename itself starts with `.` or `-`.

[Testing Notes]
`name_tighten_test.go` exercises the tightening through the CLI door (`run()`),
not `client.Join` directly, across `join`'s channel and alias positions,
`rename`, and `spawn`/`branch`'s `--name` and channel positional — asserting
refusal AND that the store gained nothing, not just a nonzero exit.
`list_golden_test.go` pins `list`/`active`/`channels`' rendered bytes from a
REAL arm (a child `cbus tail` killed on cleanup) plus one hand-staged legacy v1
fixture (the only hand-built one, since only the retired bash v1 client ever
wrote a channel-level `meta.json`) — verified under mutation that a
list-polling arm-wait dies on its own precondition (10s timeout) where the
meta-polling wait fails the golden in under a second. `jsonout_test.go` and
`whoami_json_test.go` cover both remote-refusal flag orders, the
`legacyV1`/empty-`peers` shape, `listenerPid` omission, and joined/unjoined
`whoami` parity including the marker-only (no local registration) joined case.
Full suite green; C1/C2 folds landed same-session per the M5.1 review's own
class-C disposition, no re-review needed.

## [2026-07-19 19:23:43 UTC] [Client/Replay] M4 (cbus-8k9.4): durable replay cursor, follower self-identity, local displacement gate — closes cbus-8no and cbus-0r8

[Attempt #1]

Six commits (`e0ce7de`..`5c1fadc`), M4 of the go-port epic's Phase 3. Closes the
last two open items from port-map.md's D4/D5 rulings, both of which trace back
to the same root cause: the bash-era null-`listenerPid` tri-state could only
answer "resume at byte 0 or at END," and END silently discards whatever arrived
while nobody held the file.

N1 (`e0ce7de`) adds the durable replay cursor. A `.cursor` sidecar next to
`meta.json`/`inbox.jsonl` (never a meta.json field — meta is read-modify-written
as a whole struct, so a cursor field there would race a lost update against the
identity tuple it depends on) records `<dev> <ino> <offset>`, temp+rename
written, one writer. `resolveResume` is the whole decision table, and it is a
CONTRACT worth stating plainly, not just a code comment: **loss is silent and
unrecoverable; duplication is visible and self-evident.** Every ambiguous branch
in the table resolves toward duplicates, never toward silence. ABSENT (no
cursor-aware binary has ever read this peer) and CORRUPT (one did, and the
record is damaged) are deliberately different states — collapsing them would
send a damaged cursor down the migration path and seek END, which the table
forbids. Riders folded in as ruled: P1, join deletes `.cursor` beside its
existing inbox truncate (a join starts a new epoch; the previous epoch's cursor
is void — same-session join hits an early return before the cursor could
matter, cross-session join's `RemoveAll` is the actual delete path, and a
silently-failed `RemoveAll` is the narrow residue, not the in-place-truncate
mechanism an earlier draft of this comment claimed and later retracted). P2
keys the record on dev+ino, matching the live rotation check's strength. P6
takes identity from the OPEN FD, never a fresh path stat, making disagreement
with the rotation check structurally impossible. P5's migration self-heal is
asserted by test, not assumed: the follower writes its cursor immediately on
open, so the ABSENT/seek-END branch cannot recur for a peer once it has run.
Dedup itself (reconciling the duplicates this mechanism deliberately produces)
stays Phase 4 wire work — untouched here.

N2 (`33bd5e2`) gives a follower proof of which listener it is: its
`(listenerPid, listenerStart)` witness, already-existing state repurposed as a
generation number (only one process can hold a given pid+starttime pair). One
predicate — `check()` — closes four things at once: `--steal` displacement, the
`cbus-0r8` foreign-reopen leak, two arms racing the displacement gate (the
loser self-terminates instead of becoming a second permanent listener), and the
post-rename stale tail. Polarity is deliberate and frozen (R14): anything the
check cannot confirm reads NOT-MINE, which inverts the file-read leniency used
elsewhere in this codebase (a bad meta read there defaults to "still alive," to
avoid reaping a live peer on a torn read). The two policies are not in tension —
they protect against opposite failures. A false dormant here costs a quiet
window and a re-arm; a false continue streams another session's traffic into
someone else's terminal, which is the leak this rider exists to close. Cursor
writes are identity-conditional (P3 of this commit): a displaced or orphaned
follower must stop MOVING the cursor, not merely stop reading it, or it drags
the stealer's — or a recreated peer's — resume point past messages it never
delivered.

N3 (`7ede690`) adds the displacement gate itself: a second local `tail` on an
already-armed alias is refused (relay-style) unless the caller passes
`--steal`, which is lossless because the cursor belongs to the peer, not the
follower. The gate takes no lock and is not atomic by design (R-B) — the
self-correcting race N2's identity check provides is cheaper than the
wedged-alias recovery path a lock would trade for atomicity. Rider D7: one
stderr warning when there is no session id, on the verbs that actually record
or resolve identity (`join`, `send`, local `tail` — not `rename`/`leave`/
`whoami`), naming what sessionless mode actually loses rather than pretending
it's an error. Rider b1: cadence measured, not described — identity check
17.7us, cursor write 114.5us; a quiet follower performs one check per second and
ZERO writes (`TestQuietFollowerWritesNothing`); a busy one runs roughly 5
check+write pairs/sec, ~0.07% of a core. Rider b2: `main.go`'s "unreachable"
return and `ArmLocalTail`'s returns-only-on-failure contract were both made
false by dormancy — a nil return is now the designed exit path, and the source
comments say so; not duplicated here since docs describe behavior, not comment
history.

A same-day follow-up (`04dfbc8`) fixed the dormancy markers themselves: every
cause printed "re-arm to resume," which is true for exactly one of the four —
`pruned` needs a re-join first, `displaced` needs `--steal`, `renamed` only
answers to its new alias. `TestMarkerRemedyMatchesBehavior` checks the text
against what the code actually accepts in each state, not against a fixed
string, so the two cannot drift apart again.

Reviewer findings, both fixed same-session: **F1** (`5c1fadc`) — `causeRenamed`
was unreachable through the real flow. Rename MOVES the peer directory, so the
old follower's path stops resolving and it lands in `causeGone`, whose remedy
(re-join) is actively wrong post-rename (it resurrects the alias the peer just
vacated). The N2 comment's claimed mechanism (a cleared witness read at the
OLD path) was never true — that cleared witness lands at the NEW path, which
the old follower never reads — and is retracted; the test that "proved" it
manufactured the state by hand-editing a meta no real code path writes. Fixed
by detection: `findRenamed` scans the channel for a sibling recording OUR pid
with an empty witness (exactly what `renameMeta` leaves behind) and returns the
new address so the marker can name it — real rename detection, at most once per
follower lifetime, never on the poll path. **F2** (`c4b8743`) — the cursor
persisted `consumed` (raw bytes off the fd), which includes a partial line
still buffered; a re-arm could resume mid-frame, losing the head of a message
forever. Fixed by persisting `consumed - len(pend)`, the last frame boundary
actually emitted; `TestCursorNeverPointsMidFrame` asserts the boundary
invariant rather than a byte count. Rider n1, folded into the same commit:
`readCursor` mapped any read error to ABSENT, so an EACCES on a present cursor
sent an ever-armed peer down the migration path and silently skipped its
backlog — split on `os.IsNotExist` so present-but-unreadable takes the CORRUPT
path and replays instead.

Docs: behavior-spec.md §8.6 (replay) is rewritten, not amended, into an 8-row
resume table — rename, `--steal`, and a `--force`-into-dead-gap re-arm are
deliberately NOT three of its rows; each resolves to the ordinary "cursor
valid" row, which is the design point. §8.7's zombie-reattach hazard gets a
Go-side closure note. port-map.md's D4/D5 rulings, Phase 3 status block, and
Phase 3 paragraph are marked DONE with the real mechanisms; D7 corrected to
read "ships with M4 N3," not "at cutover." command-reference.md and
overview.md's living descriptions of rename's `cbus-8no` window, the
no-collision-gate quirk, and `--force`'s best-effort caveat are struck through
with closure notes rather than silently rewritten, so the record shows what was
believed before and why it changed. Two new standing doctrine entries added to
`roles/reviewer.md` and `roles/coder.md`: a red test is not proof — a red test
failing on the assertion aimed at is; and a hand-built test fixture proves a
mutation fails, not that the state it constructs is reachable — for any state a
test constructs by hand, ask which real code path writes it, and if none does,
the fixture is the finding (provenance: F1's own fixture, above). A third
doctrine addition bars any bare `pkill` pattern that could match live
infrastructure, scoping kills to harness-tracked pids. Standing doctrine 2 (the
re-arm-on-drop rule) gains the prune case: a re-arm failing with "no such
peer" means the peer was pruned, and the remedy is re-JOIN under your alias,
then re-arm — not a bare re-arm retry. These ship in-repo immediately;
installed fleets pick them up at the next `cbus selfupdate` release, not before.

[Files Changed]
internal/client/cursor.go (new), cursor_test.go (new), cursor_regression_test.go
(new), identity_follow.go (new), dormancy_test.go (new), cadence_test.go (new),
follow.go, follow_test.go, meta_rewrite_test.go, rename_invalidation_test.go,
store.go — internal/client. cmd/cbus/sessionless.go (new), main.go,
tail_inprocess_test.go (new) — cmd/cbus. docs/architecture/behavior-spec.md (§8.6 rewritten,
§8.7 closure note, new dated preamble note), docs/architecture/port-map.md
(STATUS block, D4/D5/D7 rows, Phase 3 paragraph, installer parenthetical),
docs/architecture/command-reference.md (Monitor-arming contract, tail sections,
sessionless-degradation, quirk index items 12-13, rename/send sections),
docs/architecture/overview.md (§5.5 decisions table, §6 known limitations) —
docs/architecture. roles/reviewer.md, roles/coder.md (three new standing
doctrines, doctrine 2 amended).

[Possible Ripple Effects]
Local replay behavior changes observably: a re-arm after any gap (rename,
`--steal`, a dead `--force` queue, a plain listener death) now redelivers
instead of skipping — sessions that relied on "re-arm always starts fresh" as
an implicit dedup mechanism will see duplicates, which is the stated trade.
Wire, presence, the relay, and remote `tail` are untouched. A `.cursor` file
now exists per armed peer under `$CBUS_DIR` — not covered by any existing
prune/cleanup path beyond what removing the peer directory already does.

[Testing Notes]
Full suite green on darwin and in the bookworm container gate. Reviewer
mutation-verified each rider independently (F1: dropping detection reproduces
cause 4 on a real Rename+prune flow; F2: restoring raw `consumed` fails the
70-vs-37 boundary case; N2: removing the identity check fails the
confidentiality and orphan tests, removing only the P3 write guard requires the
steal-overlap test specifically, since the rotation check intercepts the orphan
case first; the marker-remedy fix: restoring the uniform suffix fails 3 of 4
subtests). Both N3 gate tests are bounded (a regression hangs rather than fails
cleanly, since a missing gate becomes a blocking follower, not an error).

## [2026-07-19 05:48:18 UTC] [Client/Liveness] P3 compat tranche 2: structural (pid, starttime) identity, in-process follower, COMPAT items 1-2 deleted

[Attempt #1]

Second code tranche of the go-port epic's P3 phase (cbus-8k9.4), unblocked by
tranche 1's independently-removable items. Where tranche 1 deleted without
touching liveness mechanics, this tranche replaces the mechanics themselves:
compat-deletion-plan.md's items 1-2 (the Decision 2 re-exec and the raw inbox
spelling) are gone, and the argv-grep three-clause predicate is no longer how
liveness works for any peer this binary arms.

M1 (3865d52) adds procStartTime(pid), the structural half of a (pid, starttime)
witness -- darwin reads pbi_start_tvsec/tvusec out of proc_bsdinfo (offsets
verified against `ps -o lstart`), linux reads /proc/<pid>/stat field 22 prefixed
with the boot id (jiffies are boot-relative; $CBUS_DIR outlives a reboot). Wired
to nothing yet. The token is opaque by contract -- callers compare byte equality,
never parse it as a clock -- and a probe that cannot answer errors DEAD rather
than guessing (R2: proc-probe failure is dead, distinct from the meta-file read
leniency elsewhere).

B1 (4337f79) single-sources the token composition into starttime.go as pure
functions over injected bytes, so the writer that records a token and the
prober that checks one cannot drift apart, and makes the linux token's field
index, boot-id variation and newline-trimming provable from darwin instead of
argued for. Mutation-verified both constants; the mutation run also caught that
the darwin composition test was tautological (wrote and read at the same
constant, passed on a wrong offset) -- its fixture now hardcodes the ABI offsets
so it disagrees with the code when the code is wrong.

F1/F2 (b094b31): the first real linux runtime run (colima, bookworm) found
linux starttime is USER_HZ ticks (10ms) where darwin is microseconds, so a
child spawned in the same tick as a sibling carries a byte-identical token,
breaking the strict-ordering and cross-process-distinctness assertions --
correctly, since those assertions exist to catch a wrong field, and a `>=`
relaxation would pass on one. Fixed by construction in the shared startedChild
test helper (separate spawns by several ticks), not by loosening the
comparisons.

cbus-w33 (510b595), riding alongside: surfaceSweepBudget did not bound the
close sweep on linux -- exec.CommandContext kills only the direct child, and
dash (linux /bin/sh) forks the last command of a script where darwin's sh
execs it, so the kill landed on the wrong process and a wedged-ps test ran its
full 30s. boundedCmd now sets Setpgid and kills the process group, plus a
WaitDelay backstop. Verified in the tree (not assumed from the commit message):
this bounds every runOsascriptOut caller, not just close.go's four sweep sites
the commit names -- pane.go's paneSplitScript and paneGeometryScript
(spawn/branch/formation pane targets) route through the same Ctx variant and
are bounded too. The window/tab fork path is a SEPARATE function, runOsascript
(harness.go:387, plain exec.Command), not touched by this fix and still
unbounded -- a documenter query against the tree caught that the broader "every
osascript caller" framing didn't hold; ruled an open tracked gap, folded into
the umbrella item cbus-cih (now naming both unbounded sites: harness.go:349's
osaForkITerm window path and pane.go:147's tab path).

M2 (4458e83) makes listener identity structural: armMeta records
listenerStart, the opaque witness from the single composer; MetaListenerAlive
judges a peer on whether the process at listenerPid is still the process that
armed it, not on what its argv says. listenerIdentityHolds is the one place
that answers that question -- its structural and TRANSITION(P3T2) argv
branches are exclusive by construction (never `structural || argv`), so the
shim can't resurrect a listener whose recorded starttime says it isn't that
process. The argv read side survives fenced in the new
liveness_transition.go, because a follower armed by a pre-P3 binary has no
witness and its --inbox argv is the only ground truth about it; the write
side is gone outright (this binary never writes argv identity). Rename now
clears listenerStart ONLY (not listenerPid) -- clearing listenerPid too would
still read dead correctly but would flip the re-arm to a full replay, since
rename does not truncate the inbox. peerMeta carries listenerStart so every
rewriter preserves it -- a field absent from that struct is not a cosmetic
omission, encoding/json drops it, which would strip a live peer down to the
transition branch. Mutation-verified against four distinct wrong
implementations, each caught by a different test (no-fallback/structural-branch,
the D4 tri-state assertion, predicate+reap-exposure, and
TestClosePeerIgnoresARecycledListenerPidStructurally).

M3 (4d53d4e) deletes the re-exec whole: TailArgv, ParseTailFollower, the
hidden --inbox/--from flags, compatInboxPath, main.go's follower dispatch
branch, and the os.Executable/os.Environ plumbing that only existed to
survive the exec. ReplayMode's wire()/replayFromWire go with them. InboxPath
is now filepath.Join; the raw concatenation was never about file I/O, only
about byte-matching an argv, and that consumer is gone. metaInboxNeedle
survives alone, rebuilding the raw spelling independently, because a pre-P3
follower's argv is still on disk in live process tables. Adds a real-CLI
harness (the bounded-deadline exception to the never-run-tail-under-a-tool
doctrine) that arms `cbus tail` as a child and asserts the recorded
listenerPid IS the streaming process -- the assertion that would catch a
re-exec or fork coming back.

F3 (f853ff2), reviewer finding: on linux a dead-but-unreaped follower read
ALIVE -- /proc stat stays readable at state=Z with the ORIGINAL starttime
intact and kill -0 still succeeds, so the recorded token byte-matched a
process that had already exited, and the peer passed the send gate, survived
prune, and kept "receiving" broadcasts with nothing listening. This is a
regression of a pinned edge: the old argv clause caught it for free (a
zombie's cmdline is empty) and TestArgvClauseZombieDead has pinned
zombie=dead since the port. procZombie now guards listenerIdentityHolds above
the branch split, so the predicate and close.go's owner guard inherit the
same answer. darwin was already safe (proc_pidinfo errors for a zombie) but
that was a libproc accident, not a decision -- the guard makes it intended on
both platforms. Reproduced in bookworm before fixing: the new test failed on
both assertions, passed after.

[Files Changed]
- internal/client/procinfo_darwin.go, procinfo_linux.go: procStartTime
  primitive per platform (M1), reduced to syscall wrappers over starttime.go
  (B1).
- internal/client/starttime.go (new): single-sourced token composition,
  darwinStartToken/linuxStartToken as pure functions over injected bytes.
- internal/client/procinfo_test.go, procinfo_darwin_test.go,
  procinfo_linux_test.go, starttime_test.go: offset/field sanity checks,
  mutation-verified; F1/F2's tick-separated spawn helper.
- internal/client/liveness.go: listenerIdentityHolds (structural/TRANSITION
  branch split), procZombie guard (F3).
- internal/client/liveness_transition.go (new): TRANSITION(P3T2) argv
  fallback, metaInboxNeedle.
- internal/client/liveness_structural_test.go, meta_rewrite_test.go,
  rename_invalidation_test.go (new): M2's mutation-verified coverage.
- internal/client/follow.go: ArmLocalTail loses the re-exec (M3); InboxPath
  is filepath.Join.
- internal/client/store.go: armMeta records listenerStart; rename clears it.
- cmd/cbus/main.go: follower dispatch branch removed.
- cmd/cbus/tail_inprocess_test.go (new): real-CLI bounded harness,
  listenerPid-IS-the-streaming-process assertion.
- internal/client/close.go, pane.go, close_test.go: boundedCmd + Setpgid +
  WaitDelay on every runOsascriptOut caller (w33); close.go's owner guard
  also inherits procZombie (F3).
- internal/client/formation_apply.go: comment-only, no longer describes
  `cbus tail` as image-replacing (F3 rider, R11).
- docs/architecture/compat-deletion-plan.md: tranche-2 stamp, items 1-2
  marked deleted, TRANSITION(P3T2) documented as a new non-COMPAT artifact,
  grep-sweep and notes sections corrected (both were stale since tranche 1
  for items already gone then).
- docs/architecture/port-map.md: Phase 3 status block, Phase 3 bullet list,
  D1/D3 rows annotated DONE with commit refs; cbus-6lv (pidfd/kqueue) called
  out as explicitly deferred.
- docs/architecture/behavior-spec.md: Go-side-equivalences note corrected
  (argv grep/re-exec no longer describe this binary); new dated note on the
  structural liveness delta, the zombie regression-and-fix, and rename
  invalidation now being deliberate rather than accidental.

[Possible Ripple Effects]
- Any external tooling or script that greps a NEW peer's argv for its inbox
  path (the old bash-era liveness check) will no longer find it -- expected,
  and only reachable if a bash cbus process still exists somewhere to run
  that grep, which compat-deletion-plan.md's tranche-1/cutover history says
  is not the case on the MBP or NUC.
- A peer still armed by a pre-tranche-2 Go binary or a bash follower is read
  via the TRANSITION(P3T2) fallback, which has a one-release lifespan --
  upgrading two releases at once without an intermediate re-arm will read
  those peers as dead once the shim is removed.
- cbus-cih (broadened per ruling): window/tab fork (runOsascript,
  harness.go:387 and pane.go:147) still has no deadline; a wedged osascript
  there can still hang a branch/spawn call the way close's sweep used to
  before w33.

[Testing Notes]
- go build ./... and go test ./... green on darwin and in a linux/arm64
  bookworm container (colima) throughout the chain; F3's new case reproduced
  FAILING in the container before the fix, passing after.
- amd64 is compile-verified only (GOOS=linux GOARCH=amd64 go build ./...),
  not runtime-tested -- it rides the NUC deploy gate. Do not read "verified
  on linux" in this entry as covering amd64 runtime behavior.
- Recorded lesson from this tranche: "verified on darwin" is not evidence for
  process-state code (zombie reads, fork/kill semantics, argv/proc layout) --
  darwin and linux diverged twice here (w33's dash-fork stall, F3's zombie
  reads-alive) in ways darwin-only testing structurally cannot catch.

## [2026-07-19 04:30:20 UTC] [Client/Compat] P3 compat tranche 1: lastActivity-only grace, help-line drop, bash artifact retirement

[Attempt #1]

First code commit of the go-port epic's P3 phase. compat-deletion-plan.md's
own notes gate items 1-2 (follower re-exec + raw inbox spelling) on the
structural-liveness replacement -- argv-grep liveness still consumes them --
so this tranche deletes only the independently-removable items 3, 4 and 6.

Item 3: unarmedGraceElapsed no longer falls back to the meta file's mtime.
The stamp is authoritative: every Go join writes lastActivity (store.go
writes it at both meta-creation sites), so a readable meta without a
parseable stamp can only be a bash-era relic or a damaged file. New
semantics: missing meta = not dead (unchanged); stampless-but-readable =
past grace, i.e. prunable, and broadcast delivery now skips such peers
(the hook-compact harness caught exactly this -- its seeded watchers were
stampless and stopped receiving events until the seed helpers wrote stamps,
which mirrors what real joins do).

Item 4: the CBUS_PYTHON (default python3) env line is dropped from --help;
cbus-go has no python dependency. The usage comment now records two ruled
deltas vs the bash heredoc (CC_BRANCH was the first).

Item 6: bin/cbus, bin/cc-branch.sh deleted; scripts/p26_sweep.sh and
scripts/p26_rollback.sh deleted with them (both are bash-differential
harnesses that execute the bash client, meaningless once it is gone).
Bash rollback is now git-history recovery only.

Post-review hardening (gpt-5.2-codex via zen codereview): lastActivity
parsing relaxed from the frozen write layout to any RFC3339 form, so a
future format drift can never read a live peer as stampless-dead; the
stamp read moved into unarmedGraceElapsed (single ReadFile, no
stat-based TOCTOU) and a read error on a present meta now returns
not-dead instead of prunable -- only a readable, stampless meta dies.
TestPeerDead gained empty-file, invalid-JSON and RFC3339-offset cases.

[Files Changed]
- internal/client/liveness.go: unarmedGraceElapsed rewritten (mtime read
  deleted, stat-only presence check); lastActivity comment de-dual-write-d.
- cmd/cbus/usage.go: COMPAT(P3 #4) comment block replaced with the
  two-ruled-deltas note; CBUS_PYTHON clause removed from the env line.
- internal/client/liveness_test.go: TestPeerDead stampless cases inverted
  (stampless = dead now); mtime-non-influence regression cases kept.
- internal/client/store_test.go, internal/client/identity_test.go:
  seedPeerPid/seedMeta write a fresh lastActivity stamp, matching real joins.
- docs/architecture/compat-deletion-plan.md: tranche-1 execution stamped.
- bin/cbus, bin/cc-branch.sh, scripts/p26_sweep.sh, scripts/p26_rollback.sh:
  deleted.

[Possible Ripple Effects]
- Any still-existing pre-cutover stampless meta on MBP/NUC now reads dead:
  it gets pruned and stops receiving broadcasts. Intended; verify with
  cbus list after deploy if anything looks missing.
- Scripts or docs invoking scripts/p26_*.sh or bin/cbus will not find them;
  cutover-decision-package.md references them as historical evidence, which
  stays accurate as history.
- --help output changed by one line; anything diffing help against the bash
  heredoc must account for the second ruled delta.

[Testing Notes]
- go build ./... and go test ./... green on darwin (all packages, including
  relay conformance). The initial run failed 3 hook-compact tests, which was
  the semantic change working as designed on stampless seeded watchers; seed
  helpers updated to stamp, suite green.
- Not yet exercised: a live prune pass against a real pre-cutover relic
  meta (none exist on this MBP's CBUS_DIR to test against).

## [2026-07-19 00:55:31 UTC] [Docs] Fold-in: one more stale relay-presence claim, caught by spot-check

[Attempt #1]

The 00:52:59 UTC entry below fixed command-reference.md's presence table and
quirk 18 (both said relay-generated presence still strips `kind` -- false
since `cbus-ijx.5` phase 1 shipped 2026-07-14). A third instance of the same
stale claim survived the first pass: L397-398's follower-rendering bullet, a
different section describing the same fact, missed because it wasn't in the
punch list's cited line ranges for that file. Caught in the orchestrator's
spot-check, not by re-auditing -- reworded consistently with the other two
fixes: relay-generated `join`/`departed` cross since ijx.5 phase 1;
client-originated `POST /send` presence and `compact-pre`/`compact-post`
stay local-only. Swept the rest of the repo + dev-docs tier for any other
surviving copies of this claim after the fix; found none live (only this
entry's own new text and historical changelog rows describing what was true
when written, which stay as-is).

[Files Changed]
- docs/architecture/command-reference.md: L397-398 bullet reworded.

[Possible Ripple Effects]
- None -- docs-only, same fact already fixed elsewhere in the same file.

[Testing Notes]
- Repo-wide + dev-docs grep for "strips `kind`" / "presence never crosses"
  after the fix, confirming no other live instance remained.

## [2026-07-19 00:52:59 UTC] [Docs] Docs-audit remediation: v0.3.0 parity pass across both tiers, solo round

[Attempt #1]

Three independent auditors swept every doc surface against shipped v0.3.0
(main `e27ac1a`) and produced a consolidated punch list (17 repo-tier items,
10 dev-docs items). This was a solo documenter round -- no reviewer gate --
so every item was re-verified against source before writing, per standing
doctrine. Nothing on the list was rejected: all 27 items checked out true and
were fixed as described; the punch list's "NOT defects" section was left
untouched.

Repo tier (verified against internal/client/{pane,close,marker,formation_apply}.go,
internal/core/frame.go, relay/cmd/cbus-relay/main.go, cmd/cbus/usage.go):
- README.md: added `cbus close` to the CLI reference (near `unregister`),
  `pane` to the `/bus-branch` target enum and forking prose, `[child-alias]`
  to the `bootstrap` usage line.
- docs/architecture/command-reference.md: corrected the presence table and
  quirk 18 -- relay-generated `join`/`departed` presence crosses the relay as
  of `cbus-ijx.5` phase 1 (shipped 2026-07-14; `Reframe` renders `kind`, the
  relay drives it off the ws lifecycle); only client-originated `POST /send`
  presence and `compact-pre`/`compact-post` stay local-only. Added the two
  compact-pre/compact-post rows the table was missing.
- docs/architecture/protocol.md: fixed §4.4/§4.5 to match -- the relay framer
  does render a `kind` slot now, and the divergence-matrix row for `kind
  present` is identical on both paths, not "dropped" on the relay side. Added
  a one-line note that ownerPid ancestor matching is now argv[0]'s basename,
  `comm` only a fallback.
- docs/architecture/overview.md (the largest gap -- pre-pane era): added the
  `pane` row to the terminal-coupling table and fixed the `tab` row (targets
  the caller's OWNING window via UUID lookup now, not current-window); retired
  the "tmux is the only terminal-agnostic path" claim (pane is tmux-backed
  too); added compact-pre/compact-post to the presence decision row and to §6;
  added `cbus close` to the component map and a new §5.5 decision row (the
  only lifecycle verb that signals a peer's OS process); added the argv[0]
  ownerPid note in two places.
- commands/bus-formation.md: reworded the `save` description -- origin/model
  are auto-stamped from the birth-record for launcher-born peers, not hand-
  maintained; only role/profile/split truly are.
- detailed_changelog.md: restored a `## ` header a rebase-order move had
  stripped (the 20:23:27 UTC entry), and un-interleaved/reordered the
  19:17:24 and 19:26:00 UTC entries -- a prior edit had spliced the 19:26:00
  entry's body into the middle of the 19:17:24 entry without giving it its
  own header, and left both out of descending order. simple_changelog.md was
  independently verified fully ordered; not touched.

Dev-docs tier (~/dev-docs/projects/claudebus/, direct edits, no git):
- index.md + architecture.md: re-pointed orphaned pre-rebase SHAs `d52a264`/
  `058ea28` (not ancestors of main) to their on-main equivalents `f246e3b`
  (feat: pane target) / `ff7ceef` (fix: tmux<3.1 retry etc.); corrected a
  claim that an explicit `split` direction suppresses the 70%-first-split
  sizing "for the whole run" -- verified against `pane.go`'s
  `tmuxSplitArgv`/`forkTmuxPane` that only the main-vertical reflow
  suppression is run-level, the 70% skip is per-peer; labeled the current
  shipped release (v0.3.0 `40eaec2`; v0.2.0 `9c13055`; v0.1.0 `f833e19`) where
  neither file named anything past v0.1.0; fixed architecture.md's
  doc-refresh-note date range (07-14→07-17, when two of its own §10 rows are
  dated 07-18).
- behavior-spec.md + port-map.md (frozen pair): fixed one broken cross-link
  anchor each (index.md's heading moved from `--2026-07-17` to `--2026-07-18`)
  -- link repair only, no content added, per the standing ruling that these
  stay annotation-only.

[Files Changed]
- README.md, commands/bus-formation.md (repo, this commit)
- docs/architecture/{command-reference,protocol,overview}.md (repo, this commit)
- detailed_changelog.md, simple_changelog.md (repo, mechanical-fix commit)
- ~/dev-docs/projects/claudebus/{index,architecture,behavior-spec,port-map}.md
  (direct edit, no git)

[Possible Ripple Effects]
- None -- docs/changelog only, no code or test surface touched.

[Testing Notes]
- Every claim re-verified against the cited source file/line before writing,
  not transcribed from the punch list on faith (the list itself was compiled
  from three reports that did verify, but solo-round doctrine required a
  second look with no reviewer gate downstream).

## [2026-07-18 21:26:45 UTC] [Fix] `procZombie` was darwin-only -- broke the linux leg of `make dist`

[Attempt #1]

`close.go`'s `waitGone` calls `procZombie` to tell a zombie owner apart from a
genuinely-alive one. It was implemented against darwin's process APIs only;
every gate through this round's landing (unit tests, live door runs, the
reviewer's own checks) ran on darwin, so `GOOS=linux` was never actually
compiled until `make dist` cross-built the release. That build broke.

Fixed with a linux-specific implementation reading `/proc/<pid>/stat`'s
process-state field directly: `Z` is a zombie, any read/parse failure is
treated as not-zombie (the pid either doesn't exist or isn't inspectable,
neither of which is "confirmed zombie").

[Files Changed]
- internal/client/procinfo_linux.go (new): `procZombie` for linux via
  `/proc/<pid>/stat` state-field parsing.

[Possible Ripple Effects]
- None outside the linux build target -- darwin's `procZombie` is untouched.
- `make dist`'s cross-compile remains the only gate that actually exercises
  linux-specific code in this repo; a darwin-only dev/review loop won't catch
  the next one either.

[Testing Notes]
- Documenter-side: verified against the `40eaec2` diff directly (the new
  file, the state-field parse, the Z/unreadable handling) -- did not run
  `make dist` myself; the coder's own cross-build is the primary evidence
  per the commit message ("every gate ran on darwin so make dist was the
  first linux compile").

## [2026-07-18 21:09:13 UTC] [Docs] Amendment: pane-split claims corrected for chain-split anchoring

[Attempt #1]

Two entries from earlier today described formation apply's pane target as
always splitting off the applier's own surface. That was accurate when
written -- `ForkSpec.Anchor` was always empty for apply, same as
`branch`/`spawn` -- but chain-split anchoring (see the 20:23:27 UTC entry
below) changed apply's behavior under that field without changing the field
itself, so the claim is now wrong for apply specifically:

- The 19:26:00 UTC "dev-trio starter targets: tab -> pane" entry's ripple-
  effects line: "A fresh `cbus formation apply dev-trio` now splits all four
  peers out of the APPLIER's own pane/session." Since chain-split anchoring
  landed, only the FIRST peer's split is guaranteed to anchor on the applier.
  Later peers anchor on the largest-area candidate among the applier and the
  panes already created this run, so a 4-peer apply typically chains: peer 2
  splits the applier, peer 3 may split peer 2's pane instead if it is now the
  larger one, and so on. The applier stays large by the tie-break rule (ties
  go to the newest teammate, not the applier), not because it is always the
  anchor.
- The 18:38:09 UTC "pane: a fourth fork target..." entry's headline and body
  describe `branch`/`spawn`/`formation apply` together as all splitting "the
  CALLER's own" surface. That phrasing still holds for `branch`/`spawn` --
  their anchor is unconditionally empty (the caller), unchanged by this round
  -- but is no longer accurate for `formation apply`, which now computes a
  per-split anchor.

No prior entry's text is rewritten -- both stand as an accurate record of
what shipped that day. This note is the correction, not a silent edit.

[Files Changed]
- None -- documentation-only correction note.

[Testing Notes]
- N/A (changelog correction, not a code change).

## [2026-07-18 21:06:17 UTC] [Fix] `cbus close` false-succeeded on every live Go-era peer -- ownerPid derivation matched the wrong process name

[Attempt #1]

`OwnerPID`'s ancestry walk matched a candidate ancestor's kernel `comm`
against `claude`/`claude-*`. The Go client is a bun-compiled binary whose
kernel accounting name (`ucomm`) is its version string, not `claude` -- so
every Go-era peer registration recorded `ownerPid` as null, and `ClosePeer`'s
`pid == 0` check read that as "no live process," reporting "already gone"
without ever sending a signal. A live peer closed with `cbus close` looked
torn down and was not.

Fixed in two passes:
1. `ownerFromPid` (the walk, factored out of `OwnerPID`) now matches argv[0]'s
   basename first, `comm` kept as a fallback for any build where it still
   reads `claude`. `ClosePeer` derives the owner from the armed listener's
   ancestry when the stored `ownerPid` is null, rather than false-succeeding.
2. That fallback was itself unsafe: it walked ANY live `listenerPid`'s
   ancestry, so a recycled `listenerPid` now belonging to a process under a
   DIFFERENT claude session would have donated that session to the SIGTERM --
   closing a window nobody asked to close. Fixed by requiring the listener's
   argv to carry THIS peer's inbox path first (the same identity test
   `MetaListenerAlive` already uses), so a genuine armed follower always
   passes and a stranger reads as nothing to close.

Also folded in (reviewer rider n4): the surface sweep now runs under a 5s
deadline (`context.WithTimeout`) instead of unbounded `exec.Command` calls --
a wedged Apple Event was observed stalling ~45s in a detached shell; a
nonzero `ps` on the tty stays the normal "still busy, leave alone" path, and
only a sweep TIMEOUT is now treated as "left alone" rather than closing a
surface the sweep could not prove dead.

[Files Changed]
- internal/client/marker.go: `ownerFromPid` (factored out of `OwnerPID`)
  matches argv[0]'s basename via new `isClaudeName`, `comm` kept as fallback.
- internal/client/close.go: null-`ownerPid` fallback to
  `ownerFromPid(listenerPid)`, gated on `argvContains(listenerPid,
  metaInboxNeedle(...))`; a zombie owner reads as already-gone, not
  "recycled"; `SIGTERM` hitting `ESRCH` (died between the argv check and the
  signal) is idempotent success; `sweepSurface` takes a `context.Context`
  bound to a 5s `surfaceSweepBudget`.
- internal/client/pane.go: `runOsascriptOut` now wraps a context-bound
  `runOsascriptOutCtx`, used by the surface sweep.
- internal/client/close_test.go (new): the owner-walk regression suite --
  fake processes orphaned to PID 1 and `setsid`'d so the walk terminates at
  init rather than climbing into the real test-runner session; a fake
  reporting `comm "claude"` alone would pass through the old fallback and
  prove nothing, so cases assert argv-only identity too.
- internal/client/marker_test.go: `ownerFromPid`/`isClaudeName` coverage.

[Possible Ripple Effects]
- Every PRE-fix registration on disk (recorded before this lands) still has
  `ownerPid: null`; the listener-ancestry fallback covers those going
  forward, but a peer with BOTH a dead listener and a null ownerPid stays
  unreachable by `close` until it re-registers (join/re-arm) -- the same
  ceiling as before this round, not a new gap.
- The 5s sweep deadline means a very slow AppleScript environment (heavy
  Apple Event backlog) now leaves a surface open that an unbounded wait
  would eventually have closed -- traded deliberately: closing a surface the
  sweep could not prove dead risks closing the WRONG one.

[Testing Notes]
- Owner-walk regressions: comm-only match (old behavior, still passes as
  fallback), argv[0]-only match (the actual defect case), ancestry walk
  terminating at PID 1, null-ownerPid path deriving from a live listener, and
  the needle guard refusing a listener whose argv does NOT carry this peer's
  inbox path.
- ESRCH-during-TERM and zombie-owner paths both assert `Ok: true`
  ("already gone"), not a failure.

## [2026-07-18 20:23:43 UTC] [Docs] Multi-harness exploration doc

### [Attempt #1]

Research pass across cbus internals and three open-source harnesses (Codex CLI
0.144.1, xai-org/grok-build, sst/opencode 1.18.3) to map what it takes for each
to participate as a bus peer. File-based/relay-based only; no daemons proposed.

### [Files Changed]

- `docs/architecture/multi-harness-exploration.md` (new) — coupling audit (4
  spots: identity.go:26 env var, marker.go:60-80 ownerPid walk, harness.go
  launch argv, 8 bootstrap prompt constants), per-harness profiles, comparison
  table, 5 ordered increments, open questions.

### [Possible Ripple Effects]

- None (doc only). If acted on, first code change is the SessionID()/ownerPid
  seams in internal/client/identity.go and marker.go.

### [Testing Notes]

- Grok monitor tool + OpenCode plugin injection were verified by research probes
  (OpenCode end-to-end live); Codex Stop-hook continuation is documented but not
  executed — flagged as prototype-first in the doc.

## [2026-07-18 20:23:27 UTC] [Client/Forking] formation apply: pane splits chain off the largest-area candidate; envelope peers gain a `split` field

[Attempt #1]

`formation apply`'s pane target no longer always splits the applier's own
surface. `TerminalForker.Fork` now returns the created surface's id (iTerm2
session UUID / tmux pane id for `pane`, empty for `window`/`tab`/`tmux`), and
`ForkSpec` gains `Anchor` (pane only: the surface to split; empty still means
the caller, which `branch`/`spawn` keep using unconditionally). Apply tracks
candidates -- its own surface plus every pane created so far this run -- and
picks each new split's anchor via `PaneAnchor`: the largest-area candidate
(one geometry query, `tmux list-panes` or an AppleScript triple-loop over
iTerm2 sessions), ties going to the newest-created teammate over the applier.
The result is a self-balancing grid instead of the applier being shaved down
by every subsequent split; `PaneAnchor` degrades to `""` (split the applier)
rather than failing the launch when geometry can't be read.

Envelope peers gain an optional `split: auto|right|down` field (`right` =
side-by-side, `down` = stacked, `auto`/absent keeps the existing 2.2-ratio
heuristic on iTerm2 and today's 70/30-then-normalize behavior on tmux). It is
hand-maintained like `rolefile`/`role`/`profile`: `formation save` never
writes it. ANY peer in the file declaring an explicit direction suppresses
tmux's main-vertical normalize AND the 70%-first-split sizing for the WHOLE
run (`ForkSpec.NoNormalize`) -- a per-peer flag would let an `auto` sibling's
reflow stomp the layout the file asked for.

[Files Changed]
- internal/client/formation.go: `FormationPeer.Split` field + `split`
  round-trip in `fields()`; `validate()` gates it to `auto|right|down`.
- internal/client/formation_apply.go: `launchPeer` threads `Anchor`/`Split`/
  `NoNormalize` into each `ForkSpec` and returns the created surface id;
  `fileDeclaresSplit` scans the envelope once per run for the normalize
  suppression; the peer loop accumulates `created` and calls `PaneAnchor`
  only for `pane`-target peers.
- internal/client/pane.go: `PaneAnchor`/`pickLargest`/`parseGeometry`
  (backend-aware: `$TMUX` first, else iTerm2), `paneGeometryScript` (iTerm2
  geometry query), `osaForkPane`/`forkTmuxPane` now return the created id and
  accept `spec.Anchor`/`spec.Split`, `tmuxSplitArgv` gains the `right`/`down`
  `-h`/`-v` mapping.
- internal/client/harness.go: `TerminalForker.Fork` signature change to
  `(created string, err error)`; `ForkSpec.Anchor`/`Split`/`NoNormalize`.
- internal/client/spawn.go: updated for the new `Fork` signature (behavior
  unchanged -- spawn never sets `Anchor`).
- cmd/cbus/usage.go: `formation apply` usage gains the split-field note.
- Test-fake mechanical updates for the new `Fork` signature: cmd/cbus/
  formation_test.go, internal/client/formation_apply_test.go, harness_test.go,
  pane_test.go, cmd/cbus/pane_test.go.
- internal/client/formation_apply_test.go, pane_test.go: chain-split anchor
  table (`TestPickLargestPolicy`), the mixed-file suppress-normalize case
  (`TestApplyNoNormalizeIsRunLevel`, `TestApplyNormalizeStaysOnForAnAllAutoFile`),
  end-to-end apply through a fake tmux answering the geometry query
  (`TestApplyChainsPaneAnchors`: %0 -> %1 -> %2).
- internal/client/formation_save_test.go (new): split survives `save`
  unmodified, asserted against the file bytes and a reload.

[Possible Ripple Effects]
- `dev-trio` and any other pane-target formation now chains splits without a
  file change -- this is a behavior change under the existing `target: pane`
  field, not something a saved envelope opts into. See the amendment entry
  above for the two changelog claims this invalidates.
- A geometry query failure (osascript/tmux error, or a candidate whose id
  fails the shape check) degrades to `anchor=""` -- apply still splits the
  applier rather than failing the peer's launch.
- Saved runtime formations that predate this round have no `split` field;
  they round-trip as `auto` (today's heuristic), not a hard-coded direction.

[Testing Notes]
- `pickLargest` pinned as a table: largest area wins, ties go to the newest
  pane (`TestApplyAnchorsOnTheLargestPane` covers the applier-stays-anchor
  case when it is genuinely largest).
- End-to-end apply exercised against a fake tmux on PATH answering the
  geometry query, confirming the %0 -> %1 -> %2 anchor chain without a real
  multiplexer.
- Mixed-file case: one peer declaring a direction suppresses the tmux reflow
  for its `auto` siblings too (a per-peer flag would let a sibling stomp the
  file's declared layout).
- Split preservation asserted against `formation save`'s file bytes and a
  reload, not the returned struct -- the failure mode is the value never
  reaching disk, not the in-memory copy losing it.

## [2026-07-18 20:17:15 UTC] [CLI] New `cbus close <channel>/<alias>... [--force]` teardown verb

[Attempt #1]

`cbus close` ends one or more LOCAL peer sessions on request: read
`OwnerPid` from the peer's registration, `SIGTERM` it (graceful -- the
SessionEnd hook broadcasts `left` and removes the registration itself), wait
up to 5s, then sweep any terminal surface still standing once the tty reads
dead (`ps -o tty=` captured BEFORE the signal, since a reaped pid has no tty
left to read after; iTerm2 session closed by tty match, or `tmux kill-pane`
via `tmux list-panes -a`). `--force` escalates to `SIGKILL` after the grace.
An already-gone target (no live process) reports success, not an error, so a
scripted sweep can close the same roster twice. Targets resolve the way
`send`'s do -- a bare alias searches this session's own channels.

Refusals: this session itself (closing yourself is not how you exit --
refused before any signal), a remote `@host` target (refused at the CLI
layer, before `ClosePeer` runs -- `ClosePeer` takes a local `(channel,
alias)` and cannot express a host, so an accepted remote form would tear
down a same-named LOCAL peer instead), and a pid whose argv no longer looks
like a claude session (pid recycling -- signaling a stranger is worse than
failing the close). Registrations are never touched by close itself; the
SessionEnd hook owns the graceful path and lazy-prune owns the rest.

Every target produces exactly one stdout line in the order given, refusals
included; the exit code is 1 if any target failed.

[Files Changed]
- internal/client/close.go (new): `ClosePeer`, `sweepSurface`,
  `closeSurfaceScript`, `ttyOf`, `waitGone`.
- cmd/cbus/main.go: `close` verb dispatch, `runClose` (parses its own argv --
  the shared `splitVerbArgs` scanner stops at the first positional, which
  would swallow a trailing `--force` as a target here since targets are
  variadic), `closeOne` (target resolution + remote refusal).
- cmd/cbus/usage.go: `cbus close` usage block.
- cmd/cbus/close_test.go (new): CLI-layer argv parsing, target resolution,
  remote refusal, aggregate exit code.

[Possible Ripple Effects]
- `close` is local-only by design -- closing a remote peer requires running
  `cbus close` on its own host. No cross-machine teardown exists yet.
- A peer closed via `--force` skips the graceful SessionEnd broadcast (the
  process never gets to run it), so its registration is left for prune's
  10-minute grace rather than removed immediately.
- No dedicated `/bus-close` skill was added: `leave`/`unregister`/`prune`/
  `hook-exit` have no dedicated skill either, and `close` is the same shape
  (CLI verb, orchestrator-driven). Documented in CHEATSHEET.md's "Under the
  hood" list instead.

[Testing Notes]
- CLI-layer: argv parsing (`--force` in leading/trailing position, unknown
  flag rejection), target resolution (bare alias vs full address), remote
  refusal, aggregate exit code across mixed success/failure targets.
- Client-layer coverage for `ClosePeer` itself (the owner-walk paths) landed
  with the fix below (internal/client/close_test.go), not in this commit --
  see the 21:06:17 UTC entry.

## [2026-07-18 19:26:00 UTC] [Formations] dev-trio starter targets: tab -> pane

[Attempt #1]

[Files Changed]
- formations/dev-trio.json: all four peers (orchestrator, coder, reviewer, documenter) change "target": "tab" to "target": "pane". No other fields touched; template mode/rolefile/channel unchanged.

[Possible Ripple Effects]
- A fresh `cbus formation apply dev-trio` now splits all four peers out of the APPLIER's own pane/session (tmux: split-window grid normalized to main-vertical past two panes; iTerm2: osascript splits of the applier's session). Five panes total counting the applier -- on a small window tmux may refuse late splits with "no space", which surfaces per-peer and skips that peer, not the run.
- Hosts with neither $TMUX nor $ITERM_SESSION_ID (plain SSH to the NUC, Terminal.app) now hard-error per pane's contract where the old template opened tabs. Deliberate, Carlos-ruled: refusing beats splitting a frontmost surface; workaround is running under tmux or copying the envelope with per-peer targets.
- Saved runtime formations are unaffected -- this is the committed starter template only; existing envelopes keep whatever targets they recorded.

[Testing Notes]
- Template validates post-flip: formation oneOf accepts pane since f246e3b (unit: formation_test.go); a pane-target template already exercised live through apply --dry-run during the f246e3b review.
- Real apply smoke deferred to first live dev-trio launch on the new binary; the pane fork path itself carries the f246e3b..fef5cba review ledger (live tmux + iTerm2 door runs).

## [2026-07-18 19:17:24 UTC] [Client/Forking] pane/tab fork fixes: tmux<3.1 retry, tmux stderr surfaced, launcher tmpfile reaped on dispatch failure

[Attempt #1]

Three fixes folded in immediately after `f246e3b` landed, all in `ff7ceef`,
all caught before review:

1. **tmux<3.1 plain-split retry.** `forkTmuxPane`'s first split passes
   `-l 70%` (percentage pane sizing) to give the child 70% width beside the
   caller — but `-l <percentage>` needs tmux >= 3.1. On an older tmux the
   sized split fails; rather than fail the whole fork over a sizing nicety,
   `forkTmuxPane` now retries the SAME split with `preCount=0` (the plain,
   unsized form `tmuxSplitArgv` builds for anything other than exactly
   `preCount==1`) before giving up. The pane is the point; the 70/30 layout
   is a nicety that degrades gracefully.
2. **tmux stderr surfaced.** `exec.Command(...).Output()`'s error is just
   `exit status 1` — it discards tmux's own stderr message (e.g. "no space
   for new pane"). New `cmdStderr` helper type-asserts the error to
   `*exec.ExitError` and appends its captured `Stderr` (trimmed) as a
   `": <text>"` suffix, so `tmux split-window: <err>` now carries the actual
   reason.
3. **Launcher tmpfile reaped on dispatch failure.** `osaForkITerm` writes a
   self-deleting launcher script to a tempfile, then hands it to iTerm2 via
   osascript; the script deletes itself when it RUNS. Previously any
   osascript failure after the tempfile was written leaked it (nothing ever
   executed the self-delete). Now the dispatch call's error is captured
   (`ferr`) and, if non-nil, `os.Remove(path)` reaps the tempfile explicitly.
   This closes the leak for DISPATCH failures (osascript/pane/tab call
   errors) — a failure during SETUP (the write/`Close`/`chmod` calls before
   dispatch even runs) still leaks, since nothing has identified the file as
   needing cleanup yet.

Doc fold-in: command-reference.md quirk 36's launcher-leak claim ("A pre-exec
osascript failure leaks...") was written against pre-ff7ceef code and went
stale the same day — reworded to describe the pre-dispatch/dispatch split
precisely, with the `ff7ceef` anchor.

[Files Changed]
- internal/client/harness.go: `osaForkITerm` captures the dispatch result as
  `ferr`, reaps the tempfile with `os.Remove(path)` when `ferr != nil`.
- internal/client/pane.go: `forkTmuxPane` retries the split with
  `preCount=0` when the first (sized) attempt fails and `preCount==1`; new
  `cmdStderr` helper; `tmuxSplitArgv`'s doc comment updated for the retry's
  `preCount=0` call.
- docs/architecture/command-reference.md: quirk index item 36 reworded
  (this entry's fold-in).

[Testing Notes]
Documenter-side: verified directly against the `ff7ceef` diff (`git show
ff7ceef`), not transcribed from the review-verdict message unverified.

## [2026-07-18 18:38:09 UTC] [Client/Forking] pane: a fourth fork target that splits the caller's own surface, and a tab targeting fix

[Attempt #1]

`branch`/`spawn`/`formation apply` gain a fourth target, `pane`, alongside
`window`/`tab`/`tmux`: instead of opening a new terminal surface, it splits the
CALLER's own — Claude-Code-teammate style. The same commit fixes a real bug in
`tab`: it previously always landed in iTerm2's current/frontmost window, which
moves with the user's focus, so a peer forked from one window could land in
whichever window happened to be frontmost at fork time, not the caller's own.

Precedence for `pane` is tmux-first, matching CC's own teammate placement:
inside tmux (`$TMUX` set) the split is a tmux pane targeted at `$TMUX_PANE`
(focus-immune, unlike "current pane" which follows the user); otherwise, inside
iTerm2, the split targets the CALLER's own session, located by the UUID in
`$ITERM_SESSION_ID` — never "current window". Neither surface present is a hard
error: silently splitting whatever is frontmost would reintroduce the exact bug
`tab` fixes elsewhere in this commit, so refusing is the feature, not a gap.

tmux mechanics: `tmux split-window -d -P -F '#{pane_id}' -t $TMUX_PANE`, with
`-h -l 70%` on the first split (child gets 70% width, caller keeps a 30% leader
column); a best-effort `remain-on-exit failed` on the new pane (a crashed child
stays inspectable, a clean exit closes its pane); and past two panes the window
is renormalized to `main-vertical` with the caller's column resized back to
30%. All three post-split steps are best-effort (`_ = exec.Command(...).Run()`)
— older tmux lacks some of the forms.

iTerm2 mechanics: an AppleScript triple loop (windows→tabs→sessions) locates
the session whose `id` matches the UUID (AppleScript has no flat
session-by-id accessor and `whose` clauses are unreliable across nested
elements), then splits it — vertically (side-by-side) when the terminal is
wider than tall by more than 2.2x (a cell is ~2.2x taller than wide, so this
approximates a tiled grid instead of ever-thinner slices as splits repeat),
else horizontally. A UUID that matches no live session is a hard AppleScript
`error`, not a fallback — unlike `tab` below.

tab fix: it now finds the window OWNING the caller's session (the same UUID +
triple-loop lookup as pane, but `tell w to create tab`, not `tell s to
split`) and creates the tab there directly. Unlike pane, a stale or absent
UUID degrades to the historical `tell current window` behavior rather than
failing the fork — tab never depended on locating the caller, so there is no
reason to fail over the lookup instead of falling back.

Dispatch reuses existing plumbing rather than inventing new pathways: pane's
iTerm2 branch goes through the same self-deleting launcher-script indirection
as window/tab (`osaForkITerm`, the iTerm2-tokenizer workaround); pane's tmux
branch goes through the same quoted-one-liner `terminalCommand` as plain
`tmux` (tmux runs via `/bin/sh`, which does honor POSIX quoting). AppleScript
errors are surfaced via a new `runOsascriptErr` (`CombinedOutput`, not a bare
`.Run()`) so the pane/tab scripts' meaningful `error` text (e.g. "session not
found") reaches the caller instead of being swallowed as a bare exit status.

[Files Changed]
- internal/client/pane.go (new, 151 lines): iTermSessionUUID,
  findSessionScript (the shared triple-loop), paneSplitScript,
  tabInOwningWindowScript, osaForkPane, osaForkTab, runOsascriptErr,
  forkTmuxPane, tmuxSplitArgv (pure argv builder, testable without tmux),
  tmuxPaneCount.
- internal/client/harness.go: Branch/Spawn target validation extended to
  window|tab|tmux|pane; OSAForker.Fork gains the pane case (tmux-first
  precedence, hard error on neither surface); osaForkITerm's tab case now
  calls osaForkTab instead of the old inline current-window osascript.
- internal/client/spawn.go, internal/client/formation.go (+ spawn_test.go,
  formation_test.go): target enum extended to accept pane throughout.

[Possible Ripple Effects]
- tab's landing window changes for any caller running inside iTerm2 with
  `$ITERM_SESSION_ID` set (the normal case) — a fork that used to land
  wherever was frontmost now lands in the caller's own window. No CLI
  signature change; existing `cbus branch tab` / `cbus spawn tab`
  invocations are unaffected in form.
- pane is reachable via `branch`, `spawn`, and `formation apply`'s peer
  launcher (formation target enum updated too) but has no dedicated
  slash-command surface yet — bus-branch.md/bus-spawn.md's argument-hints
  now list it, no new skill file.
- Docs (this commit): commands/bus-spawn.md, commands/bus-branch.md,
  CHEATSHEET.md, docs/architecture/command-reference.md §9 (mechanism line,
  both target headings, target-validation prose + pane's precondition, the
  tokenizer-quirk paragraph's dispatch note, quirk index item 36 rewritten
  for the corrected tab behavior). dev-docs
  (`~/dev-docs/projects/claudebus/`) intentionally NOT touched — the
  feature is unmerged on this branch; a deferred-patch note (exact file,
  section, one-line change) was handed to the lead for post-merge
  application instead of documenting unshipped behavior as canon.

[Testing Notes]
Documenter-side: every precedence, error-string, and dispatch claim above
was cross-checked directly against internal/client/pane.go and the
harness.go diff (commit f246e3b) before being written, not transcribed
unverified from the kickoff message. No coder/reviewer test-run report
(`go test`/`-race`/live-fork verification) was relayed for this entry as of
this writing.

## [2026-07-18 18:35:17 UTC] [Core] New verb: cbus hook-compact <pre|post> — compaction notices

[Attempt #1]

Mirrors `hook-exit`'s SessionEnd wiring for Claude Code's PreCompact/PostCompact
hooks: a session about to lose (or having just lost) its in-context state now
broadcasts a `compact-pre`/`compact-post` `kind=presence` event to every LOCAL
channel it's joined to, so an orchestrating peer can force a checkpoint before
the context goes rather than discovering it after the fact.

**Mechanism.** `runHookCompact` (cmd/cbus/main.go) reads the phase (`pre`/`post`)
off argv and hands stdin to `HookCompact` (internal/client/harness.go), which
extracts `session_id` (stdin JSON first, env fallback second, silent no-op
third — same as `hook-exit`), resolves this session's registrations, and
broadcasts one presence event per joined channel via the existing
`BroadcastPresence` (skip=self, same convention as join/leave/rename).
Registration itself is untouched — unlike `hook-exit`, a compacting session is
still here. Text (`compactText`) is phase + an allowlisted `trigger`
(`manual`/`auto` only; anything else drops the parenthetical rather than
guessing) + a fixed tail: "about to compact (auto), in-context state will be
lost" / "compacted (auto), in-context state was reset". `PostCompact`'s
`compact_summary` field is deliberately never read — unbounded conversation
content that would otherwise land in every peer's inbox.

**Always exits 0, corrected rationale.** The verb never writes stdout (an
exit-0 hook's stdout is parsed as JSON) and always exits 0 — a `PreCompact`
hook exiting 2 blocks compaction. Trailing args past `phase` are ignored
rather than fatal; the first-draft rationale for that (dodging rc-2 blocking)
was wrong and caught in review — a strict no-extra-args `die` would exit 1,
which doesn't block compaction either. The honest reason: a hook must never
fail, and either hook exiting nonzero surfaces a hook-error notice plus the
first stderr line to the user — not a PostCompact-only behavior.

**PostCompact vs SessionStart(source=compact).** Both are documented,
independent post-compaction signals in the Claude Code hooks reference.
`hook-compact post` is wired to the dedicated `PostCompact` hook because it
fires in the *completing* context and needs no matcher, keeping the
`PreCompact`+`PostCompact` wiring symmetric — the `SessionStart(source=compact)`
alternative was considered and passed over for that reason, not because it
doesn't exist.

**Local only (D-zig-1).** The frozen `POST /send` relay contract carries no
`kind` field and the relay rebuilds stored lines from `{from,text,to,ts}`, so a
relayed notice would arrive as plain chat rather than presence. The honest fix
is a wire change plus a relay redeploy — deferred, not faked.

**Reviewer verdict:** APPROVED — two code commits, `19dd20b` (the verb) and
`4dd092d` (a follow-up: the users-door test checked rc and stdout but not
stderr, so it would have passed with chatter on the hook's stderr; added
assertions that both success paths stay silent on both fds), one review
finding (C1) scoped to tests only, no impact on documented behavior.

[Files Changed]
- `cmd/cbus/main.go` — dispatch case + `runHookCompact` (19dd20b)
- `cmd/cbus/update_check.go` — `hook-compact` added to the update-check skip
  list alongside `hook-exit` (hook targets stay quiet) (19dd20b)
- `internal/client/harness.go` — `HookCompact`, `compactText`, shared
  `hookInput`/`readHookInput` (refactored out of the pre-existing
  `hookSessionID`) (19dd20b)
- `cmd/cbus/hook_compact_test.go` (new, 19dd20b; stderr-silence assertions
  added, 4dd092d) / `internal/client/harness_test.go` (19dd20b) — per-channel
  broadcast, both phases, trigger allowlist, env fallback, no-session no-op,
  bad phase, remote markers untouched, stderr silence on both success paths,
  plus a users-door test that builds the real binary and drives it with the
  documented hook payloads
- `docs/architecture/protocol.md` — `compact-pre`/`compact-post` added to the
  presence event enum + event table; new "Compaction presence (D-zig-1,
  local-only)" bullet
- `docs/architecture/command-reference.md` — new §7 subsection (`cbus
  hook-compact <pre|post>`, mermaid sequence diagram, wiring JSON), exit-code
  table, stdin table, and quirk index (#11) updated
- `README.md`, `CHEATSHEET.md` — one-line verb mentions + presence paragraph
  extended
- `~/dev-docs/projects/claudebus/architecture.md` (direct-edit) — new
  "Compaction" data-flow subsection + 3 design-decision rows
- `~/dev-docs/projects/claudebus/index.md` (direct-edit) — new paragraph in
  "Shipped since cutover"

[Possible Ripple Effects]
- None to existing verbs — new dispatch case only; the one shared code path
  touched, `hookSessionID`, was refactored to reuse the new `readHookInput`
  behavior-preservingly, covered by the existing `hook-exit` tests too.
- Settings.json wiring is manual and explicitly NOT part of this effort — no
  PreCompact/PostCompact hooks are live on either machine until Carlos adds
  them by hand (shared across every CCS profile via `~/.ccs/shared`).
- Remote/relay compaction notices remain unimplemented (D-zig-1) — an
  orchestrator on a different machine gets no signal until the wire change
  plus relay redeploy this entry defers.

[Testing Notes]
- Verified against the actual diff (`git show 19dd20b`), not the commit
  message alone, before writing this entry.
- Reviewer approved the single commit; the one test-only finding (C1) was
  confirmed with the orchestrator to carry no doc-content implications before
  omitting further detail here.

## [2026-07-18 04:12:19 UTC] [Docs] Docs-refresh effort complete: dev-docs→repo promotion + F5 + ledger (cbus-yle)

[Attempt #1]

Closing entry for the docs-refresh effort (cbus-yle). Covers the promotion
assessment, its resulting port commit + fold, and the effort's full ledger.

**Promotion assessment.** With both tiers refreshed (M1-M3), the question was
what dev-docs content (`~/dev-docs/projects/claudebus/`, direct-edit) had no
repo-tier counterpart and deserved a repo home for a human reader. The
standing prior was that the 11 design-decision WHY rows added to dev-docs
architecture.md were the strongest candidates — repo docs tend to state
decisions but not why. Checked each of the 11 individually against
command-reference.md/overview.md/protocol.md by grep rather than trusting the
prior's count: 7 were already duplicated (M1-M3 had independently documented
the same rationale — OSAForker/tokenizer, birth-record anti-ghost-orchestrator,
formations runtime-shadows-template, `--role` spawn-only, flag-shape screening,
selfupdate verify-before-swap, sha-guard install), leaving 3 genuine gaps: the
`dev-trio` starter's 4-peer count (stated nowhere in the repo tier), formations'
plan-before-launch rationale (mechanism described, rationale not), and
install.sh's deletion-as-policy rationale (every commit says WHAT, none says
WHY delete rather than deprecate). A 4th candidate — a post-cutover commit
timeline — was ruled SKIP: changelogs are the repo's audit trail; the existing
frozen §9 timeline in overview.md is precedent for the format, not a mandate
to extend it. Verdict: the prior was right in kind (WHY rows are where repo
docs under-explain) but wrong in number — most WHYs had already been ported by
the refresh's own M1-M3 work, not left for a separate promotion pass.

**Port commit `e2c7832`** wrote the 3 ruled whys into `command-reference.md`
§10/§14. **F5, on the formations plan-before-launch rationale**: the
originating claim — first written into dev-docs architecture.md by the
documenter, faithfully paraphrased into the orchestrator's routing message,
transcribed by the coder into the port commit — read "a refusal can't strand
a half-launched fleet," implying a refusal halts the launch sequence. Wrong:
`formation_apply.go:139-141` shows `ActionRefuse` peers are recorded and the
loop `continue`s — launchable siblings still fork regardless of an earlier
refusal; only an all-refused/empty-launchable plan aborts pre-launch (D13,
`:124-135`, so a silent "converged 0 peers" is never reported as success). The
reviewer caught this by reading the actual code path rather than trusting the
chain of paraphrases, fixed it in `8779444` (the real guarantee: every
refusal is decided and reported before any launch, so it never interrupts the
sequence midway — refused peers are skipped, the rest come up, a re-run
reconciles), reworded the `dev-trio` note per n7 ("the name alone suggests
three"), and the coder adopted a new standing rule — verify a mechanism claim
against the actual code path before writing it, especially a claim received
as a paraphrase of a paraphrase — which the reviewer confirmed matches its
own rule 9. The documenter independently found and fixed the same wrong
framing at its origin: dev-docs architecture.md's design-decision row and
index.md's formations paragraph both carried the "strand a half-launched
fleet" claim and both are corrected in this same pass, so the error doesn't
survive anywhere it was written.

**dev-docs §4.12 sync-back**: ruled in separately — port-map.md's own
tokenizer-rationale wording ("AppleScript `do script`") was one class of
imprecision behind D30's repo-side fix (33b4565: the `command` parameter of
`create window`/`create tab` specifically, not `do script` generally).
Tightened both mentions (the primitive-inventory row, the Preserve-forever
entry) to match.

**Effort ledger, cbus-yle complete.** 14 repo commits across M1-M3 + the
promotion pass: `17a3ab1 f13ab49 2951c0a 8703a82 83cfe64` (M1) ·
`f628283 de59683 ad45c78 33b4565 bdc2249` (M2) · `be50b2f e4a2849` (M3) ·
`e2c7832 8779444` (promotion + F5). Four dev-docs files refreshed direct-edit
(index.md, architecture.md, behavior-spec.md, port-map.md) — no hashes, per
tier. Findings F1-F5 all confirmed; micro-notes n1-n7 on record. Both doc
tiers now current against everything shipped 2026-07-14→07-18: spawn family,
formations, birth-records, distribution, terminal coupling, retired
installers.

[Files Changed]
- `docs/architecture/command-reference.md` — 3 promoted whys (`e2c7832`); F5
  correction + n7 reword (`8779444`).
- `~/dev-docs/projects/claudebus/port-map.md` (direct-edit) — §4.12 tokenizer
  wording tightened, 2 mentions.
- `~/dev-docs/projects/claudebus/architecture.md` (direct-edit) — F5's wrong
  framing corrected at its origin (design-decision row).
- `~/dev-docs/projects/claudebus/index.md` (direct-edit) — same F5 correction
  in the formations paragraph.

[Possible Ripple Effects]
- None expected — this closes the effort. Both tiers are now the source of
  truth for the 2026-07-14→07-18 surface; a future feature should update both
  as it ships rather than accumulate another multi-day gap.

[Testing Notes]
- All docs-only; no code or test surface touched by this entry's commits.
- F5 verified independently against `formation_apply.go:124-141` before
  writing this entry, not taken from the fold commit message alone.

## [2026-07-18 03:58:02 UTC] [Docs] Repo docs/architecture refresh, M2: protocol, compat-deletion-plan, cutover-package, port-map (cbus-yle)

[Attempt #1]

Second milestone of the docs-refresh effort (cbus-yle). Four commits reviewed
as one batch alongside M1's two fold commits; three approved outright, one
conditional on two findings, both folded into this entry per the
hold-for-verdict rule.

`f628283` documented `protocol.md` §2.2's Go-client-only meta.json fields:
`origin`/`model` (the launcher-stamped birth record) and `lastActivity` (the
D3 grace-clock timestamp), all `omitempty` so bash-written and pre-birth-record
metas stay byte-identical to bash's tolerant reader (the A3 freeze). Also
documents the join stamp map (`birthForJoin`): a reservation-reclaim inherits
the placeholder's stamped origin/model, a resume-rejoin preserves the existing
values, and anything else is a takeover reading as `joined`/unknown — and why
`formation apply`'s `resume` mode never lays down a reservation (a placeholder
would win over the resumed session's own sid and clobber its preserved origin).

`de59683` updated `compat-deletion-plan.md`'s status banner and item 6: the
legacy installers are retired (`de07cbe`), so they're no longer the rollback
procedure or a pending P3 deletion — the remaining bash artifacts narrow to
`bin/cbus` + `bin/cc-branch.sh`, and rollback is now a manual copy.

`ad45c78` fixed `cutover-decision-package.md`'s rollback procedure, which still
told a reader to run `./install.sh` — retired. Step 1 now reads: copy `bin/cbus`
over `~/.local/bin/cbus` (or recover from git history), with the rest of the
package's installer references left as the recorded P2.6-readiness snapshot,
flagged as such in the banner.

`33b4565` (CONDITIONAL) tightened `port-map.md` §4.12's tokenizer rationale
(it's specifically iTerm2's `create window`/`create tab` **`command` parameter**
that self-tokenizes, not `do script` generally — cross-referenced to
`harness.go`'s `osaForkITerm` and overview.md's Terminal-coupling section) and
reframed §5's NUC-propagation note for the post-cutover, post-distribution
world (`install.sh` retired, NUC updates via `cbus selfupdate`). That reframe
overreached on one point:

**F3 (binding):** §5 claimed the release flow (`get.sh` + `selfupdate` +
sha-guarded install verbs) meets all three installer-design goals the original
note set, including "SessionEnd hook wiring." False — `get.sh` and `selfupdate`
touch no settings files; hook wiring stays a manual `settings.json` edit
(`cbus hook-exit`, command-reference §7). Fixed in `bdc2249`: dropped the hook-
wiring claim from the "met" list, keeping only the two goals actually
delivered (version stamp, mode-agnostic placement).

**F4 (binding, overview.md — pre-existing, not introduced by 33b4565):** the
presence design-decision row still read "remote presence does not work yet —
the relay strips `kind`," filed against `cbus-ijx.5`. Stale since `cbus-ijx.5`
shipped 2026-07-14: the relay now renders `kind` and generates join/departed
from the ws connect/disconnect lifecycle (protocol.md §8, verified current in
this same M2 pass). Reworded in `bdc2249`: what's actually still open is ijx.5
phase 2 — client-originated `leave`/`rename` and offline catch-up.

The dev-docs tier held its own mirror of both findings rather than writing
ahead of this fold: F3 against dev-docs port-map.md's M10/Phase-2 rows (ruled
NOT a mirror — those rows correctly describe the P2-era installer's real
hook-wiring check, an era D29 protects, distinct from the repo tier's "met by
the release flow" claim) and F4 against dev-docs behavior-spec.md §5's
"Remote presence does not exist" line (a genuine mirror, true as of its 2026-
07-12 freeze date but misleading in present tense — annotated
`[SUPERSEDED 2026-07-16: cbus-ijx.5 shipped...]` in place rather than rewritten,
consistent with the frozen-tier treatment already applied to port-map.md §0).

[Files Changed]
- `docs/architecture/protocol.md` — §2.2 Go-client meta fields + birth-record
  mechanism + join stamp map (`f628283`).
- `docs/architecture/compat-deletion-plan.md` — status banner + item 6
  (`de59683`).
- `docs/architecture/cutover-decision-package.md` — rollback procedure
  (`ad45c78`).
- `docs/architecture/port-map.md` — §4.12 precision + §5 NUC-propagation
  reframe (`33b4565`); F3 fold drops the hook-wiring claim (`bdc2249`).
- `docs/architecture/overview.md` — F4 fold rewords the presence row
  (`bdc2249`).
- `~/dev-docs/projects/claudebus/behavior-spec.md` (direct-edit tier) — F4's
  mirror sentence annotated `[SUPERSEDED 2026-07-16: ...]` in place, same pass.

[Possible Ripple Effects]
- Hook wiring being manual (not automated by any current or planned
  distribution surface) is now stated in three places that must stay in sync
  if that ever changes: port-map.md §5, command-reference.md §7, and the
  behavior-spec.md drift context — a future automation of hook wiring needs
  all three touched, not just the installer code.
- Remote presence's real open item (ijx.5 phase 2: client-originated
  leave/rename) is now named consistently in overview.md and protocol.md;
  anything still citing "remote presence does not work" as a blanket
  statement is now the actual stale claim.

[Testing Notes]
- All five commits (four M2 + the bdc2249 fold) are docs-only; no code or test
  surface touched.
- F4's "relay renders kind" claim was independently checked against
  protocol.md §8 in the same pass, not taken on faith from the M2 commit
  message alone.

## [2026-07-18 03:56:39 UTC] [Docs] Repo docs/architecture refresh, M3: README fixes + architecture-docs pointer (cbus-yle)

[Attempt #1]

Third milestone of the docs-refresh effort (cbus-yle). Two commits, both
APPROVED — reviewer reproduced R1/R3/R5 (skills exist on disk as named,
every link in the new section resolves, the meta.json anchor resolves,
code fences balance).

`be50b2f` fixed three README inaccuracies unrelated to the shipped-surface
gap list but found along the way: the intro pointed at `/branch-term` (the
generic, non-bus-joined terminal-fork skill) where it meant `/bus-branch`
(the bus-aware skill the rest of the README's example actually depends on —
a fork opened via `/branch-term` alone can't report back over cbus); the
CLI-reference env block was missing `CBUS_REPO` and `CBUS_UPDATE_CHECK`,
documented only in Install prose; and the How-it-works `meta.json` field
list read as exhaustive without a pointer to the `origin`/`model` birth-record
fields added since.

`e4a2849` added a "distribution / self-update" block to the CLI reference
(`selfupdate`, `install-commands`, `install-roles` — previously documented
only in Install prose, not the verb list) and a new "Architecture & reference
docs" section linking `overview.md`/`command-reference.md`/`protocol.md`/
`port-map.md`, which the README had never linked before (only
`compat-deletion-plan.md` got an inline mention, and only in one place).

[Files Changed]
- `README.md` — `/branch-term`→`/bus-branch` intro fix, env-block additions
  (`be50b2f`); distribution CLI block + new architecture-docs section
  (`e4a2849`).

[Possible Ripple Effects]
- The new "Architecture & reference docs" section is the first link from
  README.md into `docs/architecture/` — anything that renames or moves those
  four files now needs a README update too, not just cross-links among
  themselves.
- `/branch-term` itself still exists as a skill (generic fork, no bus join) —
  the fix only corrects README's own example to point at the bus-aware skill
  it actually narrates; it does not deprecate `/branch-term`.

[Testing Notes]
- Docs-only; reviewer live-checked all four new links resolve and that the
  named skills exist on disk under their corrected names.

## [2026-07-18 03:50:50 UTC] [Docs] Repo docs/architecture refresh, M1: help text, overview, command-reference for spawn/formations/distribution (cbus-yle)

[Attempt #1]

First milestone of the docs-refresh effort (cbus-yle): the `docs/architecture/`
tier had gone stale against everything shipped 2026-07-14 through 07-18 (spawn
family, formations, birth-records, distribution, terminal coupling), same gap
list the dev-docs tier (`~/dev-docs/projects/claudebus/`) was refreshed against
in parallel. Three commits, two APPROVED outright, one CONDITIONAL, both
findings folded into this entry per the hold-for-verdict rule rather than
written separately.

`17a3ab1` fixed `cmd/cbus/usage.go`'s `formation save` help text: it claimed
model/role/origin/profile are all hand-maintained and the store records none —
false since the birth-record landed, `save` captures origin/model from the
launcher's stamp when recorded. Text-only, no behavior change.

`f13ab49` rewrote `docs/architecture/overview.md`'s component map (dropped
`install.sh`/`install-cbus-go.sh` as live installers, retired per `de07cbe`;
added Formations/Roles/Distribution rows; CC-integration row now lists five
slash commands and native spawn/branch forking) and added a new "Terminal
coupling: the `TerminalForker` seam" subsection: `OSAForker`'s iTerm2
window/tab path (with the self-deleting launcher-script workaround) versus
the terminal-agnostic tmux path. Also reframed the prior-art pointer (dropped
the now-inaccurate "sole surviving copy / scratchpads gone" framing) and swept
residual install-drift and slash-command-count mentions. APPROVED, but carried
one class-C riding F1's fix below (same false attribution, same fold commit).

`2951c0a` rewrote `docs/architecture/command-reference.md` (§9 forking/spawning
gains spawn + `--model`/`--name`/`--role`, native `TerminalForker`/`OSAForker`,
drops the retired `cc-branch.sh` helper path; new §10 formations — the full
verb set, the `cbus-formation/v1` envelope, birth-record capture, the
list-vs-committed-templates discoverability seam; new §11 distribution —
selfupdate, install-commands/install-roles, the `CBUS_UPDATE_CHECK=1` hint;
renumbers historical/retired/deprecated/quirks sections by +2; reframes
python3/CC_BRANCH as bash-era; fixes the STATUS banner for both retired
installers; adds `/bus-spawn` and `/bus-formation` slash entries). CONDITIONAL
on two findings:

**F1 (binding, both f13ab49 and 2951c0a):** both files quoted "quoting cruft"
as `port-map` §4.12's own words and claimed §4.12 currently mislabels the
launcher shim that way. Both claims were false — §4.12 was corrected in place
back at `dab6726` (2026-07-13), and "quoting cruft" never appeared in
`port-map.md` at all; it's `harness.go`'s own paraphrase of the
pre-correction item, not a quote of anything port-map ever said. Confirmed
three independent ways before the fold: the reviewer's `git log -S`, the
orchestrator's routing note, and the documenter's own direct grep across
`port-map.md` — all three found zero hits. Fixed in `8703a82`: both mentions
replaced with a plain cross-reference to port-map §4.12 recording the same
rationale.

**F2 (class-C, 2951c0a only):** quirk 36 in the consolidated registry sat
under the new "(bash era)" binding header but mixed eras — the tmpfile leak
on a pre-exec `osascript` failure is current Go behavior (`osaForkITerm`
skips cleanup on that path), while only the stdout-errors half is genuinely
bash-era (the Go client dies to stderr, `main.go:110`). Fixed in `83cfe64`
by splitting the quirk into its two era-scoped halves.

The dev-docs tier's parallel draft (index.md's "Shipped since cutover" section,
architecture.md's new design-decision rows and terminal-coupling entries) was
grounded in the same source facts throughout and never repeated either
misattribution — no rework needed there once these folds confirmed.

[Files Changed]
- `cmd/cbus/usage.go` — formation-save help text corrected (`17a3ab1`).
- `docs/architecture/overview.md` — component map, new terminal-coupling
  subsection, prior-art reframe, residual cleanups (`f13ab49`); F1 fold
  removes the misattributed quote (`8703a82`).
- `docs/architecture/command-reference.md` — new §9-§11 content, section
  renumbering (+2), STATUS banner fix, new slash entries (`2951c0a`); F1 fold
  (`8703a82`) + F2 fold (`83cfe64`).

[Possible Ripple Effects]
- Section renumbering in `command-reference.md` (historical cc-branch.sh
  §10→§13, retired installers §11→§14, deprecated §14→§15, quirks §15→§16)
  shifts every in-repo cross-reference by two; anything outside this commit
  range citing the old numbers is now off by two.
- `docs/architecture/port-map.md` itself is untouched by this milestone — its
  §4.12 correction (`dab6726`, 07-13) is what both fold commits point back to,
  not something this pass changed.
- dev-docs tier (cbus-yle, same effort) already finalized its mirroring
  sections against these facts; no follow-up write needed there.

[Testing Notes]
- All five commits are docs/help-text only; `17a3ab1`'s own commit message
  notes `formation_test.go`'s substring assertions are unaffected; no other
  test surface touched.
- F1 verified independently three times (reviewer git-log search, orchestrator
  routing, documenter direct grep) before the fold landed — no reliance on a
  single check.

## [2026-07-18 03:03:18 UTC] [Distribution] Legacy installers retired

[Attempt #1]

Carlos-directed removal of `install.sh` and `install-cbus-go.sh`, the
decision the distribution effort's D24 ruling explicitly deferred ("their
retirement is a separate call"). The call came after v0.1.0 shipped and
the release path was live-verified end to end on both machines — the two
scripts' jobs no longer exist: first installs are `get.sh`, updates are
`cbus selfupdate`, and the transitional side-by-side era is over.

[Files Changed]
- `install.sh`, `install-cbus-go.sh` — deleted (git history and the
  pre-scrub bundle retain them).
- `README.md` — the legacy-installers note now records the retirement and
  the remaining rollback reality: `bin/cbus` (the retired bash client)
  stays until P3, and rolling back to it is a manual copy over
  `~/.local/bin/cbus`.
- `get.sh` — header note updated (no longer claims the two are untouched).
- `docs/RELEASE-CHECKLIST.md` — "Not part of this" section updated to the
  retirement.

[Possible Ripple Effects]
- The NUC never-run-install.sh footgun (bash rollback) is now structurally
  impossible — the script is gone from the tree.
- `docs/architecture/` still references both installers as present
  (overview, command-reference §12, port-map, compat-deletion-plan,
  cutover-decision-package); those pages were already flagged stale and
  the references ride the cbus-yle docs-refresh.
- P3 homogenization (cbus-8k9.4) loses one of its deletion items — the
  bash artifacts remaining in scope are `bin/cbus` and `bin/cc-branch.sh`.

[Testing Notes]
- `git grep` over the live tree confirms the only remaining references are
  historical (changelogs) and the architecture tier noted above.
- Suite untouched by the change (script deletions + doc edits only).

## [2026-07-18 01:40:33 UTC] [Distribution] Bootstrap installer, release checklist, and install docs (M5) — distribution complete

[Attempt #1]

Fifth and final milestone of the distribution effort (cbus-7sg), approved
with one binding finding (C7) and one class-C finding (c8). This entry
covers 1a93755 together with C7's fold-in fix (28ffbff) and c8's fold-in
fix (cd3d1a0), per the hold-for-verdict rule, and closes with the effort's
ledger — this is the record's last write for this effort.

1a93755 adds `get.sh`, the first-install bootstrap: it downloads the
release binary via `gh` and installs the skill commands and role prompts
it carries, as one `curl | sh` step. This is deliberately a third,
distinct installer alongside the two that already existed and stay
untouched: the retired `install.sh` (bash rollback) and the transitional
`install-cbus-go.sh`. It reads the repo slug from `$CBUS_REPO` at
bootstrap time rather than baking one into the committed script (the same
never-commit-a-personal-slug posture M1's `repoSlug` plumbing already
established), and refuses clearly, naming the missing variable, when it's
unset.

`docs/RELEASE-CHECKLIST.md` is the S10 deliverable this milestone was
building toward: a ledger of every path in this effort that touches a
real GitHub release and therefore cannot be exercised until one exists.
It names the selfupdate round-trip (on both a Mac, exercising the
same-filesystem rename, and the NUC, whose tmpfs-backed `/tmp` forces the
cross-filesystem fallback leg), the bootstrap itself, the update-check
poll, and `make release`, with the Carlos-gated sequence that has to run
first (history scrub, private remote, tag, `make release`, then bootstrap
each machine). The point of the file is that nothing gh-facing in this
effort gets silently counted as tested when it wasn't — it is named,
sequenced, and left for a human pass after the quiesce window.

README and the cheat sheet learn the full release-install path in this
same milestone: `get.sh` for first install, `selfupdate` thereafter, the
sha-guarded `install-commands`/`install-roles` verbs, `$CBUS_REPO`, and
`CBUS_UPDATE_CHECK`, with the checklist linked. Docs ship inside the
milestone that introduces the feature, not trailing it in a later pass.

C7, the binding finding: `get.sh --clobber` downloaded the new binary
directly onto the path of the already-installed `cbus`. A run that died
mid-transfer, or that fetched a truncated or otherwise bad asset, would
leave a damaged binary in place of a working install — the exact failure
class M3's `selfupdate` was designed from the ground up to make
impossible. Fixed in 28ffbff: `get.sh` now fetches to a sibling temporary
file on the same filesystem, verifies the temp file actually runs before
trusting it, and only then atomically moves it into place — the identical
never-damage-a-working-install order `selfupdate` already followed. The
coder's own self-report is carried here rather than smoothed into a
generic "fixed": this was its own M3 discipline (temp, verify, atomic
move) not carried over to a sibling surface that needed the identical
protection, and it was found and fixed inside the same review cycle
rather than shipped and found later. Reviewer confirmed the fix live: it
planted a genuinely working binary, ran a `get.sh` invocation engineered
to fail partway through against it, and confirmed the original survived
checksum-identical afterward, with its trap-cleaned temporary file gone —
not merely that the code looked right, but that a real failure left the
real install untouched.

c8, folded in via cd3d1a0: the release-asset name format
(`cbus-<os>-<arch>`) lived in a third, unguarded place — `get.sh`'s own
`BIN="cbus-${OS}-${ARCH}"` — that the existing asset-name pin (M3's S5
test, which already covered the Makefile and the Go client) did not
reach. The pin now also greps `get.sh`, so a format change there fails
the build the same way a Makefile or client-side change already did. The
reviewer's own mutation of the format in `get.sh` confirmed the new pin
actually catches it.

Reviewer pass highlights for the record, beyond the two findings: the S10
checklist (`docs/RELEASE-CHECKLIST.md`) was verified complete against its
own pre-registered list of gh-facing paths, including a line for running
the formations live smoke against the released binary rather than a
manually built one. S8 (this repo's standing coverage/quality gate) came
back clean across the entire commit range of the effort, not spot-checked
on this milestone alone. Both of M3's `gh`-error remedies — the download
path's original stderr pass-through and c6's fix to the release-view
path — were live-probed again as part of this pass, not assumed still
correct from M3's own review. Docs were checked to match behavior in both
directions: every doc claim was checked against what the code actually
does, and every behavior the code has was checked for a corresponding doc
line, rather than only checking one direction.

[Files Changed]
- get.sh (new, 1a93755; hardened in 28ffbff): the bootstrap installer,
  the temp-verify-atomic-move sequence.
- docs/RELEASE-CHECKLIST.md (new, 1a93755): the S10 gh-facing-paths
  ledger.
- README.md, CHEATSHEET.md (1a93755): the release-install path
  documented alongside the feature.
- cmd/cbus/selfupdate_test.go (cd3d1a0): the asset-name pin extended to
  grep get.sh as a third guarded location.

[Possible Ripple Effects]
- `get.sh` and `selfupdate` now share one hardening pattern
  (temp+verify+atomic-move); a future change to that pattern in one
  should be checked against the other rather than assumed independent.
- The asset-name format is now guarded in three places by one test
  family (Makefile, Go client, get.sh); a fourth future consumer of the
  format would need the same treatment to stay covered.
- `docs/RELEASE-CHECKLIST.md` is now the single source Carlos needs for
  the post-quiesce manual pass; it should be kept in sync if the
  gh-facing surface grows before that pass happens.

[Testing Notes]
- go test ./... green repo-wide.
- Reviewer live-planted a working binary and drove a failing get.sh run
  against it: checksum-identical survival, trap-cleaned temp confirmed
  gone.
- Reviewer's own mutation of get.sh's asset-name format confirmed caught
  by the extended pin.
- S10 checklist completeness checked against its own pre-registration;
  S8 clean across the full effort range; docs-match-behavior checked in
  both directions.

## Distribution effort (cbus-7sg) — effort ledger

Closing this effort's record. All five milestones are reviewer-approved:
M1 (ddf7527, release Makefile + repoSlug), M2 (03a5514, embed package +
install verbs), M3 (10c6768, selfupdate, with c6's fold-in 08c577b), M4
(1f813c5, opt-in update hint), M5 (1a93755, bootstrap installer +
checklist + docs, with C7's fold-in 28ffbff and c8's fold-in cd3d1a0).
13 local commits span the effort, ddf7527 through cd3d1a0. Nothing has
been pushed — no remote exists for this repo yet; that, the tag, and the
first `make release` all wait on Carlos's quiesce window. The full test
suite ran green under `-race` throughout, milestone by milestone.

Honest limit for the record, stated at wrap rather than discovered
later: every path in this effort that talks to a real GitHub release —
the selfupdate round-trip, the bootstrap, the update-check poll, `make
release` itself — is unit-tested at the helper level and driven through
injectable seams where one exists, but the true end-to-end round-trip
against a real release has not run, because no release exists yet to run
it against. `docs/RELEASE-CHECKLIST.md` names every one of those paths
explicitly for a human pass after the quiesce window. This is declared
here, not discovered by someone hitting it later.

This is the documenter's last scheduled write for the distribution
effort.

## [2026-07-18 01:34:13 UTC] [Distribution] Opt-in update-available hint (M4)

[Attempt #1]

Fourth milestone of the distribution effort: 1f813c5, approved clean with
no conditions.

With `CBUS_UPDATE_CHECK=1` set, `cbus` prints one stderr line when a newer
stable release exists, and at most once a day spawns a detached `gh` poll
(via `Setsid`, so it outlives the command that spawned it) to refresh an
on-disk cache the next invocation reads from. With the variable unset, the
feature does nothing at all — no poll, no cache read, no stderr line.
Everything about the check is deliberately best-effort and silent on
failure: a version hint must never break or slow down the command it
rides along with, and the one exception to "silent" is the hint itself,
which is capped at exactly one line so it can never become noise.

Prereleases stay invisible to the hint, using the same clean-X.Y.Z tag
classification the stable selfupdate path already established in M3
(reused, not reimplemented — a second classifier would be a second place
for the definition to drift). Dev builds (unstamped or non-release
version strings) are never nagged. The hint is excluded from `--json`
output, from `selfupdate` itself (which has its own, more detailed
update-related output), and from `hook-exit` (a SessionEnd hook target
where any extra output is unwanted noise in a log nobody is watching
interactively). With no repository configured (M1's empty-slug case),
the hint is a silent no-op rather than an error.

Reviewer evidence worth carrying, not just the verdict: the exclusion
list's completeness was argued structurally rather than by enumeration
and hoping nothing was missed. The hint writes to stderr only, so any
consumer that only reads stdout is safe by construction and needed no
individual check. Every consumer that reads a *merged* stream (stdout and
stderr combined) was instead checked one at a time and explicitly
excluded: `hook-exit` (log noise), `--json` (whose output is scanned
before any terminator, so a stray line could corrupt a parse), `--version`
(excluded as defense-in-depth even though it isn't actually a merged-
stream consumer), and the hidden subcommand the detached poll itself
dispatches to (closing the recursion question below). No-recursion holds
by construction — the detached poll's own process dispatches through a
path that cannot itself trigger another hint — and this was additionally
confirmed live with a 0.09-second probe rather than left as a structural
claim alone. A garbage or corrupted on-disk cache fails silently and
falls through to no-hint, rather than crashing the command it's
supposed to be invisible alongside. Prerelease-invisibility and
dev-builds-never-nagged were both live-proven against real version
strings, not just asserted from the code reading correctly.

Reviewer's own evidence-hygiene note, carried here because the habit is
worth keeping visible in the record: its first verification probe
miscounted `cbus list`'s own "no peers registered" baseline output as if
it were a hint line, inflating an apparent hit count. It caught this by
checking the actual content of what it counted before concluding
anything, rather than trusting a bare line-count match — the standard
this effort's verification held to throughout, worth naming explicitly
rather than only implicitly demonstrating.

[Files Changed]
- cmd/cbus/update_check.go (new) + update_check_test.go (new): the
  throttled poll, on-disk cache, hint formatting, exclusion checks.
- cmd/cbus/detach_unix.go (new): the `Setsid`-based detachment so the poll
  outlives the parent command.
- cmd/cbus/main.go, usage.go: wiring and help text.

[Possible Ripple Effects]
- Any future verb that reads a merged stdout+stderr stream needs to be
  added to the exclusion check explicitly — the structural stdout-only
  safety argument does not cover a verb that changes to read merged
  output later.
- The on-disk cache format and throttle window are now a small piece of
  persistent state on the user's machine; a future change to either needs
  to tolerate a stale cache written by an older binary.

[Testing Notes]
- go test ./... green repo-wide.
- Live 0.09s no-recursion probe.
- Live-proven prerelease-invisibility and dev-never-nagged behavior
  against real version strings, not simulated ones.
- Garbage-cache silent-fail path exercised directly.

The live `gh` poll leg itself (the actual network round-trip against a
real release) is S10 checklist material, deferred to M5's post-release
checklist for the same reason M3's full round-trip was: no release exists
yet to poll against.

M5 (get.sh bootstrap, the checklist file, and docs) is the effort's final
milestone, released and held for its verdict.

## [2026-07-18 01:27:28 UTC] [Distribution] Selfupdate from the latest GitHub release (M3)

[Attempt #1]

Third milestone of the distribution effort: 10c6768, approved with one
class-C (non-blocking) finding, c6, closed in 08c577b. This entry covers
the milestone and c6's fold-in together.

`cbus selfupdate` downloads this platform's release asset via `gh`,
verifies it, swaps the running binary in place, and refreshes the
installed commands and roles by exec'ing the new binary's own install
verbs. `--check` reports what would happen without applying it.

The download is version-gated before anything is swapped: the newly
downloaded binary must report the exact tag that was asked of `gh`, so a
corrupt download or a wrong-platform asset can never replace a working
install. `versionMatchesTag` is a pure function, mutation-tested, and both
a wrong-version fixture and an unrunnable-binary fixture are refused by
it. A release with no asset matching this platform's naming pattern is a
loud error, deliberately not a quiet no-op that could be mistaken for
"already up to date."

The swap itself is a same-filesystem rename by default, with a fallback
for cross-filesystem installs that handles `ETXTBSY` safely (replacing a
binary that is currently executing cannot always use a simple rename). A
failed swap, on either path, leaves the running binary untouched — there
is no window where the install is neither the old nor the new binary.

The asset name selfupdate looks for is pinned equal to the names M1's
Makefile actually produces; a dedicated test (S5) greps the Makefile
directly rather than duplicating the string, so the two cannot drift
apart in either direction — a Makefile rename without a client update
fails the test, and so does the reverse. Tag classification keeps a
prerelease off the stable update path. M1's deferred empty-slug remedy
(print a remedy rather than guess) was exercised live in this milestone,
not left as a unit-test-only claim.

Reviewer evidence worth carrying, not just the verdict: all three of the
coder's own mutation tests were re-run independently, plus a fourth the
reviewer added itself (an S6 anchor-drop mutation). The `ETXTBSY`
cross-filesystem fallback leg cannot be triggered naturally on macOS, so
the reviewer forced it deliberately — worth doing because the NUC's
tmpfs-backed `/tmp` is exactly where this fallback will actually fire in
production, so an untested fallback there would be a live gap, not a
theoretical one — and drove both a success case and a
failure-leaves-the-old-binary-in-place case through it. `gh` was genuinely
reached live, past the slug gate, against a real authenticated read-only
view — not mocked for this check.

[Files Changed]
- cmd/cbus/selfupdate.go (new) + selfupdate_test.go (new): the download,
  version-gate, swap, and post-swap refresh logic.
- cmd/cbus/version.go (new) + version_test.go (new): `versionMatchesTag`
  and its mutation-tested comparison.
- cmd/cbus/main.go, usage.go: verb dispatch and help text.

[Possible Ripple Effects]
- The asset-naming contract with M1's Makefile is now enforced by a test
  in both directions; either side changing independently is caught, not
  silently accepted.
- The install-refresh step depends on M2's `install-commands`/
  `install-roles` verbs existing in the newly downloaded binary — a
  future change to those verbs' contract is now also selfupdate's
  contract.

[Testing Notes]
- go test ./... green repo-wide.
- Reviewer re-ran all three coder mutations plus its own S6 mutation.
- ETXTBSY fallback leg forced and driven through both success and
  failure-preserves-original cases (unreachable naturally on macOS, real
  on the NUC's tmpfs `/tmp`).
- `gh` reached live past the slug gate against a real authenticated
  read-only view.

Honest limit for the eventual wrap, stated now rather than discovered
later: the full `gh` round-trip and `get.sh` (M5) are declared, not
faked, checklist material deferred to a post-release manual pass — by
necessity, since no release exists yet to round-trip against.
`runSelfupdate`'s actual binary-swap path is deliberately not
unit-driven, because doing so would replace the running test binary
itself; its component pieces (version-gate, asset naming, the rename/
fallback logic) are each tested in isolation instead.

c6, folded in via 08c577b: the latest-tag lookup (the release-view path,
distinct from the download path) captured only `gh`'s stdout, so a failed
lookup surfaced as a bare "exit status 1" with no explanation. The
download path already passed `ExitError.Stderr` through correctly; the
view path had not. Both now surface it, and the fix was live-verified
against a genuinely nonexistent repository, returning "release not found"
(or "could not resolve to a Repository") instead of a bare exit code. The
mechanism worth naming: this was the error a private-repo user's most
likely mistake (repo not yet public, or `gh` not authenticated against
it) would actually produce, and until this fix it was exactly the one
with no reason attached.

[Files Changed, c6]
- cmd/cbus/selfupdate.go (08c577b): stderr capture and pass-through on the
  release-view path.

M4 (update-check hint) is released in parallel and held for its own
verdict.

## [2026-07-18 01:20:50 UTC] [Distribution] Embed and install the /bus-* skills and role prompts (M2)

[Attempt #1]

Second milestone of the distribution effort: 03a5514, approved clean with
no conditions.

A root `claudebus` package embeds `commands/*.md` and `roles/*.md`. This
lives at the module root rather than inside `cmd/cbus` because of a hard
`go:embed` constraint (D23): an embed directive cannot cross a `..` in its
path, so a package under `cmd/cbus` cannot reach files at the repo root —
only a package rooted where those files actually live can. Two new verbs
consume the embed: `install-commands` writes the slash-command prompts to
`~/.claude/commands`, and `install-roles` writes the four role prompts to
`$CBUS_DIR/roles`.

Each install is sha-guarded, file by file: an unchanged destination file is
left alone; a file that has been locally edited since it was last installed
is skipped unless `--force`; a file that doesn't exist yet is written
fresh. Every one of those outcomes is reported per file, so a skip is never
silent (D27's best-effort-but-loud principle, landed here in code rather
than just stated as a ruling). One skipped or failed file does not abort
the rest of the batch, but the process exits non-zero whenever anything was
skipped, so a script driving this can still detect the degraded case.

An embed-count guard fails the build if a command or role file is ever
added or removed on disk without updating the embed's expected count, and
a separate check compares the embedded bytes as actually served against
the live repo source — so a stale embed (built before an edit landed)
cannot silently pass either.

Reviewer evidence worth carrying, not just the verdict: the stale-embed
canary was proven honestly rather than staged. The reviewer's test binary
was compiled *before* editing `roles/coder.md`, so the prebuilt snapshot's
embedded bytes were already stale by the time the edit landed — and the
canary caught that real staleness, not a contrived one written to fail on
demand. The shared doctrine block (repeated 4x across the role files "by
ruling", per this repo's own standing note) came back byte-identical
across all four embedded copies as actually served, 2753 bytes each — this
extends the 4x-by-ruling invariant provably into the compiled binary, not
just the source tree where it was previously only eyeballed. A
dest-replaced-by-directory failure probe (installing over a path where a
directory now sits instead of a file) printed its failure per-file while
the rest of the batch continued past it, which is C4/D27 landed in code
rather than left as an intention.

[Files Changed]
- assets.go (new), assets_test.go (new): the root embed package, the
  embed-count guard, the served-vs-source staleness check.
- cmd/cbus/install_assets.go (new), install_assets_test.go (new): the
  `install-commands`/`install-roles` verbs, sha-guarding, per-file outcome
  reporting, `--force`.
- cmd/cbus/main.go, usage.go: verb dispatch and help text.

[Possible Ripple Effects]
- Any future addition or removal of a file under `commands/` or `roles/`
  must update the embed-count guard's expectation, or the build fails —
  this is now a standing constraint on those two directories.
- `install-commands`/`install-roles` are the mechanism M5's `get.sh`
  bootstrap will call into for a first-time install; their exit-code and
  reporting contract is now load-bearing for that milestone.

[Testing Notes]
- go test ./... green repo-wide.
- Reviewer's stale-embed canary used a genuinely pre-edit compiled binary,
  not a synthetic staleness injection.
- Byte-identity of the doctrine block verified across all four served
  copies (2753 bytes each).
- Dest-replaced-by-directory failure probe exercised live, per-file
  reporting confirmed while the batch continued.

M3 (selfupdate) is released in parallel and held for its own verdict.

## [2026-07-18 01:15:55 UTC] [Distribution] Release Makefile and repo-slug plumbing (M1)

[Attempt #1]

First milestone of the distribution effort (gh releases, selfupdate, and
embedded installs — the bdx pattern): ddf7527, approved clean with no
conditions.

Adds a cross-compile Makefile that builds the unix matrix only — darwin and
linux, amd64 and arm64 (D25; Windows is out of scope for this effort). It
stamps the version and the repo slug into the binary via ldflags, builds a
`dist/` directory with exact `cbus-<os>-<arch>` asset names (the naming the
later selfupdate milestone will need to match exactly to find its own
asset), and carries a tag-and-slug-gated release target that publishes
through `gh`. That target is written but never run as part of this
milestone — the remote and the first real release both stay gated behind
Carlos's post-quiesce window, same as every outward action in this repo.

`repoSlug` is baked into the binary at release time and stays empty in
committed source (D26): no personal repo slug lands in a file that gets
committed, so the repo's visibility (public/private) can change later
without requiring another history scrub like the one already pending.
`$CBUS_REPO` overrides the baked value at runtime for anyone building their
own binary from source, and an empty slug (neither baked nor overridden)
prints a remedy telling the user how to set it, rather than guessing or
silently degrading.

No behavior change to any existing verb, and the legacy install scripts
(`install.sh`, `install-cbus-go.sh`) are untouched — this milestone is
additive build tooling only.

Reviewer evidence worth recording, not just the verdict: the ldflags bake
was proven from the reviewer's own matrix build, grepping the built
binaries' strings in both directions (present when the slug was baked,
absent when it wasn't — not just "the flag was passed"). The `-X` ldflags
target's symbol existence was checked explicitly, closing a silent-no-op
failure class where a renamed or moved variable would silently stop
accepting the flag with no build error. The legacy installers were
confirmed byte-identical before and after. The release target's
tag-and-slug-gated refusal was live-confirmed with `gh` never actually
reached — the gate fires before the network call, not after a failed one.

[Files Changed]
- Makefile (new): the cross-compile matrix, asset naming, ldflags stamping,
  the release target.
- cmd/cbus/repo.go (new) + repo_test.go (new): repoSlug resolution
  (env > baked > empty-with-remedy), a 4-case test.
- .gitignore: excludes the new `dist/` build output.

[Possible Ripple Effects]
- The exact `cbus-<os>-<arch>` asset naming is now a contract the
  selfupdate milestone (M3) must match byte-for-byte to find its own
  update asset; renaming the pattern here would be a breaking change to
  that milestone once it lands.
- repoSlug's env>baked>empty precedence is now the standing resolution
  order for any future code that needs to know the repo's remote location.

[Testing Notes]
- go test ./... green repo-wide.
- Reviewer's own cross-compiled matrix build, strings-grepped both
  directions for the baked slug.
- Live-confirmed: the release target refuses before reaching `gh` when the
  tag/slug gate isn't satisfied.

M2 (root embed package + install-commands/install-roles) is released in
parallel and held for its own verdict.

## [2026-07-17 19:22:17 UTC] [Client/Formations] The starter template library (M8) — formations v1 complete

[Attempt #1]

Eighth and final milestone of formations v1, approved with one class-C
finding (c5). This entry covers the M8 commit range (1ef6435 through
8d38d6c, plus ace7dd0) and c5's fold-in (356cef5) together, and closes
with the effort's ledger — this is the record's last write for
formations v1.

1ef6435 makes apply, show, and bootstrap resolve a formation by name
runtime-first, then against repo-committed templates
(`formations/*.json`). A runtime save of the same name shadows a
committed starter — the user's live state wins, the opposite precedence
from role files, whose canonical home is the repo (D20). The resolved
source is printed, so a shadowed template is stated behavior, not a
surprise discovered later. The name==filename identity rule (M1's C1)
now guards repo templates too, and a torn runtime file stops the resolve
rather than silently falling through to a template. `rm` stays
runtime-only: a name that exists only in the repo gets a loud refusal
pointing at git, so the tool can never delete a version-controlled file
(D22).

b816153 adds `--channel`, so a committed starter can be applied to
whatever channel an effort actually uses without editing the file. The
override is set once, in memory, before anything reads the channel, so
every downstream read follows it: the applier-presence check, the roster
and reconcile, the kickoff join lines and reply-to address, and the alias
reservations. A test asserts structurally that nothing leaks onto the
template's own channel, and that apply writes no envelope file — not a
behavioral spot-check but a guarantee about where the seams are.

ee6fdcd lets save read a committed template as its refresh base when no
runtime file of the same name exists yet, so an apply-then-save cycle
inherits the template's rolefile references instead of blanking them to
TODO. save still writes the runtime store only, never the repo file — a
test reads the committed file before and after a save and asserts it is
byte-for-byte unchanged (H1). Basing on a template while targeting a
different channel (the template's default channel equals its name) hits
the existing repoint refusal from M1/M3; that interplay is pinned by a
test rather than left to memory.

5ffed2f: a dry-run needs no joined applier and no existing channel.
Applying a template to a channel nobody has joined yet is the first thing
a new user does, and previewing it should not require joining first. A
dry-run skips the applier-presence check (it sends no kickoffs, so it
needs no reply-to), and the plan treats an absent channel as an empty
roster rather than an error, so every peer plans as missing. A real apply
still requires a joined applier, and save still refuses to snapshot a
channel that does not exist. Fragility rider carried for the record: this
relaxed path is unreachable from a real apply by construction today —
worth re-checking if that construction ever changes.

ace7dd0 lets a peer defer its model to its role file at apply time:
previously apply read only a peer's explicit model, so a template
carrying no models launched everyone on the CLI default. It now falls
back to the role file's `MODEL:` line when the peer sets none — the same
defaulting `spawn --role` already does — so a template can leave models
unset and inherit coder=opus, reviewer=fable, documenter=sonnet from
roles/*.md (D21). The resolved model reaches both the launch and the
reservation stamp, so a save after apply records the real model, never a
blank.

8d38d6c ships the payoff: `formations/dev-trio.json`, a committed
four-role starter (orchestrator, coder, reviewer, documenter), all
`mode=template`, no session ids, no drift anchor, no models — models
defer to each role file's `MODEL:` line per ace7dd0. rolefile references
are deliberately left unpinned, since a pin on a committed file rots at
the coming history scrub (D15's logic, applied here to committed
templates). Prompts are never inlined: the template references
roles/*.md by path, and a canary test fails the build if any committed
template contains doctrine-block text or a personal path — the
public-repo-face rule and the reviewability rule, both pinned and
verified to actually bite.

c5, folded in via 356cef5: the `/bus-formation` skill had never learned
M8. It now documents the `--channel` per-run override, the committed
dev-trio starter applyable from any checkout, and that a runtime save
shadows a committed starter of the same name (with apply printing which
source it used). All four skill items were verified; c3's earlier skill
entry (bootstrap in the argument hint) was confirmed intact; the doc's
prose style choice was accepted as consistent with the rest of the file.

[Files Changed]
- internal/client/formation.go, formation_resolve_test.go: runtime-first
  resolution, name==filename guard extended to repo templates, torn-file
  handling (1ef6435).
- cmd/cbus/formation.go (1ef6435, b816153, ee6fdcd): rm's git-pointing
  refusal, `--channel` flag wiring, template-seeds-save wiring.
- internal/client/formation_apply.go, formation_apply_test.go (b816153,
  5ffed2f, ace7dd0): `--channel` override plumbing and its leak test,
  dry-run's relaxed preconditions, role-file model fallback.
- internal/client/formation_plan.go (5ffed2f): absent-channel-as-empty-
  roster planning.
- internal/client/formation_save.go (ee6fdcd): template-as-refresh-base,
  runtime-only write, byte-identity test.
- formations/dev-trio.json (new, 8d38d6c): the committed starter.
- internal/client/formation_resolve_test.go (8d38d6c): the doctrine/
  personal-path canary.
- commands/bus-formation.md (356cef5, c5): `--channel` and starter-
  template documentation.

[Possible Ripple Effects]
- Any future committed template must pass the canary (no doctrine text,
  no personal paths) or the build fails — this is now a standing
  constraint on `formations/*.json`, not just on this one file.
- Runtime-shadows-repo precedence means a hand-saved formation with the
  same name as a shipped starter silently takes over; the printed source
  line is the only signal, so tooling or docs that don't surface it could
  reintroduce the earlier "which one ran" confusion class.

[Testing Notes]
- go test ./... green repo-wide, `-race` clean, consistent with every
  prior milestone in this effort.
- Structural (not just behavioral) tests for the `--channel` isolation
  and the save byte-identity guarantee.
- Canary test verified to actually fail the build when a committed
  template violates it (not just present, but proven to bite).

Record-only: n16 — a `--channel`-instantiated effort cannot inherit the
template's rolefile refs via save, because repo-base seeding keys on a
name+channel match and the override breaks that match; a `--based-on`
flag could close this if it stings in practice, but it doesn't block
anything today. n17 — `formation list` stays runtime-only, so a fresh
user sees "no formations saved" while `apply dev-trio` already works
against the committed starter; a discoverability seam, consistent with
the runtime/repo split this milestone establishes, recorded here rather
than fixed.

## Formations v1 — effort ledger

Closing this effort's record. M1 through M8 are all reviewer-approved.
38 local commits span the effort, f4553ee through 356cef5. Nothing has
been pushed — this repo has no remote until Carlos's quiesce window. The
full test suite ran green under `-race` throughout, milestone by
milestone, not just at the end. The build ships to installed `cbus` on
the next Carlos-gated install, the same gate every prior client change in
this repo has waited on.

This is the documenter's last scheduled write for formations v1.

## [2026-07-17 18:48:40 UTC] [Client/Formations] The meta.json birth-record (M7) — honest-limit #2 closes

[Attempt #1]

Seventh milestone of formations v1, approved with one class-C finding
(C4). This entry covers cd71d43, 33a639a, and 9368480 together with C4's
fold-in fix 311f86f, per the hold-for-verdict rule. It closes honest-limit
#2 from the M6 wrap block: "origin is unknowable at save time... nothing
mechanically enforces it until a meta.json birth-record ships." That
birth-record ships here.

cd71d43 does the reservation-side stamping. A session cannot know whether
it was spawned fresh, forked, or joined on its own, but the launcher can,
at reservation time, before the child even boots. `ReserveAlias` now
stamps origin+model into the placeholder: `spawn` stamps `fresh`, `branch`
stamps `fork` (the one place a forked transcript is actually made), and
the child's own join carries those values into its real-sid meta. D18: a
reservation-less join resolves origin by a three-way rule — a reservation
placeholder's record is inherited; this session's own surviving meta is
preserved (a resume-rejoin keeps its birth-record rather than flipping to
`joined`); any other name is joined with an unknown model. Another
session's record is never carried across, and a blank is never inferred
into a value — the never-infer rule holds even for a torn reservation,
which stays blank rather than guessed. The birth-record is deliberately
read before the channel prune runs: a resumed peer's meta carries a dead
listener, which prune would otherwise reap an instant before the record
could be read, silently losing it on a restore. `origin` and `model` are
`omitempty`, so rewriting a bash-era or pre-record meta stays
byte-identical — pinned against 9 real metas, the same discipline
`lastActivity` already follows.

9368480 does the apply-side stamping: a templated peer is fresh and a
forked peer is fork-born, so apply now reserves each with that origin and
the peer's model, meaning a save after apply records what actually got
launched. Recording a fork as fork-born is what makes a later restore
refuse to fork it again and template it instead — the same rule `branch`
already follows at the fork-creation site. Both modes mint a new session
id, so there is no preserved birth-record to overwrite. Resume is the
deliberate exception and stays reservation-free: it reuses the existing
id, and a reservation placeholder would win over that id when the peer
rejoins, blanking out its preserved origin. A test pins that resume
reserves nothing, so adding one later fails the build.

33a639a closes the loop on the save side: `ReadPeerMeta` now surfaces
origin+model, and save fills them onto a peer by the same fill-once rule
it uses for every field it touches — only when the envelope field is
blank and the meta value is present, never over a hand-set value, never
from a blank meta. A meta value the envelope would reject (an origin
outside fresh|fork|joined, a flag-shaped model, the shape a
hand-corrupted meta produces) is not propagated; it is skipped and
surfaced in the report, so garbage cannot ride a birth-record into the
file silently. Together with the reservation stamping, a spawn-born peer
now saves with `origin=fresh` and its model already filled, so apply can
resume it without a hand-edit — closing the manual step the smoke's step
8 needed.

C4, folded in via 311f86f: two things in save's output were wrong. The
skip of a hand-corrupted birth-record lived only in the report struct,
silent at the terminal — a run that skipped a corrupt value read as a
clean save. It now prints, naming the peer and the offending value. And
save's guidance line still claimed the store records nothing beyond
alias/sessionId/cwd/machine at the exact moment save started reading
origin/model from it too; it now tells the truth about what is captured
versus what stays hand-maintained. The reviewer re-proved the fix with the
exact command that found the original defect, and mutation-pinned the old
wording absent so it can't silently return.

Net effect, stated plainly: origin and model are now mechanical on every
launcher-born peer (spawn, branch, apply's template and fork modes);
resume needs no hand-edit to be recorded correctly; and R1's teeth (never
fork across roles) are restored on tool-saved formations rather than
resting on a human remembering to fill in a field by hand.

[Files Changed]
- internal/client/store.go (cd71d43): `ReserveAlias` origin+model
  stamping, the D18 three-way join resolution, prune-order fix.
- internal/client/harness.go, spawn.go, spawn_test.go (cd71d43): callers
  updated for the new stamping.
- internal/client/birth_test.go (new, cd71d43): the reservation-stamping
  and never-infer test suite.
- internal/client/formation_apply.go, formation_apply_test.go (9368480):
  apply-side origin/model stamping, resume-reserves-nothing pin.
- internal/client/formation_save.go, formation_save_test.go, liveness.go
  (33a639a): save-side fill-once capture of origin/model, reject-and-report
  for invalid meta values.
- cmd/cbus/formation.go, formation_test.go (311f86f, C4): visible skip
  reporting, corrected guidance line.

[Possible Ripple Effects]
- Every future launcher path (spawn, branch, apply) must route through
  `ReserveAlias`'s stamping rather than writing meta directly, or a new
  peer type will silently regress to unknown origin.
- The prune-before-read ordering fix is a general reservation-store
  correctness fix, not formations-specific; any other consumer of a
  resumed peer's meta benefits from it too.

[Testing Notes]
- go test ./... green repo-wide.
- Byte-identity pinned against 9 real (non-synthetic) metas, including
  bash-era and pre-record shapes.
- Reviewer re-proved C4 with the exact command that originally found it;
  the old (now-false) guidance wording is mutation-pinned absent.

M8 (starter templates) is released in parallel and will be the effort's
final entry.

## [2026-07-17 18:11:26 UTC] [Client/Formations] v1 live smoke: identity verification

[Attempt #1]

The first and only live smoke of formations v1: the frozen 10-step
lifecycle, run for real rather than dry-run, to answer the one question no
unit test can — does a resumed peer actually come back as itself, or does it
come back as a fork wearing the original's name (the B31 ghost-orchestrator
class this whole design exists to prevent).

Setup: 3 windows, one child spawned as sonnet, formation name `fmsmoke`. The
coder reported PASS with teardown proofs matched. The reviewer did not take
that report on faith: it verified independently, and it verified FIRST,
directly against the resumed peer's own transcript (sid-B) rather than
against the coder's summary of it.

Identity proof, the load-bearing claim: the resumed session, given no prompt
to do so, quoted its own first-turn nonce back. That is only possible with
real process continuity, not a fresh actor that was told what the nonce was.
Structural proof, independent of either party's narrative: both the peer's
original kickoff and its resume kickoff land in one session transcript file
— a fork would have written a second, different sid's file, so a single file
containing both is direct evidence of resume, not reconstruction. The
reviewer grepped the full transcript for `--fork-session` and found zero
occurrences. The reviewer's own independent nonce count matched the coder's
reported count.

Scope, stated precisely rather than inflated: exit codes and teardown
cleanliness rest on the coder's summary alone — nothing in the identity
verdict depends on them, and the record should not imply otherwise. Evidence
epistemics for anyone re-reading this later: the session transcript is
primary evidence; a reconstructed kickoff (typed out from memory or notes
rather than read verbatim off disk) is corroboration only, never proof on
its own.

The smoke's real yield was a defect, not a formality: D17, apply's `--brief`
flag was never wired — the plumbing existed but the CLI rejected the flag,
so kickoff briefs went silently empty. This is a design-section-5.3 fidelity
gap inside the already fully-reviewed M5, caught only because someone ran
the real thing instead of trusting the review cycle that had already passed
it. D17's fix rides its own commit and will fold into M5's record, same
pattern as the class-C findings, once the reviewer confirms it.

[Testing Notes]
- Live, not simulated: 3 real windows, one real child process, the real
  formations channel.
- Reviewer verification order: transcript first, coder's summary second —
  deliberately, to avoid anchoring on the report being verified.

No files changed by this entry; it records a live verification run, not a
code change. D17's fix commit is tracked separately and folds into M5 when
its hash arrives.

## [2026-07-16 19:24:22 UTC] [Client/Formations] Bootstrap and the /bus-formation skill (M6) — v1's verb set is complete

[Attempt #1]

Sixth and final milestone of formations v1: 8317624, approved with one
class-C (non-blocking) finding. This closes the verb set design section 9
committed to — save, apply, bootstrap, list, show, rm — with nothing added
beyond it.

`bootstrap` prints one peer's first turn for a human to paste: the path for
a peer apply will not launch (one recorded on another machine), or simply
for reading what a peer would be told before opening a fleet. It composes
through the same prompt builder apply uses — parity by construction, not by
convention: `BootstrapPeer` returns `KickoffPrompt` directly, with a content
test pinning the two paths to the same output. Two renderers that could
drift apart, briefing a peer differently depending on who started it, is
exactly the kind of divergence nobody notices until it matters.

Because it consults no live state, bootstrap does not skip a peer on another
machine the way apply does — that peer is exactly who bootstrap exists for.
It still refuses what the file alone proves wrong: no world is needed to
know a fork-born peer must not be resumed, and handing someone that prompt
anyway reproduces the ghost-orchestrator failure with extra steps. The file-
only refusals are exactly R1, R2, and D12, mode-gated the same way apply
gates them. A peer with no recorded role is asked to describe itself in its
first reply — the store cannot capture a role and save cannot invent one, so
the peer is the only one who can say what it is. The skill (`commands/
bus-formation.md`) surfaces exactly the verbs that exist and steers to
`--dry-run` before a real launch.

Live-verified: the rendered prompt for a real peer carries the D15 advisory
rolefile-pin line and the no-escalation line exactly once. Layers-compose
finding, record-worthy: the reviewer fed bootstrap a malformed fixture
envelope, and it was refused by M1's load-time validation before bootstrap's
own output logic ever ran — the milestones compose the way they were meant
to, rather than each needing its own copy of the same guard. The shared
self-describe line, added for this milestone, also closed a role-less-peer
gap the reviewer had noticed independently one milestone earlier than
expected.

Class-C c3 folds in without a separate re-review: the skill's frontmatter
trigger hint omits `bootstrap` from the verbs it lists, a one-line fix.
Closed in 52d7dc9: bootstrap added to the argument hint, with parity
verified across the hint, body, and usage text for all six verbs.

[Files Changed]
- internal/client/formation_kickoff.go, formation_kickoff_test.go:
  BootstrapPeer, the shared-composer parity test.
- cmd/cbus/formation.go, cmd/cbus/formation_test.go, cmd/cbus/usage.go:
  bootstrap dispatch and help text.
- commands/bus-formation.md (new): the `/bus-formation` skill.

[Possible Ripple Effects]
- None to the wire or relay; local CLI and skill surface only.
- bootstrap and apply now share one kickoff composer — a future change to
  kickoff wording or gating affects both call sites at once, which is the
  point.

[Testing Notes]
- go test ./... green repo-wide.
- Live-verified rendered prompt content (D15 pin line, single no-escalation
  line) and the malformed-fixture refusal path.

Record-only: n13 — a 3-line mode-mapping block is duplicated between apply
and bootstrap; extract on next touch, not urgent now.

v1 is fully reviewed as of this milestone: M1 through M6 all closed.

Honest limits, stated by the coder at wrap rather than discovered later —
carried here verbatim in substance because a limit named at wrap is worth
more than one found after the fact:
- Fork mode is permanently untested live, by choice: proving it live would
  mean manufacturing the exact ghost-orchestrator scenario the design
  forbids. It is exercised only through a fake forker in tests.
- Origin is unknowable at save time. R1's refusal (never fork across roles)
  depends entirely on a human having recorded the origin correctly; nothing
  mechanically enforces it until a meta.json birth-record ships, which is
  Carlos-gated and not built.
- The alive-check proves "not alive on the bus", and nothing stronger — it
  is not a claim about the process, only about what cbus can observe.
- Rolefile pins stay advisory, not enforced, because the pending history
  scrub will invalidate every recorded SHA (D15) — pin-honoring today would
  orphan every formation saved before the scrub.

## [2026-07-16 19:21:15 UTC] [Client/Formations] Apply: launch the missing peers and prove they answered (M5)

[Attempt #1]

Fifth milestone of formations v1: apply, approved with one class-C
(non-blocking) finding. This entry covers b57511d and 1cbdec3 together, plus
a refactor and a note connecting back to M4's record, per the
hold-for-verdict rule.

1cbdec3 adds the apply engine and its CLI. Launch is sequential and
anchor-first, so a formation whose members expect an orchestrator to already
be listening gets one before anyone else starts. The whole plan is decided
before anything launches (M4's contribution) — a refusal must not arrive
halfway through a fleet. Convergence is a round-trip and nothing else: each
kickoff carries a nonce and asks for it back, and apply reads its own inbox
for the answers with a bounded file read against a deadline — never an exec
of the tail follower, which would block forever. Reading is non-destructive,
so the applier's own Monitor still sees every frame. A peer that never
answers is reported failed, because launched is not converged and a roster
marker would otherwise be trusted when it has lied in both directions before
(the B31 and presence-smoke history this design keeps citing). The three
modes launch three different ways: resume continues a session as itself and
must not carry `--fork-session`; kickoffs tell a peer what it IS — a fork is
warned it is not the original and must not act on its parent's unfinished
work, and a peer degraded to template is told it lost the history it was
meant to have. Nothing launches when nothing is launchable: an empty fleet
reporting success is how a restore that did nothing gets read as one that
worked, so that case is a failure, not a quiet no-op. `--dry-run`, `--only`,
and `--wait` ship here; kickoffs follow design section 5.3 with D15's
advisory rolefile pins.

b57511d, a D11 self-find folded into the same milestone: the applier is
never itself a peer to launch. An orchestrator that saved a formation is
normally in it and is the one running apply — it cannot be missing, it is
here. Without this fix, an applier whose Monitor had died planned a relaunch
of its own alias and then failed on its own reservation, so the common case
(the orchestrator applying its own formation) could not work. The fix lives
in the plan rather than in apply itself, so `--dry-run` rehearses the same
play that actually runs — this peer's liveness cannot lie, because it is the
one executing the check. A second D11 in the same range: a duplicate
no-escalation line in template kickoffs, caught not by a unit test but by
reading a rendered 7.6KB kickoff end to end; now emitted exactly once per
mode.

Reviewer live dry-ran apply against the real formations channel (this
formation, its own peers). Honest mechanism note carried for the record: a
whole-store diff between before/after snapshots is racy on a live bus — the
reviewer's first residue check flagged ordinary inbox traffic as leftover
residue, a false positive, and was replaced with a narrower, scoped check
instead of a whole-store comparison. Trap for the record, unrelated to the
plan's correctness: the test suite's role fixtures were shadowed by this
repo's own real `roles/*.md` files, because `LoadRole` checks the git
toplevel before the test's intended fixture directory — a test was passing
for the wrong reason. Fixtures were renamed to names that don't collide with
shipping roles. No live smoke (an actual formation launch) has run yet; that
stays orchestrator-gated.

a5b8b72, a small refactor riding the same range: extracts the shared
child-launch prefix (the CCS-instance detection that `branch` and `spawn`
each rebuilt inline, and that apply now needs a third time with the profile
overridden per peer) into one place. Behavior is unchanged for both existing
callers — an empty profile still resolves to the caller's own, which is what
they passed implicitly before.

Connects back to M4's record: 68f81ed (the c1 precedence pin, already
written into M4's entry) sits inside this same commit range; the reviewer
independently re-ran its own mutation test on that pin to reconfirm it while
reviewing M5, rather than take the earlier confirmation on faith.

[Files Changed]
- internal/client/formation_apply.go (new) + formation_apply_test.go (new):
  the apply engine, convergence, and the failed/converged/degraded reporting.
- internal/client/formation_kickoff.go (new) + formation_kickoff_test.go
  (new): per-mode kickoff composition (resume/fork/template), D15 advisory
  pins, the no-escalation line.
- cmd/cbus/formation.go, cmd/cbus/formation_test.go, cmd/cbus/usage.go:
  apply dispatch, `--dry-run`/`--only`/`--wait`.
- internal/client/formation_plan.go (b57511d): the applier-exclusion rule.
- internal/client/harness.go, internal/client/spawn.go (a5b8b72): shared
  child-launch prefix extraction.

[Possible Ripple Effects]
- Convergence now depends on a bounded inbox read with a deadline; a very
  slow or wedged peer reads as failed rather than hanging apply.
- The shared launch-prefix helper is now a single point of change for
  branch, spawn, and apply's per-peer profile override alike.
- Test fixtures for role resolution must keep avoiding real `roles/*.md`
  names going forward, or the shadowing trap recurs silently.

[Testing Notes]
- go test ./... green repo-wide.
- Reviewer live dry-run against the real formations channel (this
  formation's own peers), scoped-diff residue check after the whole-store
  diff proved racy on a live bus.
- Fixture-shadowing trap caught and fixed before it could hide a real
  resolution-order bug.

Class-C c2 folds in without a separate re-review: payload references must
render in the envelope's own order, not sorted — reading order is the point
of M1's hand-edit preservation guarantee, so sorting it away here would
undercut that. Closed in 8806efc: `payloadRefs` now walks the raw JSON in
envelope order instead of sorting it. Root cause was representational —
`map[string]RawMessage` cannot represent key order at all, so a sort had been
added for determinism that the author's own order already provided. The
class was chased rather than patched locally: `show` already preserved order
correctly (it renders via `json.Indent`, which is order-preserving), and
`drift_anchors` was confirmed to stay sorted deliberately — its keys are
unordered facts, not authored prose, so sorting them is correct and not part
of this class. Torn or non-object payloads pass through exactly as written,
now pinned by a test. Mutation-verified via the reviewer's own
`TestPayloadRefsKeepsAuthoredOrder`.

Record-only: n12 — one convergence poll tick costs O(inbox size); bounded
and fine at formation scale, revisit only if inboxes grow very large.

D17 folded in: the v1 live smoke (recorded separately above) surfaced that
apply's `--brief` flag was never wired, after this milestone had already
passed a full review cycle. `ApplyOptions.Brief` flowed correctly into the
kickoff builder, but no CLI flag ever set it, so apply always sent an empty
effort brief while bootstrap (M6) carried one through the same builder — a
design-section-5.3 fidelity gap. Fixed in two commits: 6c3ea2f wires `--brief`
(the flag, `opts.Brief`, usage text, and a client-level render test), and
2c5d2ea drives the flag through `runFormationApply` end to end to a rendered
kickoff, via a new `applyForker` package-var seam (the `http.DefaultClient`
idiom — a real terminal by default, swappable in tests; no parallel hazard
since the cmd-level tests use `t.Setenv`). The reviewer re-ran the fix's own
mutation: unwiring `opts.Brief` fails 2c5d2ea's test while every in-process
test from the original milestone stays green — which is the whole story of
how the gap survived review.

Reviewer's self-report on the miss, carried here rather than smoothed over:
this was a review miss, not only a live find. The reviewer had read both
halves at M5's original review — the `Brief` plumbing and the
flag-parsing allowlist — but never joined them, because the kickoff-content
test in place at the time injected the brief in-process, bypassing the CLI
path entirely; that is exactly how a dead CLI parameter stays invisible.
6c3ea2f's own first-pass coverage repeated the same shape: parse-without-
render, and render-without-CLI, never both at once. Distilled doctrine,
reviewer's words in substance: a content gate for a user-reachable feature
must enter through the user's door at least once. This is the third honest
self-report of the effort (following the M1 HTML-escaping dismissal and the
M2 C2 citation gap), and the closing commit embodies the doctrine directly,
as a test that would have caught the original gap.

[Files Changed, D17]
- cmd/cbus/formation.go, cmd/cbus/formation_test.go, cmd/cbus/usage.go
  (6c3ea2f): `--brief` flag and wiring.
- internal/client/formation_apply_test.go (6c3ea2f): client-level render
  test.
- cmd/cbus/formation.go, cmd/cbus/formation_test.go (2c5d2ea): the
  `applyForker` seam and the CLI-path end-to-end test.

## [2026-07-16 19:05:44 UTC] [Client/Formations] The apply plan (M4): every decision made before anything launches

[Attempt #1]

Fourth milestone of formations v1: a03128a, approved with one class-C
(non-blocking) finding. The B31 restore's failure was a bad decision (forking
the wrong transcript), not a bad terminal, so this milestone puts every
decision in a pure function: `BuildPlan` takes a formation plus a world
snapshot and always yields the same plan; it launches nothing. That makes
every prohibition a table test instead of a live rehearsal. `GatherPlanWorld`
is the read-only counterpart that assembles the world snapshot.

Refusals, each with its own message so failures stay legible: a fork-born
peer resumed or forked would re-run its parent's intent under another name,
and an unrecorded origin cannot prove it isn't that case (D12 — refuse over
warn, per the B31 F1 lesson); one transcript claimed by two aliases means one
of them is wrong; and resume on a session that is live-armed elsewhere is
refused, never silently degraded to fork (D14 — names the live location and
both remedies: kill or re-point the original, or a deliberate `mode=fork` for
an intentional copy — closing design section 3's refuse-or-degrade either/or
on the refuse side). Machine matching is strict equality (D13): a skip names
both values, zero launchable peers is a failure rather than a quiet
empty-fleet success, and an empty machine field means local. Drift is
reported and never blocks — the snapshot is a cache, the ground is live.

Reviewer live-reproduced the plan read-only against the real formations
channel: CBUS_DIR shasums identical before and after, every refusal class
fired on real data, including D14 refusing on the coder's own live session by
name (launchable=1, refusals=3, zero CBUS_DIR writes). Record-worthy
mechanism the reviewer surfaced: the D14 remedy line sent the coder back to
re-read design section 3 and catch a bug in its own pre-commit build — it had
gated `mode=fork` on liveness too, which would have refused the exact mode the
design prescribes for a live original (a deliberate copy). The shipped guard
is resume-only. Harness finding for whoever re-verifies this later: copying
live peer registrations into a scratch CBUS_DIR to test against silently
breaks liveness detection, because the argv needle is rebuilt from the
scratch path rather than the original — false negatives, not a plan bug.

M4 ships no CLI by design (ruling D9's surface split): `--dry-run` and
`--only` reach users at M5, when apply itself lands.

Class-C c1 folds in without a separate re-review: a precedence test-pin
asserting the refusal-priority ordering (R2) on both claimants of a
conflict, including one with `origin=fork`. Closed and reorder-verified in
68f81ed.

[Files Changed]
- internal/client/formation_plan.go (new): BuildPlan, GatherPlanWorld, and the
  refusal table (D12/D13/D14).
- internal/client/formation_plan_test.go (new): the refusal-class table
  tests plus the live differential harness.
- internal/client/formation_plan_test.go (68f81ed, c1): pins the
  shared-sid refusal ahead of the per-peer ones, asserted on both claimants.

[Possible Ripple Effects]
- M5's apply executes exactly this plan; any change to a refusal's condition
  or message here is a direct behavior change for apply, not just planning.
- The scratch-CBUS_DIR liveness gotcha applies to any future test or review
  harness that copies real registrations rather than joining fresh ones.

[Testing Notes]
- go test ./... green repo-wide.
- Live differential: real bus, read-only, byte-identical CBUS_DIR shasums;
  every refusal class (fork-born resume/fork, dual-claimed transcript,
  live-armed-elsewhere resume, machine mismatch, zero-launchable) reproduced
  on real data.

Record-only: anchor-first launch ordering is out of scope here and gates at
M5 (n10); liveSids' limit is honestly scoped in the code as written, no
follow-up needed (n11).

## [2026-07-16 19:00:29 UTC] [Client/Formations] Formations list/show/rm (M2), with the fixture-portability fix folded in

[Attempt #1]

Second milestone of formations v1: the read-side verbs against the M1 envelope.
This entry covers 31f9501 and its confirmed fix b24cd70 together, per the
hold-for-verdict rule.

31f9501 adds `cbus formation list/show/rm`. `show` flags the two states that
make a formation unusable as written: a recorded sid whose transcript is gone
(STALE), and a peer with no brief to send (TODO). A peer tagged with another
machine reads `unchecked` rather than `STALE` — this host cannot see that
host's transcripts, and calling it stale would dress a guess as a finding.
Transcripts are located by globbing the sid rather than rebuilding a project
directory from a cwd: the munging rule would have to be duplicated and kept in
sync, and a peer whose cwd moved since save would read as stale while
`--resume` still worked. The sid and profile come from a hand-edited file, so
both are screened before they reach a glob or a path. `rm` refuses path
traversal. An unreadable envelope is listed with its error rather than skipped
— the file is still on disk either way. Ruling D7 accepted
`internal/client/transcript.go` as a 7th file in the formation surface: its
live/stale/unchecked predicate is shared with M4's resume-liveness gating. The
`mbp` vs `carlos-mbp` machine-value display convention is deferred to an M4
ruling.

Reviewer's verdict was CONDITIONAL APPROVE on one binding finding, C2,
test-portability only (no product-code defect): the fixtures hard-coded one
developer's hostname and expected STALE for it, and used a literal `"nuc"` as
the foreign machine — so the suite failed on any host but that one laptop, and
inverted on a host actually named nuc. b24cd70 fixes it: fixture machine
values now derive from `ShortHostname()`, with the foreign value built by
extending it so it structurally cannot collide with the host under test. The
reviewer's own C2 citation missed a twin instance of the same bug in
`internal/client/formation_read_test.go`; the fix's inclusion of that file was
accepted as a valid scope extension, not scope creep — the reviewer recorded
that its own citation missed those rows.

[Files Changed]
- cmd/cbus/formation.go, cmd/cbus/formation_test.go, cmd/cbus/main.go,
  cmd/cbus/usage.go: list/show/rm dispatch, flags, help text.
- internal/client/formation.go: read-side helpers backing show/list.
- internal/client/transcript.go (new) + transcript_test.go: the
  live/stale/unchecked transcript predicate (D7).
- internal/client/formation_read_test.go (new).
- b24cd70: cmd/cbus/formation_test.go and
  internal/client/formation_read_test.go — fixture hostnames derived from
  ShortHostname(), foreign value built by extension so it cannot collide.

[Possible Ripple Effects]
- transcript.go's predicate is now a shared dependency for M4's resume-mode
  liveness gating; changing its semantics later affects both surfaces.
- The machine-value display convention (short vs full hostname) is still open,
  deferred to M4 — list/show output may need a follow-up pass once that
  ruling lands.

[Testing Notes]
- go test ./... green repo-wide after both commits.
- Portability: the fixed suite passes independent of the running host's
  hostname (previously passed on exactly one machine and inverted on a host
  literally named nuc).

Record-only: RoleTODO substring over-match is warning-only, not a hard
failure; an invalid profile value skips the sibling root and reads STALE,
which the reviewer noted is consistent with the predicate's overall framing.

## [2026-07-16 19:00:29 UTC] [Client/Formations] Formations save (M3), with an M1 emission fix folded in

[Attempt #1]

Third milestone of formations v1: `cbus formation save`, approved outright,
plus a self-found fix to M1's own emission code landing in the same commit
range.

61c25f9 adds save. The store records exactly four substrate facts per peer —
alias, sessionId, cwd, host — and save owns only those; mode/onStale/
target/addresses get declared defaults (template/template/tab/[]), and model,
rolefile/role, origin, and profile are left blank for a human to fill in. A
refresh updates the four captured facts and never overwrites anything else it
finds, which is what makes re-saving at a milestone boundary safe. A peer
that's in the file but no longer on the channel is kept, not dropped: a paused
effort is the main thing a formation exists to hold, and it is paused
precisely when its peers are gone. An unloadable envelope refuses rather than
guessing at a rewrite, consistent with M1's refuse-over-guess posture.

9b3d487, folded into this milestone rather than M1's: fixes a defect in M1's
own `formation.go` emission. `encoding/json`'s default HTML-escaping mangled
`<`, `>`, and `&` in hand-written text — a brief saying "if a < b" came back
re-escaped — breaking the envelope's contract to preserve what a human wrote
(the escaping exists to protect HTML embedding, which this file never does).
Fixed by carrying `SetEscapeHTML(false)` through the custom marshalers;
`MarshalIndent` would otherwise have re-escaped on the way out. The coder
self-found this in its own already-delivered code. Retraction for the record:
the reviewer had explicitly considered HTML-escaping at M1's review and
classed it harmless; that judgment is retracted in substance now that real
hand-written text is shown to hit it.

[Files Changed]
- cmd/cbus/formation.go, cmd/cbus/formation_test.go, cmd/cbus/usage.go: save
  dispatch and flags.
- internal/client/formation_save.go (new) + formation_save_test.go: roster
  capture, refresh-preserving merge.
- internal/client/liveness.go: small addition supporting save's on-channel
  check.
- internal/client/formation.go + formation_test.go (9b3d487):
  SetEscapeHTML(false) through the custom marshalers.

[Possible Ripple Effects]
- None to the wire or relay; local file-format and save-path work only.
- The four-fields-refresh contract is now load-bearing for any future
  formation verb that re-saves an existing envelope; deviating from it would
  silently drop human-owned fields.

[Testing Notes]
- go test ./... green repo-wide.
- Live-proven: save exercised against this formation's own channel, capturing
  its real (4-peer) roster.

Record-only: object keys still HTML-escape after 9b3d487 — the fix covers
values, a narrower edge case remains for keys; `drift_anchors.git_head`
anchors the saver's own repo and is advisory only, apply reports drift loudly
rather than trusting it; `SaveFormation`'s doc comment wording is ambiguous
against the four refreshing facts (wording only, no behavior change).

## [2026-07-16 18:51:03 UTC] [Client/Formations] Formations envelope (M1): typed save/load, hand-edit preservation, and the identity-clobber fix

[Attempt #1]

First milestone of formations v1: a formation is a saved snapshot of a channel's
shape, so a whole multi-session formation's peers and roles can be recreated on
demand. This entry covers the milestone commit and its confirmed fix together,
per the hold-for-verdict rule — the finding below is folded in, not appended
separately.

f4553ee adds the cbus-formation/v1 envelope: typed load/save at
$CBUS_DIR/.formations/<name>.json, atomic temp+rename writes, validation, and an
opaque by-reference payload the tool never interprets (a peer field is a pointer
into whatever durable store the orchestrator already uses — bd, a markdown file,
GitHub issues — never a contract cbus enforces). The file is meant to be
hand-editable: unknown keys are retained and re-emitted, known fields in spec
order with hand-edited keys sorted after them, and `fields()` single-sources both
the emission order and the known-key set so the two can't drift apart — a
reflection test fails the build if a struct field is ever added without a
`fields()` entry. Re-saving a loaded envelope reproduces it byte for byte. Ruling
D6: no converter for the old hand-authored v3-draft snapshots — loading an
unsupported schema id refuses rather than guessing at a migration.

Reviewer's verdict on f4553ee was CONDITIONAL APPROVE on one binding finding, C1:
a formation's identity is stated twice, once in the filename and once in the
`name` field, and `LoadFormation` didn't tie them together. Save derives its
write path from the field, so a file loaded under one name while carrying
another wrote itself over an unrelated formation — the reviewer's repro copied
roles.json to backup.json and showed that touching the copy destroyed the
original. 85207af closes it: refuse loudly, naming both remedies, rather than
silently adopting the requested name at load (adopting would revert a
hand-edited field, and surviving hand-edits is the envelope's whole premise). A
rename is therefore two-half: set the field and move the file; either half alone
is refused, because either half alone is where the clobber started. Reviewer
confirmed the fix end-to-end through the CLI — roles.json's checksum held, and
the guard caught the fix commit's own test fixture (far.json) as a live
positive.

An earlier framing floated mid-review — that a deliberate rename via the Name
field alone still works — was retracted before it reached the record: a
field-only edit is observationally identical to the clobber precursor, so intent
isn't recoverable from the file alone. The library-level flow (load, mutate Name
in memory, Save) survives the refuse-at-load guard, but that matters only to
save/copy call sites internally; a hand-editor still needs both halves.

[Files Changed]
- internal/client/formation.go (new in f4553ee, +12 lines in 85207af): envelope
  struct, fields(), atomic load/save, the reflection guard, and the C1
  name/filename identity check.
- internal/client/formation_test.go (new): 24 cases across load/save/validation/
  convergence.
- internal/client/formation_identity_test.go (new): the C1 repro and its
  regression.
- cmd/cbus/formation_test.go: adjusted for the identity check.

[Possible Ripple Effects]
- The identity refusal is a new failure mode for any future formation verb that
  loads by path instead of by name; list/show/rm/save all need to route through
  the same check rather than re-deriving it.
- No wire or relay surface touched; this milestone is local file-format work
  only.

[Testing Notes]
- go test ./... green repo-wide (24 focused formation cases plus the identity
  regression).
- Reviewer-verified live: roles.json checksum identical before/after a
  load+save round trip; the name/filename guard fired on both the reviewer's
  own far.json fixture and the constructed repro.

Record-only, not independently re-verified by the documenter: a WriteFile
failure can leave a temp file behind (cosmetic, no correctness impact);
`drift_anchors` field order normalizes on first save; in the copy-then-mv-remedy
case, following the guidance overwrites the original with the copy — that is the
user's own `mv`, done in view, not tool behavior.

## [2026-07-16 15:32:29 UTC] [Client/Spawn] spawn --role: committed role prompts ride the child's first turn

[Attempt #1]

Makes formations one command per peer. Until now, launching a role-carrying peer
meant `spawn --name <alias> --model <m>` plus hand-pasting the role brief as the
first dispatch; the role files shipped earlier (roles/*.md) were designed to be
pasted alone, and this flag does exactly that mechanically. `cbus spawn tab ch
--role documenter` now equals: reserve alias `documenter`, launch on the file's
MODEL: default (sonnet), and deliver join/arm instructions followed by the full
role prompt as the opening turn.

Design decisions, per the task's open questions:
- Resolution order: the spawn cwd's git-toplevel `roles/<r>.md` first (role files
  ship with the repo they serve), then `$CBUS_DIR/roles/<r>.md` as the
  machine-global fallback. Not-found errors list every path tried.
- The role file is read before any alias is reserved, so failures are side-effect
  free (verified: no channel dir created on a missing role).
- `branch` refuses `--role` outright rather than warning. A fork inherits its
  parent's intent; the B31 restore's ghost-orchestrator failure is the canonical
  case. The refusal message points at spawn.
- Recording the role in the child's meta.json is deferred to the formations work
  (it belongs with the join-side --role/role-capture design there).

[Files Changed]
- internal/client/role.go (new): LoadRole (resolution + read) and roleModel
  (first MODEL: line, screened like --model; flag-shaped tokens read as absent).
- internal/client/spawn.go: Spawn gains a role param; fills model/name defaults
  before the existing validation; appends the trimmed body to the prompt in both
  the local-aliased and remote-aliased paths.
- cmd/cbus/main.go: runSpawn extracts --role; runBranch refuses it before any
  side effect; spawn's success line notes "+ role brief".
- cmd/cbus/usage.go: --role documented under spawn, including the branch refusal.
- internal/client/role_test.go (new), spawn_test.go, cmd/cbus/main_test.go:
  MODEL parsing table, resolution fallback + repo-toplevel + not-found, alias and
  model defaulting, explicit-flag override, remote pre-assign, fail-before-reserve,
  branch refusal. Existing Spawn call sites gained the new parameter.
- internal/client/endpoint_test.go (separate commit): TestSiteURL blanks
  CBUS_SITE_NUC_URL so the suite passes in shells that configure the fleet.

[Possible Ripple Effects]
- Spawn's signature changed (new role param); all in-repo callers updated. The
  wire, spool, and relay are untouched; no coexistence surface moved.
- A role named like an existing reservation behaves exactly like --name with that
  value (same ReserveAlias path, same live-collision refusal).
- Remote spawns with --role always pre-assign the alias (a role implies a name),
  so the self-pick remote path never carries a role brief. Intentional.

[Testing Notes]
- go build ./... && go vet ./... && go test ./... green; -race green on
  internal/client and cmd/cbus.
- Live: branch --role refused (rc=1, teaching message); spawn --role ghost lists
  both tried paths, rc=1, no reservation; spawn tab roletest --role documenter
  launched `ccs personal --model sonnet --name documenter` with the doctrine
  block in the prompt argv, and the child self-joined + armed as
  roletest/documenter (cbus list: listen). Test peer dismissed after.

## [2026-07-16 07:42:19 UTC] [Docs/Roles] Role prompts for cbus formations — orchestrator, coder, reviewer, documenter

[Attempt #1]

A multi-session formation (four peers on a shared channel, one per role) distilled
the go-port formation's live dispatches into committed role prompts. The prior
formation's process rules — propose-then-code, facts-not-changelogs, verdict format,
verify-don't-trust, stage-before-verdict, milestone-hash reporting — existed only as
messages typed live by an orchestrator each time a new peer spawned. The design
constraint was the one alignment failure that motivated the work: a successor coder
once wrote its own changelog commit because a handoff carried the technical spec and
dropped the process rule that forbade it. So the shared standing-doctrines block is
duplicated byte-identical across all four files instead of factored out, and no file
references another — a role prompt has to survive being pasted alone into a fresh
window with nothing else loaded.

Base commit (411c083) landed four files: a mission, the doctrine block, role-specific
process rules, a literal report/verdict template, escalation guidance, and anti-patterns
per role, plus a MODEL: line giving `spawn` a per-role default (orchestrator fable,
coder opus, reviewer fable, documenter sonnet).

An independent reviewer session then read the four files cold against the source
transcripts and returned CONDITIONAL APPROVE: one binding fidelity fix plus riders.
The binding fix (F1): doctrine 2 claimed "anything queued while you were dark replays
on the next arm," true only on the relay path. Locally, only the first arm replays —
every re-arm seeks to end, so messages queued while a listener was dead are skipped
silently. As originally written, a local peer would trust replay and never think to
ask for a resend; the fix scopes the claim per path and tells the local case to assume
loss and ask. Riders folded into the same follow-up commit (4e0a497): doctrine 10
gained the same treatment (a rename orphans the old alias; a send to it fails silently
only on the relay path, dies loud locally), orchestrator.md gained a dispatch-anatomy
rule (a confirmable first reply, since an ack alone proves nothing) plus kickoff and
review-request templates, coder.md gained a ruling-request form (lettered options,
explicit recommendation, what proceeds meanwhile) and dropped "one milestone, one
commit" as a wrong absolute — a milestone may legitimately span several commits; the
real invariant is that two milestones never share one. A polarity rule also went into
orchestrator.md: two peers deadlocked earlier in the effort because a mutual dependency
was framed with opposite polarity to each of them, and each was correctly following
what it had been told.

Re-review of 4e0a497 came back APPROVED with one class-C note (n5): doctrine 10's new
wording still said sends to a stale address fail silently without naming which path,
the same error class as F1 one commit earlier. Class-C semantics let the coder fold it
without a further review round; b3a806e scopes the claim explicitly to the remote path
and re-verifies the doctrine block is still byte-identical across all four files.

Notable process finding: a blind extraction cross-check compared what the coder session
and the reviewer session each independently flagged as gaps against the source
transcripts. Zero overlap — the two passes were complementary (one mined peer behavior,
the other mined artifact claims), which is itself evidence for running review as a
second, independently-sourced pass rather than a checklist against the same material.

[Files Changed]

- `roles/orchestrator.md` — mission, doctrine block, dispatch-anatomy rule, kickoff +
  review-request templates, polarity rule.
- `roles/coder.md` — mission, doctrine block, ruling-request form, milestone/commit
  invariant correction.
- `roles/reviewer.md` — mission, doctrine block, verdict format, class-C fold semantics.
- `roles/documenter.md` — mission, doctrine block, changelog/tracker-write process rules.

(429 lines across three commits: 411c083 adds all four files; 4e0a497 and b3a806e are
scoped follow-ups touching the same four.)

[Possible Ripple Effects]

- Prompts only — no `spawn --role` plumbing yet, so nothing currently consumes the
  MODEL: line or auto-attaches these files to a spawned peer.
- The doctrine block is intentionally duplicated, not shared. Any future edit to a
  standing doctrine has to be applied to all four files by hand; the files carry no
  cross-reference to enforce that, by design.
- Two remaining gaps noted but not folded (record-only, deferred to a future pass):
  reviewer.md doesn't mention staged-confirmation harnesses or declaring one's own
  loopback traffic, and lacks a tracker-reference line analogous to the other three.

[Testing Notes]

- Reviewed end to end by an independently-sourced reviewer session against the go-port
  transcripts, not against the coder's own summary of them.
- Verdict classes exercised live: CONDITIONAL APPROVE (411c083, binding fix required),
  APPROVED + class-C (4e0a497, self-fold without re-review).
- Two live template dogfoods during the same effort: the review-request template (F2)
  was used for the real review request that produced this verdict; the ruling-request
  form (F4) was used for actual ruling requests before it was formally codified in
  coder.md.

## [2026-07-14 23:36:41 UTC] [Relay/Presence] Remote presence MVP — join/departed cross the relay (cbus-ijx.5)

[Attempt #1]

Carlos noticed presence notifications don't fire on relay channels and disliked the
local/relay asymmetry. Root cause (from a source + git + docs dig): presence was a
shared-filesystem primitive (BroadcastPresence appends to peer inbox files); the relay,
built 5 days earlier as a pure message transport, has no shared filesystem and no `kind`
on its wire, so presence had no path across it. The bridge was filed as cbus-ijx.5 the
day presence landed and deliberately deferred as "wire-touching, needs relay+client
lockstep + protocol versioning."

Key insight that unfroze it: the CORE (join + departed) is doable relay-only, no wire
break, no mandatory client code change — presence on a relay channel is a
connection-lifecycle fact the relay already owns (hub.attach/detach), and the relay can
pre-render the frame server-side (Reframe with kind=presence) so an unmodified client
renders it. Scoped with a Fable 5 session (adversarial design review) which replaced my
first mechanism (a per-tail push channel) with spool-mediated fan-out: write presence as
an ordinary spool line to each connected recipient + poke, the same path /send uses —
FIFO-preserving, no new concurrency, durable across the enqueue/drain race.

Then a SECOND Fable review of the implementation caught a real bug (see below), fixed
before any deploy.

[Files Changed]

- internal/core/frame.go — Reframe decodes an optional `kind` and appends ` kind=<k>` to
  the header (same position as LocalEmit, before the oversize check so kind never
  perturbs the WSFrameSafe math). Kind-absent lines render byte-identically (golden
  corpus.jsonl has 0 kind lines); this converges Reframe with LocalEmit instead of adding
  a third framer. Reversed the doc comment ("relay path DROPS kind" → renders it).
- internal/core/frame_test.go — the divergence-matrix case that asserted "kind dropped"
  now asserts `kind=presence` rendered (the deliberate contract reversal).
- relay/cmd/cbus-relay/main.go:
  - New const presenceGrace=90s (DISTINCT from pongGrace, commented: keepalive vs UX
    debounce) + a `-presence-grace` flag (tuning/live-testing).
  - hub gained present/departWait(map[string]uint64)/departSeq/grace/pending/wake +
    presenceEvent type; dropped the first cut's departTimers/departGen/onDepart.
  - attach returns (t, joined) and ENQUEUES a `join` under mu when present flips
    false→true; a reconnect deletes departWait[key] (invalidates the pending departed);
    displacement keeps present=true (not a join).
  - detach returns displaced (tails[key] != t) so only a real disconnect schedules a
    departed.
  - scheduleDepart arms time.AfterFunc(grace) stamped with a global-monotonic seq; on
    fire, under mu, iff still-gone + still-present + seq matches, it deletes present +
    departWait (self-cleaning, no leak) and ENQUEUES `departed`.
  - fanoutPresence(channel,actor,event,text): snapshot() → for each connected peer on the
    channel except the actor, marshal {from,to,ts,text,kind:"presence",event} → store.Write
    + poke. No lock held across the writes.
  - presenceDrainer goroutine: grabs the whole pending batch under a brief lock, resets it
    (drops the consumed backing array), fans out each in order. presenceText renders honest
    wording ("connected as <alias>" / "departed (connection lost)").
  - handleTail: `t,_ := attach(key)` (attach enqueues the join); defer `if !detach(key,t)
    { scheduleDepart(key) }`. main(): `s.hub.grace = *graceFlag; go s.presenceDrainer()`.
- relay/cmd/cbus-relay/presence_test.go — 10 tests (see Testing).
- docs/architecture/protocol.md — reversed the three "presence never crosses the relay" /
  "relay strips kind" / "kind only on the local path" statements to describe the new
  relay-generated join/departed. README.md + CHEATSHEET.md gained a relay-presence note.

[The bug Fable caught — BUG #1, fixed]

First cut committed the departure UNDER hub.mu (present=false) then fanned out OFF the
lock (an onDepart callback). Interleaving: (1) timer fires, sets present=false, unlocks;
(2) before onDepart runs, the peer reconnects — attach reads present=false, decides a
FRESH join, fans out "join"; (3) onDepart resumes, fans out "departed". Peers see
join-then-departed for a currently-connected peer, present ends true but the last event is
departed and nothing re-announces → the peer is PERSISTENTLY shown as departed until it
really disconnects and returns. Advisory-only (send ignores presence) but persistent, and
its trigger is exactly the sleep/wake reconnect presence exists for. `-race` green did not
catch it (logical event-ordering race, not a data race). Fix: decide AND enqueue under mu,
one drainer emits in enqueue order, so decide-order == emit-order — a departed decided
before a join is emitted before it, and the last event a returning peer shows is the join.
Also fixed the map leaks (present now deleted on fire; departWait/global-seq replaces the
leaking departGen/departTimers) and moved the join fan-out off handleTail.

[Possible Ripple Effects]

- Contract reversal: Reframe now renders kind. Only presence lines carry kind today; the
  no-kind (message) path is byte-identical, so the golden corpus and all message framing
  are unaffected. A stored relay line gaining kind/event is relay-internal — nothing reads
  spool bytes but Reframe; not client-visible wire, so no wire break and old clients render
  presence frames as normal `kind=presence` blocks.
- Departed latency for silent death is ~3-3.5 min (detach waits out pingEvery+pongGrace
  ≈120s, THEN the 90s grace). Documented; do not shrink pongGrace to speed it up.
- Relay restart wipes memory-only hub state → a join storm (O(N^2)) + any pre-restart death
  never departs. Accepted at fleet size 2-5.
- A presence line queued to a recipient that drops before draining counts as queued mail →
  `cbus prune @host` keeps that peer until it drains. Narrow; a drain-time TTL is a later fix.
- spawnPromptRemote's guidance (rely on `cbus list`) is now incomplete (peers also get
  pushed presence). Not false, not harmful; a one-line client prompt update is a follow-up
  needing a client rebuild.

[Testing Notes]

- go build ./... + go vet clean; gofmt-clean; full suite green with -race (5x on the
  timing-sensitive presence tests). Relay presence tests: attach join decision, fan-out
  routing (kind=presence, per-recipient to=, actor skip, channel scope), join delivered via
  drainer, departed-after-grace, reconnect-cancels-departed, displacement-no-depart,
  join-after-departed, BUG#1 ordering regression (enqueue [departed,join] → delivered in
  that order), concurrent-hub stress (8×400 attach/detach/scheduleDepart/poke, -race clean,
  no deadlock), e2e Reframe-over-spool (a fanned line renders kind=presence).
- Live validation on a loopback relay (MBP, `-presence-grace 2s`, production NUC untouched):
  bob joins → alice gets `kind=presence` "connected as bob"; bob drops → after grace alice
  gets `kind=presence` "departed (connection lost)"; both in order.
- NOT deployed to the NUC. The relay currently serves live `listen` sessions on
  android-sdk; a restart would blip those four tails. Deploy in a quiet window via
  relay/deploy.sh (build-on-NUC), backward-compatible, no client swap needed.

## [2026-07-14 06:50:11 UTC] [Relay/CLI] Server-side relay prune — `cbus prune [<ch>]@<host>`

[Attempt #1]

Carlos asked how to prune the relay: local `cbus prune` works but nothing reaches
the `@nuc` relay. Root cause: the relay's `/peers` view is the union of the
in-memory hub (ephemeral, self-cleans on WS disconnect) and the on-disk maildir
spool, and the spool is **append-only** — `spool.ensure` creates a peer's
`<ch>/<al>/{tmp,new,cur}` on its first queued write and nothing ever removes it.
So any peer that ever received one message shows up in `cbus list @host` forever
as `off / queued 0`. Local prune keys on `listenerPid` liveness; the relay holds
no pid and lives on another host, so prune structurally cannot reap it. There was
no relay prune endpoint and no client subcommand. This adds both.

Contract: a spool peer is pruned iff it has **no live tail in the hub** AND **no
queued mail in new/**. A peer with pending mail is always kept (nothing
undelivered is lost); a connected peer is always kept. Channel-scoped like local
prune — `<ch>@host` prunes one channel, bare `@host` sweeps all.

[Files Changed]

- internal/core/message.go — new `PruneResponse{Pruned []string}` (POST /prune
  body; `json:"pruned"`, always a slice, never null).
- relay/internal/spool/spool.go — added `strings` import. `Peers` now skips
  dot-prefixed entries at BOTH channel and alias levels (matches the local
  store's convention; also shields Remove's transient `.prune.*` claim dir from
  being misread as a peer). New `Remove(channel, alias) (bool, error)`: claims the
  peer dir via an atomic same-parent rename to `.prune.<pid>.<seq>` (EXDEV-proof,
  dot-hidden), rechecks `new/` in the claimed copy, and RESTORES the peer if a
  message raced in between the caller's snapshot and the claim — so a concurrent
  `Write` is either fully spooled (peer kept) or fails its final `new/` rename
  (surfaced to the sender), never silently dropped. rmdir's the channel once
  empty. Missing peer = (false, nil), not an error.
- relay/cmd/cbus-relay/main.go — added `sort` import. New `handlePrune`: POST-only,
  bearer-auth (same gate as /peers), optional `?channel=` (ValidName-checked, 400
  on bad). Enumerates `store.Peers()`, filters to `queued==0 && !connected` (via
  `hub.snapshot()`), calls `store.Remove`, returns sorted `PruneResponse`.
  Registered `mux.HandleFunc("/prune", …)`.
- internal/client/remote.go — added `net/url` import. New `RemotePrune(e, channel)`
  → POST `<base>/prune[?channel=…]`, decodes `PruneResponse.Pruned`. Mirrors
  RemoteList's error/timeout handling (no retry).
- cmd/cbus/main.go — `runPrune` gets a remote branch (`client.IsRemote(args[0])`);
  new `runPruneRemote`: parses `<ch>@host`, REJECTS a trailing `/alias`
  (channel-scoped only — footgun guard on a destructive op, whereas `runListRemote`
  silently ignores the alias), resolves creds, calls RemotePrune, prints
  `pruned <ch>@<host>/<al>` per key or `nothing to prune`.
- cmd/cbus/usage.go, README.md, CHEATSHEET.md — documented the local `[channel]@host`
  form and the remote-section `cbus prune [<ch>]@<host>` line; README gains a
  "relay peers are append-only" bullet explaining why local prune can't reach them.
- Tests: relay/internal/spool/spool_test.go (Remove keeps-mail / removes-idle /
  rmdir-channel, RemoveMissing no-op, Peers skips dot-dirs); relay/cmd/cbus-relay/
  prune_test.go (handlePrune: channel scope, connected-keep, mail-keep, unscoped
  sweep, then auth 401 / no-auth 401 / GET 405 / bad-channel 400).

[Possible Ripple Effects]

- `spool.Peers` no longer reports dot-prefixed peers. `core.ValidName` permits
  leading-dot names (documented quirk), so a peer literally named e.g. `.foo`
  would now be invisible to `/peers` and un-prunable via this path. Nobody creates
  such names (branch/spawn/join produce main/fork-N or user names); the local
  store already skips them. Acceptable, and noted in the code comment.
- A concurrent `cbus send` to a peer being pruned can now fail its `new/` rename
  (ENOENT) and return an error to the sender instead of silently spooling into a
  dir that's about to be deleted — strictly better (sender can retry) and rare.
- No wire-compat break: /prune is a new route; /send, /tail, /peers, /healthz
  unchanged. Old clients simply never call it.

[Testing Notes]

- `go build ./... && go vet ./...` clean. `go test ./...` green (the one
  `TestSiteURL` failure is a pre-existing env leak — `CBUS_SITE_NUC_URL` is set in
  Carlos's shell; `env -u CBUS_SITE_NUC_URL go test ./...` is fully green — and it
  exercises endpoint.go, untouched here).
- Live HTTP smoke against a loopback relay (127.0.0.1:18090, temp spool+token):
  seeded c/idle (delivered→empty new/), c/pending (queued 1), other/idle
  (delivered). `POST /prune?channel=c` → `["c/idle"]`; c/pending + other/idle
  survive. `POST /prune` → `["other/idle"]`; c/pending survives. `POST
  /prune?channel=c` again → `[]`. On disk the fully-drained `other` channel dir
  was rmdir'd (only `c` left). Client `cbus prune foo@nuc/bar` → `prune is
  channel-scoped: use <channel>@nuc or @nuc`, exit 1, BEFORE any keychain/network
  touch.
- NOT redeployed to the NUC. The relay binary there is pinned (orchestrator quiesce
  handoff — binaries at 1a5821d until the pre-push window); ship this in that same
  window via the Go cross-compile + scp path (never install.sh).

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
