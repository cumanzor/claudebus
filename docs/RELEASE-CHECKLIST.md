# cbus release checklist

## Current release procedure

1. Complete the acceptance gates for every adapter the release touches: the
   [Codex v1 readiness gates](architecture/codex-v1-release-readiness.md) for Codex,
   and the Claude canaries under `scripts/` (e.g. `claude_cbus_canary.py`,
   `claude_interactive_wake_canary.py`) for the native Claude adapter, against one
   frozen source revision. Keep raw canary artifacts local and attach aggregate
   outcomes plus hashes to the tracker. A build hash identifies bytes; a version
   label alone does not. A client-only release with no adapter or protocol change
   (v0.14.0 and v0.14.1 were both client-only) skips this step.
2. Review and commit the complete change, then build from a clean checkout of that
   exact revision. A release that changed an adapter runs Go tests/vet/race checks,
   ordinary CLI, native queue, presence, permissions, compaction, resume, terminal
   and relay acceptance, and documents the tested Codex version and any platform
   exclusions explicitly. A client-only release runs the smaller gate instead: two
   fresh clones, `go vet` plus the race suite, and the five assets reproduced
   byte-identically (the v0.14.0 record).
3. Build **five client binaries**: Darwin amd64/arm64, Linux amd64/arm64 and Windows
   amd64 (`.exe`). Keep SHA256SUMS alongside them. Verify a second clean build
   reproduces the assets before publication; preserve the source revision and
   build-info evidence. Native Codex connect remains macOS/Linux only.
4. Prepare the tag and release notes for review; also prepare a matching relay
   build/deployment plan when the release changes relay code or the wire contract
   (the relay itself has been unchanged since v0.13.0). Do not publish merely
   because a local candidate is green. The native daemon requires
   `/tail/durable-v1`; old relay `/tail` remains compatible with Monitor clients,
   but does not provide durable client acknowledgment.
5. After release authorization, tag the tested revision (`git tag vX.Y.Z`) and
   publish from a clean tree with `make release CBUS_REPO=owner/repo` (refuses a
   dirty tree; build from a fresh clone at the tag for provenance, as the v0.14.0
   record did). Verify downloaded bytes against the prepared hashes and run
   install/selfupdate on Mac and server. Do not rebuild different bytes under the
   same tag.
6. Verify command/role/Codex-skill refresh. Older updater binaries need one manual
   `cbus install-codex-skills` after upgrading. Preserve modified Codex skills;
   permission rule installation is always a separate explicit opt-in.
7. Restart an existing daemon explicitly with the new executable; verify reported
   version/protocol, retained connection epoch and pending messages. An old pilot
   without process fencing needs explicit stop, confirmed exit, then start.
8. When the release changed relay code, deploy the exact matching relay build only
   after its separate review/approval, and check remote connect/send/reply and
   reconnect against that running endpoint. A client-only release has nothing to
   deploy here.

## Historical first-release ledger

> **STATUS: executed.** The quiesce window ran and v0.1.0 shipped 2026-07-17;
> the following is the historical first-release record. This section is preserved
> as the pre-release verification ledger — the per-release sequence (tag,
> `make release`, `cbus selfupdate`) still applies to every new release.

The distribution machinery (cbus-7sg) is built as local commits. Everything that
talks to a real GitHub release cannot be exercised until a release exists, which is
gated behind the quiesce window (history scrub, then a private remote). This file is
the ledger of what runs only after the first release, so nothing gh-facing is
mistaken for tested.

## Sequence (Carlos-gated, after the quiesce window)

1. Quiesce window per cbus-kt3: history scrub, verify, `gh repo create --private`,
   push. Nothing here happens before the scrub.
2. Tag the release commit: `git tag v0.1.0`.
3. Cut the release: `make release CBUS_REPO=<owner/repo>`. This cross-compiles the
   four assets, bakes the slug into them via ldflags, and publishes with
   `gh release create` (a `-` in the tag would mark it a prerelease selfupdate
   ignores). Written and reviewed; never run in this effort.
4. Bootstrap each machine once: `curl -fsSL <raw get.sh> | CBUS_REPO=<owner/repo> sh`.
   The server already has gh authenticated with repo scope, so it works there day one.
5. Thereafter update in place: `cbus selfupdate`. No manual install ever again.
6. The formations live smoke (cbus-zmv) runs on the RELEASED binary, after this — not
   on a manually built one.

## Paths that can only be verified against a real release

Each is unit-tested at the helper level and driven through injectable seams where one
exists; the true gh round-trip is checked here, by hand, once step 3 has run.

- **`cbus selfupdate`** end to end: `gh release view` → `gh release download` of the
  exact `cbus-<os>-<arch>` asset → the version-gate (downloaded `--version` equals the
  tag) → the in-place swap → the commands/roles refresh. Verify on both a Mac (rename
  swap) and the server (its tmpfs `/tmp` forces the cross-filesystem copy leg).
- **`cbus selfupdate --check`** against the live latest tag (dev-build, up-to-date, and
  update-available lines).
- **`get.sh`**: a clean bootstrap on a machine with no cbus, plus the `CBUS_REPO`
  unset, bad-repo, and unsupported-arch refusals.
- **`refreshUpdateCache`** (the `CBUS_UPDATE_CHECK=1` detached poll): confirm it writes
  `~/.config/cbus/update-check.json` and that the next invocation prints one hint.
- **`make release`** itself: the tag gate, the `CBUS_REPO` requirement, and the
  prerelease branch for a `-` tag.

## Not part of this

In the historical v0.1 effort, the relay was a separate binary on `relay/deploy.sh`
and was untouched. Codex v1 now has a coordinated relay compatibility requirement
under the current procedure above.
`install.sh` (the bash rollback) and `install-cbus-go.sh` (the transitional installer)
were retired from the tree after the first release; recover them from git history if
ever needed.
