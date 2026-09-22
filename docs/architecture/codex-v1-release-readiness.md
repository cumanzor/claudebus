# Codex CLI v1 release readiness

Updated 2026-09-18. Release tracker: `cbus-6ij.21`; broader epic: `cbus-6ij`.

**Verdict: Codex CLI v1 is released; v0.12.2 was installed on Mac and server.**
[v0.12.2](https://github.com/cumanzor/claudebus/releases/tag/v0.12.2) uses source
`fa0d206833b3a3b19cd2200226cd134c269e22b5`. It fixes current CLI connections
rejected by saved thread source metadata. Current CLI ownership and queue-store
identity remain required; historical source labels do not establish the current
runtime. Desktop attachment remains outside v1.

**Status (2026-09-22): unchanged through v0.13.0-v0.14.1.** v0.13.0 added the
native `cbus connect`/`spawn --harness codex` path alongside native Claude
receive ([docs/codex.md](../codex.md)), tested against Codex 0.155.1 (macOS)
and 0.154.0 (Linux); v0.14.0 and v0.14.1 were client-only releases that did
not touch the Codex adapter. This doc's v0.12.2 evidence below remains the
readiness record for the wrapper/queue path that native connect now leads
with (`command-reference.md` §9).

## v0.12.2 release and installation evidence

- All five client assets reproduced byte-for-byte from independent clean source
  clones. Published downloads matched the prepared hashes. Source tests, vet and
  the client/CLI race suites passed. Provenance: `/tmp/cbus-v0122-provenance.json`.
- Final-asset acceptance used actual Codex CLI 0.154.0 and local fake providers:
  80 assertions on Mac, 51 on Linux. Coverage includes saved-source CLI resume,
  automatic multi-home/profile/SQLite binding and restart delivery; Mac also
  exercised trusted-bus commands. Result-only evidence:
  `/tmp/cbus-v0122-acceptance-result-only.json`, tracker attachment
  `78347441d578773e`, review `fe7debe4410e4d0c`.
- Normal selfupdate installed the matching assets and the daemons restarted on
  v0.12.2. Configuration, Codex rules and seven saved registrations were retained.
  Mac: `/tmp/cbus-v0122-install-mac.json` (107 checks); server:
  `/tmp/cbus-v0122-install-server.json` (59 checks). Saved registrations are not a
  count of online peers. No pending work existed before this upgrade, so these
  install checks do not prove pending-message recovery or recipient delivery.
- Installed Mac arm64 SHA-256:
  `55a435401d579a1a3ddba4da0584674a7b94c286ff8cb8d7c01d4f8ed7dc8f69`.
  Installed Linux amd64 SHA-256:
  `b60577ba007a81abaf8604c6b4ce5ee06dc66f7650dbcf0fe7ff413b301936d2`.
- The relay was unchanged and was not redeployed in v0.12.2. This rollout did not
  repeat a live cross-machine message exchange. The CC Monitor idle-cost issue
  remains separate work.

## Historical v1 pre-release acceptance snapshot

The remainder preserves the earlier `cbus-6ij.13` readiness record. Its gate
statuses, environment versions and local artifacts describe that earlier stage,
not current deployment. These candidate results are not v0.12.2 binary evidence;
the narrower final-asset checks above are recorded separately.

The native CLI integration, compatibility fixes and coordinated relay changes
are implemented. Intermediate acceptance below is strong evidence, but it must
not be attributed to a different final binary. The approved isolated iTerm2 GUI placement check now passes with mock children;
real Codex execution is established by the separate ordinary CLI canaries.

Codex CLI is first. Claude Code and OpenCode daemon adapters remain later epic
work, including the six directed routes between all three harnesses. Desktop
harness clients are v2. Terminal choice is independent of harness choice:
iTerm2/tmux launch support and ordinary manual CLI launch remain in v1.

| Requirement | Implemented behavior and evidence | Gate recorded before release |
|---|---|---|
| Fair delivery and responsive controls | Independent connection lanes, bounded shared workers, immutable status snapshots; blocked sidecars/peer locks do not hold the status mutex; cancellation and contention tests pass | Full race suite passes after final observer change |
| Exact runtime/config binding | Actual CLI process/start and open queue database witness; captured HOME, CODEX_HOME, cwd and executable. Real automatic connects across separate homes, profiles and `-c` SQLite overrides pass on Mac and Linux | Final Linux runtime check passes; record source/asset provenance |
| Honest availability and receipt | Consumer online/exited/unknown/disconnected is separate from queue storage and historical receipt; reconcile uses exact client ID in recipient history | Preserve these distinctions in release notes |
| Presence and lifecycle | Real CLI join/exit/resume/disconnect, durable outbox and recipient epoch checks; 34-check Mac and Linux tests pass; no invented transition on inconclusive probes | Final Linux runtime check passes; record source/asset provenance |
| Durable recovery | Attempt journal precedes submission; unresolved outcomes stop delivery rather than blindly replaying. Reconcile/explicit abandon, disk fault and ownership fencing tests pass | Full race suite passes |
| Current native resume | UUID 27 checks, `--last` 27, name 28, picker 27; interrupted cold resume 18 on Mac and Linux. Same history, exact queued/received IDs, new CLI PID, stable daemon | Final Linux presence/resume passes 34/34 |
| Terminal launch and placement | Native Codex spawn preserves cwd/config while clearing inherited identities. Actual tmux placement and failure cleanup pass 9 checks with mock children; separate real CLI tests establish boot/connect. Stale known iTerm anchors refuse | iTerm2 pane/tab/window and stale anchor checks pass 20/20 with mock children; real CLI execution is checked separately |
| Formation safety | Harness/backend persist through save/roster; Codex restore/bootstrap explicitly refuses with manual resume/connect guidance before Claude fallback | Full race suite passes |
| Relay capability | Additive durable endpoint, append-before-ACK, stable IDs, reconnect dedup, consumer ownership and journaled presence; real-server integration/race/conformance checks pass | Ordinary CLI remote connect/send/reply/restart/disconnect passes 12/12; prepare coordinated relay deployment |
| Permissions and installation | Protected skill content receipts; selfupdate includes skills; optional exact-path send rule requires explicit installation. Real tool denied with no rule (12 checks), one received reply with rule (11) | Installer/rule and selfupdate tests pass; first upgrade from old updater needs one manual skill install |
| Running daemon upgrade | Version/protocol check, PID/start fenced stop, socket closure plus lock release before start. Two-version process canary passes 5 checks. Metadata listener rearm precedes health; immediate post-restart send passes in two-home CLI test | Final Linux runtime check passes |
| Wrapper compatibility | Actual reply without `--from`, UUID/last/name/picker, and graceful/SIGTERM/SIGHUP child cleanup pass 35 checks. Caller policy preserved; native npm child teardown fixed | Fresh/resumed wrapper compaction passes 19/19 on Mac and Linux; focused regression passes |
| Compaction notices | User decision: completed only in v1; pre-hooks later. Native durable rollout completion emits one fixed local notice; 11 checks, no pre-notice or replay on restart | Wrapper uses exact persisted completion records because late connections lack a reliable item subscription; 19/19 actual checks pass |
| Platform and provenance | Actual Mac and isolated Linux 0.154.0 tests pass. Server installed 0.135.0 and existing sessions untouched. Windows native connect/daemon explicitly unsupported | All five client builds and Windows client/CLI test compilation pass; clean-source artifact preparation remains. Actual Windows runtime is not established here |
| Product documentation | Native connect first, terminal independence, permissions, upgrades, relay contract, formation limits and pre-hook deferral documented | README, operations, command reference, cheatsheet and changelogs updated; prepare reviewable release notes |

### Historical acceptance evidence

The scripts under `scripts/` use temporary homes and local fake model providers.
They verify real CLI execution and transport mechanics without paid inference;
they do not establish live-model reasoning quality. Raw transcripts stay local.
Tracker attachments contain aggregate outcomes and hashes only.

Intermediate evidence (paths are local to this development workspace, under
`/tmp` or `/private/tmp`; most are not retained past the machine's normal temp
cleanup and are provenance of what ran, not reachable artifacts). Of the
evidence in the "v0.12.2 release and installation evidence" section above,
only the final-asset acceptance run has a retained tracker attachment
(`78347441d578773e`, review `fe7debe4410e4d0c`); its provenance and
install-check files are the same kind of local-path-only record as below:

- Permissions: `/private/tmp/cbus-cli-canary-0unj_hbm` is an earlier permission
  fixture, **not proof that an allow rule was necessary**. The controlled pair
  excludes temporary directories from the scratch sandbox: denial
  `/private/tmp/cbus-cli-canary-7q0vtut5/result.json`; allowed delivery
  `/private/tmp/cbus-cli-canary-r17qv_6x/result.json`.
- Native compaction: `/private/tmp/cbus-cli-canary-t89smudm/result.json`.
- Interrupted ordinary CLI: `/private/tmp/cbus-cli-canary-pbwdvcue/result.json`.
- Native selectors: `rbhxyh3k` (`--last`), `hn_7r99u` (name), `o2y77lnh` (picker),
  each under `/private/tmp/cbus-cli-canary-*/result.json`.
- Wrapper: `/private/tmp/cbus-cli-canary-dweugzlz/result.json`.
- Multi-home/profile contrast: `/private/tmp/cbus-cli-canary-mvx9cw7k/multi-home-result.json`.
  After startup fix: `/private/tmp/cbus-cli-canary-6nxy1c5_/multi-home-result.json`
  additionally proves listeners are ready immediately when restart returns
  (5 shared checks plus 9 per peer).
- Linux: `/tmp/cbus-linux-v1-evidence/`; binary SHA-256
  `5f43cba558b6464cd5490fea1343db1a7c66de63198cad59cdd991e2c40219dc`.
  Presence 34, interrupted resume 18, multi-home 4 shared plus 9 per peer;
  all reported cleanup checks pass. Approved remote test root:
  `/tmp/cbus-linux-accept-xyk_6ss5`; no production relay deployment.
- Terminal placement: `/tmp/cbus-tmux-check-1m5wzbhr/result.json` (mock children).
- Two-version daemon upgrade: `/tmp/cbus-upgrade-check-w3mn7gk8/result.json`.
- Result-only milestone attachment `ce65b06dd9e2ecc9`, review `13b3d0de4e37b1e8`.

The earlier 65-minute ordinary CLI soak measured 3900.032 seconds with zero
recorded maintenance activity, followed by an exact PONG in 9.454 seconds and a
21-second duplicate guard. This used an existing send allow rule. Recorded usage
is not an exhaustive billing record, and that old candidate predates this work.
Real presence and compaction events can cause recipient turns; idle supervision
must not cause periodic model turns.

### Historical publication boundary

At this snapshot, the remaining gates preceded
[the release checklist](../RELEASE-CHECKLIST.md). No release, tag, global
installation, user config change or production relay restart had yet been
performed. A coordinated relay upgrade was required for native remote
subscriptions; local connections did not depend on it. This is historical,
not a statement that the subsequent releases or installations are still pending.

### Historical final-preparation artifacts

- Final full race suite: `/tmp/cbus-v1-final-race2.log`; `go vet ./...` and
  `git diff --check` pass. Windows client and CLI test binaries compile.
- Mac completed wrapper notices: `/private/tmp/cbus-cli-canary-chb4ezn5/result.json`
  (19/19, all cleanup true), candidate SHA-256
  `0a8ecc9ed0d51b561a0a46c83807947abc6455cecb2937e2e3438fd4252f894d`.
- Native presence `/private/tmp/cbus-cli-canary-4q8wdp1q/result.json` (34/34),
  native compaction `4644jb1d` (11/11), wrapper compatibility `w1xntdry` (35/35)
  are intermediate source-equivalent checks for their paths.
- Real native relay CLI: `/private/tmp/cbus-cli-canary-n2jz5aq2/result.json`
  (12/12, five cleanup checks true); no Keychain or production relay used.
- iTerm2: `/private/tmp/cbus-iterm-check-klzvaql4/result.json` (20/20, cleanup
  errors empty); only explicitly created test windows were closed.
- Final Linux binary SHA-256
  `d695dc2401e07f37eb0355edb3eb053576cb29b677b7cfc9bd09943ac290bfd5`:
  presence `_lvejmoi` (34), wrapper compaction `knxlizcw` (19), multi-home
  `qik9cckz` / `7yeu_kn4` (5 shared + 9 per peer), all passed beneath the
  approved server temporary root. Installed binaries remain untouched.
