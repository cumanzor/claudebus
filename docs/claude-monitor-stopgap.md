# Monitor stopgap: keeping `persistent` while we build the real receive path

Status: temporary, opt-in, unproven on this version. Pinned to Claude Code 2.1.277.

Around 2026-09-14 Claude Code stopped granting `persistent` to the Monitor tool. Every cbus
listener now expires at its `timeout_ms`, capped at 30 minutes, and each expiry costs two API
requests to re-arm while re-reading the full cached context. Measured machine-wide on
2026-09-17: 106 expiries, 216 requests, about 59M cache-read tokens.

This document describes a stopgap that restores `persistent` for sessions you opt in, and
`scripts/monitor-stopgap.sh` performs the one step that is fiddly. Read the limits before
using it. The stopgap is not the plan of record: cross-session messaging over each session's
Unix socket inbox is the durable path, it needs none of the gates below, and it makes this
document obsolete once cbus delivers through it.

## What actually changed

The behavior is a feature flag, not a code removal. In 2.1.277 the Monitor module reads:

    function Iq(){ return ql("tengu_breezy_crescent", !0) }
    function TVr(e,t){
      if(t) return { timeout_ms: Math.min(e.timeout_ms, ike()), persistent:!1 };
      let n = e.persistent === !0;
      if(!a.CLAUDE_CODE_REMOTE) return { timeout_ms: e.timeout_ms, persistent: n };
      return { timeout_ms: n ? o : Math.min(e.timeout_ms, o), persistent:!1 };
    }

with `o` = 1800000 and `ike()` returning 600000 under `-p`. Flagged on, the schema is bounded
and `persistent` is stripped. Flagged off, the old schema is intact. `CLAUDE_CODE_REMOTE`
forces the bounded form either way, so a remote session cannot opt out.

## Which reads the seed reaches, and which it does not

This is the part that has been argued in both directions, so it is worth stating precisely.
2.1.277 has three feature-value accessors:

    async function uRn(e,n){ return Ze().getFeatureValueBlocking(e,n) }
    function Rd(e,n){ return Ze().getFeatureValueWithSource(e,n) }
    function P(e,n){ return Rd(e,n).value }
    function ql(e,n){ let r = ps().pinnedFeatureValues ??= new Map;
                      if(!r.has(e)) r.set(e, P(e,n)); return r.get(e) }
    async function xd(e){ return Ze().checkGateCachedOrBlocking(e) }

The binary names them `getFeatureValue_SESSION_PINNED`, `getFeatureValue_CACHED_MAY_BE_STALE`,
`getFeatureValue_CACHED_WITH_REFRESH`, `checkGate_CACHED_OR_BLOCKING` and
`getDynamicConfig_BLOCKS_ON_INIT`. The cached side resolves through:

    getAllFeatures(){ if(this.remoteEvalFeatureValues.size > 0) return ...;
                      return this.deps.readGlobalConfig().cachedGrowthBookFeatures ?? {} }

So a seeded `cachedGrowthBookFeatures` controls the cached and session-pinned reads, and does
not control the blocking ones, which fall back to their defaults. Monitor reads through `ql`,
the session-pinned path, which is why seeding works for it at all. Two claims follow, and both
are true only when scoped this way:

- "The seed restores `persistent`": yes, because of the read path Monitor uses.
- "The seed freezes every flag": no. Blocking reads, blocking gate checks and dynamic configs
  that block on init do not consult the seed. With telemetry off, evaluation is disabled, so
  those reads return their built-in defaults rather than a fetched value.

Do not generalize either claim to a flag you have not traced to its accessor.

## What freezing does to everything else

A seeded snapshot pins every cached and pinned flag at the values in it, for as long as you
run with that config dir. Today's snapshot on this machine carries 664 flags. A feature whose
rollout flips after you seed, and whose gate is read through the cached or pinned path, will
not reach you until you re-seed.

