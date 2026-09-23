#!/usr/bin/env bash
# opt-in helper for docs/history/legacy/claude-monitor-stopgap.md: seed a NEW dedicated Claude Code config
# dir with a GrowthBook snapshot whose tengu_breezy_crescent is false, so Monitor keeps
# `persistent`. Never writes into an existing config, never copies credentials, and reverts
# only what it can prove it wrote.
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: monitor-stopgap.sh seed|status|revert <config-dir> [--from <source .claude.json>]

  seed    create <config-dir> (must not already hold a config) and write .claude.json
          carrying only the flag snapshot, flag forced false, plus an ownership manifest
  status  report the flag value and whether the file still matches the manifest
  revert  remove the seeded file, only while it still matches the manifest

<config-dir> must not be your live config dir. Use it as CLAUDE_CONFIG_DIR for the sessions
you want the stopgap in, and sign in there. Signing in rewrites .claude.json, after which
revert restores it and removes nothing: that is deliberate, so a login is never deleted by
this helper. Stop every session using the dir before you revert.
USAGE
  exit 2
}

cmd=${1:-}; target=${2:-}; [ -n "$cmd" ] && [ -n "$target" ] || usage
shift 2
source_cfg="$HOME/.claude.json"
while [ $# -gt 0 ]; do
  case "$1" in
    --from) source_cfg=${2:-}; [ -n "$source_cfg" ] || usage; shift 2 ;;
    *) usage ;;
  esac
done
case "$cmd" in seed|status|revert) ;; *) usage ;; esac

MS_SOURCE="$source_cfg" MS_LIVE="${CLAUDE_CONFIG_DIR:-$HOME/.claude}" MS_HOME="$HOME" \
python3 - "$cmd" "$target" <<'PY'
import hashlib, json, os, sys

FLAG = "tengu_breezy_crescent"
ALLOW = ("cachedGrowthBookFeatures", "cachedGrowthBookFeaturesAt")
MANIFEST = ".monitor-stopgap.json"
cmd, target = sys.argv[1], sys.argv[2]

def die(msg):
    sys.exit("monitor-stopgap: " + msg)

def real(p):
    # full canonicalization, every component, so a symlinked target or leaf cannot escape
    # the checks below the way a dirname-only resolve could.
    return os.path.realpath(os.path.abspath(os.path.expanduser(p)))

target_real = real(target)
# home is protected as a path, not as a subtree: a dedicated sibling like
# ~/.claude-monitor-stopgap is the documented layout and has to be allowed.
if target_real == real(os.environ["MS_HOME"]):
    die("refusing to operate on your home directory itself")
live_real = real(os.environ["MS_LIVE"])
if target_real == live_real or target_real.startswith(live_real + os.sep):
    die("refusing to operate on the live config dir (%s resolves inside %s)" % (target, live_real))

cfg = os.path.join(target_real, ".claude.json")
man = os.path.join(target_real, MANIFEST)

def digest(path):
    with open(path, "rb") as fh:
        return hashlib.sha256(fh.read()).hexdigest()

def read_manifest():
    if not os.path.isfile(man) or os.path.islink(man):
        die("no ownership manifest at %s; this helper will only revert what it wrote" % man)
    with open(man) as fh:
        return json.load(fh)

