#!/usr/bin/env bash
# opt-in helper for docs/claude-monitor-stopgap.md: seed a DEDICATED Claude Code config dir
# with a GrowthBook snapshot whose tengu_breezy_crescent is false, so Monitor keeps
# `persistent`. Reversible, never touches the live config, never copies credentials.
set -euo pipefail

FLAG=tengu_breezy_crescent
# the only keys ever copied out of the source config. everything else in that file, including
# oauth account data, stays where it is.
ALLOW='cachedGrowthBookFeatures cachedGrowthBookFeaturesAt'

die() { printf 'monitor-stopgap: %s\n' "$*" >&2; exit 1; }

usage() {
  cat >&2 <<'USAGE'
usage: monitor-stopgap.sh seed|status|revert <config-dir> [--from <source .claude.json>]

  seed    write <config-dir>/.claude.json carrying only the flag snapshot, flag forced false
  status  report the flag value and snapshot size in <config-dir>
  revert  restore the backup this helper made, or remove the file it wrote

<config-dir> must NOT be your live config dir. Use it as CLAUDE_CONFIG_DIR for the sessions
you want the stopgap in, and sign in there; this helper will not copy your credentials.
USAGE
  exit 2
}

# refuse the live config dir in every spelling we can resolve, since seeding it would freeze
# flags for every ordinary session on the machine.
guard_target() {
  local dir="$1" resolved live
  resolved=$(cd "$(dirname "$dir")" 2>/dev/null && printf '%s/%s' "$(pwd -P)" "$(basename "$dir")") || resolved="$dir"
  for live in "${CLAUDE_CONFIG_DIR:-}" "$HOME/.claude" "$HOME"; do
    [ -n "$live" ] || continue
    [ "$resolved" = "$live" ] && die "refusing to seed the live config dir ($resolved)"
  done
  case "$resolved" in "$HOME/.claude/"*) die "refusing to seed inside the live config dir";; esac
  printf '%s' "$resolved"
}

cmd=${1:-}; target=${2:-}; shift 2 2>/dev/null || usage
[ -n "$cmd" ] && [ -n "$target" ] || usage
source_cfg="$HOME/.claude.json"
while [ $# -gt 0 ]; do
  case "$1" in
    --from) source_cfg=${2:-}; shift 2 || usage ;;
    *) usage ;;
  esac
done

target=$(guard_target "$target")
out="$target/.claude.json"

case "$cmd" in
  seed)
    [ -r "$source_cfg" ] || die "cannot read source config: $source_cfg"
    mkdir -p "$target"
    [ -f "$out" ] && cp -p "$out" "$out.stopgap-backup"
    ALLOW="$ALLOW" FLAG="$FLAG" python3 - "$source_cfg" "$out" <<'PY'
import json, os, sys
src, out = sys.argv[1], sys.argv[2]
allow = os.environ["ALLOW"].split()
flag = os.environ["FLAG"]
data = json.load(open(src))
seeded = {k: data[k] for k in allow if k in data}
feats = seeded.get("cachedGrowthBookFeatures")
if not isinstance(feats, dict) or not feats:
    sys.exit("monitor-stopgap: source has no cachedGrowthBookFeatures to snapshot")
feats[flag] = False
json.dump(seeded, open(out, "w"), indent=2)
print("monitor-stopgap: wrote %s (%d flags, %s=false, keys copied: %s)"
      % (out, len(feats), flag, ", ".join(sorted(seeded))))
PY
    chmod 600 "$out"
    printf 'monitor-stopgap: now run sessions with CLAUDE_CONFIG_DIR=%s and sign in there.\n' "$target"
    ;;
  status)
    [ -f "$out" ] || die "no seeded config at $out"
    FLAG="$FLAG" python3 - "$out" <<'PY'
import json, os, sys
flag = os.environ["FLAG"]
data = json.load(open(sys.argv[1]))
feats = data.get("cachedGrowthBookFeatures") or {}
print("monitor-stopgap: %s = %r across %d flags; top-level keys: %s"
      % (flag, feats.get(flag, "(absent)"), len(feats), ", ".join(sorted(data))))
PY
    ;;
  revert)
    if [ -f "$out.stopgap-backup" ]; then
      mv "$out.stopgap-backup" "$out"
      printf 'monitor-stopgap: restored %s from backup\n' "$out"
    elif [ -f "$out" ]; then
      rm "$out"
      printf 'monitor-stopgap: removed %s\n' "$out"
    else
      die "nothing to revert at $out"
    fi
    ;;
  *) usage ;;
esac