AGENTS.md is the case to be careful about, in both directions. A flag named
`tengu_agents_md_mod` is present in the snapshot, but presence of a flag name is not proof
that a given behavior is gated on it, and this helper's own rule is not to generalize from an
untraced flag. Separately, the official memory documentation says a telemetry-off session may
disable the instruction-file loader, which would affect AGENTS.md whatever the flag does. Both
point the same way: do not rely on AGENTS.md discovery inside a seeded, telemetry-off session.
Put the project instructions where neither question matters, with a `CLAUDE.md` that carries
`@AGENTS.md`, and keep AGENTS.md as the included file rather than the discovered one.

Telemetry-off is load-bearing for the mechanism and has its own consequence. The disk cache is
only read while telemetry is disabled:

    function vSe(){ return a.CLAUDE_CODE_GB_DISK_CACHE_WHEN_TELEMETRY_OFF
                      && !a.DISABLE_GROWTHBOOK && nM() && Un() }
    function nM(){ return b() !== "default" }

Prefer `DISABLE_TELEMETRY=1`, which the docs describe as disabling feature-flag evaluation and
which leaves the auto-updater, error reports and the other first-party surfaces alone. Do not
use `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`: it silently bundles `DISABLE_AUTOUPDATER`, so
security updates stop. Never set `DISABLE_GROWTHBOOK`: it kills the disk read the stopgap
depends on, and leaves you bounded anyway.

## Gates

- First-party authentication only. A gateway, Bedrock, Vertex or custom-OAuth session falls
  back to the flag default, so the `beta` profile is out of scope for this stopgap.
- A dedicated `CLAUDE_CONFIG_DIR`. A telemetry-on session sharing the dir overwrites the seed.
- Flags pin per session on first read, so changing a seed needs a new session.
- Re-verify after every CLI upgrade. This is a rollout flag and the binary auto-updates.
- The dedicated config dir starts signed out. Sign in there. Do not copy credentials into it,
  and note that the helper below refuses to copy anything but the flag snapshot.

## Using the helper

    scripts/monitor-stopgap.sh seed   ~/.claude-monitor-stopgap
    scripts/monitor-stopgap.sh status ~/.claude-monitor-stopgap
    scripts/monitor-stopgap.sh revert ~/.claude-monitor-stopgap

`seed` writes a NEW dedicated dir only. A sibling of your live config dir, such as the
`~/.claude-monitor-stopgap` above, is the intended layout: home is protected as a path, while
the live config dir is protected as a whole subtree. Every path component is canonicalized, so
a symlinked directory or a planted `.claude.json` symlink cannot redirect the write. A
directory it creates is mode 0700; a directory that already exists must be yours and must not
be group- or world-accessible.

It copies only `cachedGrowthBookFeatures` and `cachedGrowthBookFeaturesAt` out of your source
config, forces `tengu_breezy_crescent` to false, and records a manifest with the file's
sha256. Both files are staged mode 600 through `O_EXCL|O_NOFOLLOW` and published with
`link()`, which fails rather than overwrites if the name appeared after the initial check, so
the no-clobber promise does not depend on a check-then-write window. The manifest publishes
first, so an interrupted seed leaves a manifest with no config, which `revert` handles, rather
than a config nothing can prove it owns.

`revert` removes only what that manifest proves this helper wrote. It does not hash the file
and then delete it, because a sign-in can replace the file between those two steps and the
delete would take the new one. Instead it claims the file with a rename, which can only ever
take one file, and decides afterwards: if what it holds hashes to the manifest, it removes it;
if not, it links the file back into place untouched and removes nothing. If a new config
appeared while it held the file, it keeps its copy alongside, names the path, and deletes
nothing. `status` reports a changed file as MODIFIED.

Stop every session using the dir before you revert. The helper is built not to lose a config
if you forget, but a session writing while you revert is a race you do not need to run.
Re-seeding a used dir is out of scope on purpose, and an explicit snapshot refresh would be
the safe way to add it later.

