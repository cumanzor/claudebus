#!/usr/bin/env bash
# fixture checks for monitor-stopgap.sh. touches only a temp tree: no live config, no network.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd -P)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

cat > "$tmp/source.json" <<'JSON'
{
  "oauthAccount": {"accessToken": "SECRET-MUST-NOT-BE-COPIED"},
  "primaryApiKey": "SECRET-TOO",
  "cachedGrowthBookFeaturesAt": 1789676079927,
  "cachedGrowthBookFeatures": {"tengu_breezy_crescent": true, "tengu_agents_md_mod": false, "other": true}
}
JSON

HOME="$tmp/home" CLAUDE_CONFIG_DIR="" bash "$here/monitor-stopgap.sh" \
  seed "$tmp/cfg" --from "$tmp/source.json" >"$tmp/seed.log" || fail "seed exited nonzero"
out="$tmp/cfg/.claude.json"
[ -f "$out" ] || fail "seed wrote no config"

grep -q SECRET "$out" && fail "seeded config carries a credential from the source"
python3 - "$out" <<'PY' || exit 1
import json, sys
d = json.load(open(sys.argv[1]))
assert set(d) == {"cachedGrowthBookFeatures", "cachedGrowthBookFeaturesAt"}, "unexpected keys: %s" % sorted(d)
f = d["cachedGrowthBookFeatures"]
assert f["tengu_breezy_crescent"] is False, "flag not forced false"
assert len(f) == 3, "snapshot lost flags: %d" % len(f)
assert f["tengu_agents_md_mod"] is False, "unrelated flag mutated"
print("ok: allowlist honored, flag forced false, snapshot intact")
PY

mode=$(stat -f '%Lp' "$out" 2>/dev/null || stat -c '%a' "$out")
[ "$mode" = "600" ] || fail "seeded config is mode $mode, want 600"

HOME="$tmp/home" bash "$here/monitor-stopgap.sh" status "$tmp/cfg" | grep -q 'False' \
  || fail "status does not report the flag as false"

# the guard must refuse the live config dir even when asked directly
if HOME="$tmp/home" CLAUDE_CONFIG_DIR="$tmp/home/.claude" \
     bash "$here/monitor-stopgap.sh" seed "$tmp/home/.claude" --from "$tmp/source.json" 2>/dev/null; then
  fail "guard allowed seeding the live config dir"
fi

HOME="$tmp/home" bash "$here/monitor-stopgap.sh" revert "$tmp/cfg" >/dev/null || fail "revert exited nonzero"
[ -f "$out" ] && fail "revert left the seeded config behind"

printf 'all fixture checks passed\n'
