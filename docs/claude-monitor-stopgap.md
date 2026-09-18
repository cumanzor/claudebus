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

`seed` writes a NEW dedicated dir only. It refuses a directory that already holds a
`.claude.json`, so it can never overwrite a config you care about, and it refuses a target
that resolves inside your live config dir or home, canonicalizing every path component so a
symlinked directory or a planted `.claude.json` symlink cannot redirect the write. It copies
only `cachedGrowthBookFeatures` and `cachedGrowthBookFeaturesAt` out of your source config,
forces `tengu_breezy_crescent` to false, writes mode 600 through `O_EXCL|O_NOFOLLOW`, and
records a manifest with the file's sha256.

`revert` removes only what that manifest proves this helper wrote, and only while the file
still hashes to the recorded value. Signing in rewrites `.claude.json`, so after a login
`revert` refuses and says so; `status` reports the same as MODIFIED. That is deliberate: this
helper will not delete an account you signed into. Re-seeding a used dir is out of scope on
purpose, and an explicit snapshot refresh would be the safe way to add it later.

Then run the sessions you want the stopgap in with:

    CLAUDE_CONFIG_DIR=~/.claude-monitor-stopgap DISABLE_TELEMETRY=1 \
      CLAUDE_CODE_GB_DISK_CACHE_WHEN_TELEMETRY_OFF=1 claude

`scripts/monitor-stopgap-test.sh` runs fifteen checks, positive and negative: allowlist
honored, no credential copied, flag forced false, snapshot otherwise intact, mode 600,
manifest written; and refusals for a second seed, the live config dir, a target symlinked to
the live dir, a leaf `.claude.json` symlink, a revert with no manifest, and a revert after a
simulated sign-in. It asserts the live fixture config is byte-unchanged through every negative
and that an unowned config survives a revert attempt. Temp tree only.

## Acceptance, still open

Nothing here has been measured on 2.1.277. The source reading predicts that an unflagged
session keeps `persistent`; the running behavior is unverified.
The acceptance gate is a live proof on this version: one first-party session on a seeded
config dir whose Monitor result says it runs until TaskStop, against a control session on an
unseeded dir whose result says it expires. Until someone runs that pair and records it, treat
this document as a description of a mechanism, not a working remedy. Do not claim the stopgap
works from the source reading alone.
