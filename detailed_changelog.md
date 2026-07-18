# Changelog (detailed)

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
fail, and the failure would show up as stderr noise that PostCompact surfaces
to the user in the transcript.

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
is a wire change plus a relay redeploy — deferred, not faked, and there's no
network call available inside the compaction window regardless.

**Reviewer verdict:** APPROVED — single commit `19dd20b`, one review finding
(C1) scoped to tests only, no impact on documented behavior.

[Files Changed]
- `cmd/cbus/main.go` — dispatch case + `runHookCompact` (19dd20b)
- `cmd/cbus/update_check.go` — `hook-compact` added to the update-check skip
  list alongside `hook-exit` (hook targets stay quiet) (19dd20b)
- `internal/client/harness.go` — `HookCompact`, `compactText`, shared
  `hookInput`/`readHookInput` (refactored out of the pre-existing
  `hookSessionID`) (19dd20b)
- `cmd/cbus/hook_compact_test.go` (new) / `internal/client/harness_test.go` —
  per-channel broadcast, both phases, trigger allowlist, env fallback,
  no-session no-op, bad phase, remote markers untouched, plus a users-door
  test that builds the real binary and drives it with the documented hook
  payloads (19dd20b)
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