if cmd == "seed":
    if os.path.lexists(cfg):
        die("%s already exists; seed only a new dedicated dir, and use revert first if this "
            "helper wrote it" % cfg)
    if os.path.lexists(target_real) and not os.path.isdir(target_real):
        die("%s exists and is not a directory" % target)
    src = os.environ["MS_SOURCE"]
    if os.path.islink(src):
        die("source config is a symlink; pass the real file")
    try:
        data = json.load(open(src))
    except OSError as exc:
        die("cannot read source config %s: %s" % (src, exc))
    seeded = {k: data[k] for k in ALLOW if k in data}
    feats = seeded.get("cachedGrowthBookFeatures")
    if not isinstance(feats, dict) or not feats:
        die("source has no cachedGrowthBookFeatures to snapshot")
    feats[FLAG] = False
    if os.path.lexists(man):
        die("%s already exists; another seed owns this directory" % man)
    if os.path.isdir(target_real):
        st = os.stat(target_real)
        if st.st_uid != os.geteuid():
            die("%s is owned by uid %d, not you" % (target, st.st_uid))
        if st.st_mode & 0o077:
            die("%s is group- or world-accessible (mode %o); make it private first"
                % (target, st.st_mode & 0o777))
    else:
        os.makedirs(target_real, mode=0o700)
    body = json.dumps(seeded, indent=2).encode()
    sha = hashlib.sha256(body).hexdigest()
    manifest = json.dumps({"wrote": ".claude.json", "sha256": sha, "flag": FLAG,
                           "flagCount": len(feats)}, indent=2).encode()

    def stage(path, payload):
        # O_EXCL|O_NOFOLLOW so a planted symlink is refused rather than followed
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        try:
            os.write(fd, payload)
        finally:
            os.close(fd)

    def publish(tmp, final):
        # link() fails with EEXIST when the final name appeared after our checks, so the
        # no-clobber promise survives a race that os.replace would have lost.
        try:
            os.link(tmp, final)
        except FileExistsError:
            die("%s appeared while seeding; refusing to overwrite it" % final)
        finally:
            os.unlink(tmp)

    stage(man + ".tmp", manifest)
    stage(cfg + ".tmp", body)
    # manifest first: a half-finished seed then leaves a manifest with no config, which revert
    # already handles, rather than a config nothing can prove it owns.
    publish(man + ".tmp", man)
    try:
        publish(cfg + ".tmp", cfg)
    except SystemExit:
        os.unlink(man)
        raise
    print("monitor-stopgap: wrote %s (%d flags, %s=false, keys copied: %s)"
          % (cfg, len(feats), FLAG, ", ".join(sorted(seeded))))
    print("monitor-stopgap: run sessions with CLAUDE_CONFIG_DIR=%s and sign in there." % target_real)

elif cmd == "status":
    if not os.path.isfile(cfg) or os.path.islink(cfg):
        die("no seeded config at %s" % cfg)
    data = json.load(open(cfg))
    feats = data.get("cachedGrowthBookFeatures") or {}
    state = "unknown (no manifest)"
    if os.path.isfile(man) and not os.path.islink(man):
        state = "unmodified" if digest(cfg) == json.load(open(man)).get("sha256") else \
                "MODIFIED since seeding (a login does this; revert will refuse)"
    print("monitor-stopgap: %s = %r across %d flags; top-level keys: %s; %s"
          % (FLAG, feats.get(FLAG, "(absent)"), len(feats), ", ".join(sorted(data)), state))

elif cmd == "revert":
    m = read_manifest()
    if os.path.islink(cfg):
        die("%s is a symlink; refusing to follow it" % cfg)
    if not os.path.lexists(cfg):
        os.remove(man)
        print("monitor-stopgap: config already gone; removed the manifest")
        sys.exit(0)
    # claim the file by rename before inspecting it. hashing and then unlinking would delete a
    # config that a sign-in replaced in between; a rename can only ever take one file, and we
    # decide what to do with it once it is ours and nothing else can swap it.
    archive = "%s.reverting-%d-%s" % (cfg, os.getpid(), os.urandom(4).hex())
    if os.path.lexists(archive):
        die("scratch name %s already exists" % archive)
    os.rename(cfg, archive)
    if digest(archive) == m.get("sha256"):
        os.remove(archive)
        os.remove(man)
        print("monitor-stopgap: removed %s and its manifest" % cfg)
        sys.exit(0)
    # not what we wrote, so it is someone's real config, most likely a sign-in. put it back
    # without clobbering anything that appeared in the meantime.
    try:
        os.link(archive, cfg)
    except FileExistsError:
        die("%s changed since seeding and a new file appeared while reverting; nothing was "
            "deleted and your copy is preserved at %s" % (cfg, archive))
    os.unlink(archive)
    die("%s changed since seeding, most likely a sign-in. Restored it untouched and removed "
        "nothing; delete it yourself if you are sure. Stop sessions using this config dir "
        "before reverting." % cfg)
PY