Then run the sessions you want the stopgap in with:

    CLAUDE_CONFIG_DIR=~/.claude-monitor-stopgap DISABLE_TELEMETRY=1 \
      CLAUDE_CODE_GB_DISK_CACHE_WHEN_TELEMETRY_OFF=1 claude

`scripts/monitor-stopgap-test.sh` runs twenty-seven checks, most of them negative: allowlist
honored, no credential copied, flag forced false, snapshot otherwise intact, file mode 600,
new directory mode 0700, manifest written, no staging files left, and the documented
`~/.claude-monitor-stopgap` path accepted; with refusals for a second seed, home itself, the
live config dir, a target symlinked to the live dir, a leaf `.claude.json` symlink, a
pre-existing manifest, a pre-existing config, a group-accessible target directory, a revert
with no manifest, and a revert after a simulated sign-in. Four assert the damage rather than
the refusal: the live fixture config is byte-unchanged through every negative, a pre-existing
config is byte-unchanged, no config is published when the manifest collides, and an unowned
config survives a revert attempt. Temp tree only.

Two of the checks are about revert under concurrency: a post-login config comes back
byte-identical with no archive left behind, and twenty rounds of revert racing an atomic
config replacement lose nothing.

Two honest limits. The publish tests cover a name that already exists, not one that appears in
the window between the check and the publish; that window is closed by using `link()` instead
of a replace, which is an argument from the primitive rather than from an exercised test. And
the race loop is bounded: it can find a loss, it cannot prove there is none. Neither is a
claim of immunity under an adversarial writer.

## Acceptance: mechanism measured, field proof still open

Superseded: an earlier revision of this document said nothing here had been measured on
2.1.277 and that the source only predicted `persistent`. The mechanism has since been measured
on the running binary.

A two-arm runtime pair was run against the installed 2.1.277 (sha256
`73d6a2a5...9914b9c`) from one script (sha256 `741d0e8c...78d03e`), in an ordinary interactive
PTY at default permission mode, with no human input after the initial prompt. Both arms
requested the same Monitor input, `persistent: true` with `timeout_ms: 2000`, and both held
`tengu_amber_sentinel` true. The only variable was `tengu_breezy_crescent`:

- Flag true: `Monitor started (task bu151whxw, expires in 2s unless the source ends first...)`.
  The waiter died at the deadline, the expiry notice arrived, and an external signal twelve
  seconds later produced no further request. 21 checks, all true.
- Flag false: `Monitor started (task b1jqqgx3e, persistent, runs until TaskStop or session
  end)`. The same process was still alive across a twelve-second idle with no main-loop
  requests in between, the late external signal did wake it, and TaskStop then stopped it.
  20 checks, all true.

Artifacts: `/tmp/cbus-monitor-runtime-pair-20260918.json`, with per-arm results at
`/tmp/cbus-cc-wake-zvj2v92m/result.json` (bounded) and `/tmp/cbus-cc-wake-d3mz3foh/result.json`
(persistent). Canary source under `/tmp/cbus-cc-capability-20260918`.

What that establishes is the mechanism: on this binary, that one flag decides whether the
Monitor schema keeps `persistent` and whether a monitor outlives its deadline. What it does
not establish, in the artifacts' own words, is real-account field proof. The run used a
synthetic two-flag snapshot and a local fake provider on the firstParty code route with a fake
API key, so it says nothing about seeding a real signed-in config dir. It requested a
two-second deadline, so it does not measure the default five-minute or thirty-minute
deadlines. It used environment traffic controls rather than an OS-enforced network sandbox,
and it did not exercise cbus at all.

So the remaining gate is narrower than it was, and it is still a gate: a first-party session on
a seeded config dir, doing real work, reporting a monitor that runs until TaskStop. Do not
describe the stopgap as proven in the field until someone runs that.
