# cbus release checklist

> **STATUS: executed.** The quiesce window ran and v0.1.0 shipped 2026-07-17;
> the repo is public and releases exist through v0.9.2. This file is preserved
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
   The NUC already has gh authenticated with repo scope, so it works there day one.
5. Thereafter update in place: `cbus selfupdate`. No manual install ever again.
6. The formations live smoke (cbus-zmv) runs on the RELEASED binary, after this — not
   on a manually built one.

## Paths that can only be verified against a real release

Each is unit-tested at the helper level and driven through injectable seams where one
exists; the true gh round-trip is checked here, by hand, once step 3 has run.

- **`cbus selfupdate`** end to end: `gh release view` → `gh release download` of the
  exact `cbus-<os>-<arch>` asset → the version-gate (downloaded `--version` equals the
  tag) → the in-place swap → the commands/roles refresh. Verify on both a Mac (rename
  swap) and the NUC (its tmpfs `/tmp` forces the cross-filesystem copy leg).
- **`cbus selfupdate --check`** against the live latest tag (dev-build, up-to-date, and
  update-available lines).
- **`get.sh`**: a clean bootstrap on a machine with no cbus, plus the `CBUS_REPO`
  unset, bad-repo, and unsupported-arch refusals.
- **`refreshUpdateCache`** (the `CBUS_UPDATE_CHECK=1` detached poll): confirm it writes
  `~/.config/cbus/update-check.json` and that the next invocation prints one hint.
- **`make release`** itself: the tag gate, the `CBUS_REPO` requirement, and the
  prerelease branch for a `-` tag.

## Not part of this

The relay is a separate binary on `relay/deploy.sh` (build-on-NUC) and is untouched.
`install.sh` (the bash rollback) and `install-cbus-go.sh` (the transitional installer)
were retired from the tree after the first release; recover them from git history if
ever needed.
