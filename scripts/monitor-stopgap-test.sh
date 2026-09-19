#!/usr/bin/env bash
# fixture checks for monitor-stopgap.sh, positive and negative. temp tree only: no live
# config, no network, no paid calls.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd -P)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fails=0
ok()   { printf 'ok: %s\n' "$1"; }
bad()  { printf 'FAIL: %s\n' "$1" >&2; fails=$((fails+1)); }
run()  { HOME="$tmp/home" CLAUDE_CONFIG_DIR="$tmp/home/.claude" bash "$here/monitor-stopgap.sh" "$@"; }
# a negative check passes only when the helper exits nonzero AND leaves the bait untouched
refuses() { local what=$1; shift; if run "$@" >/dev/null 2>&1; then bad "$what was allowed"; else ok "$what refused"; fi; }

mkdir -p "$tmp/home/.claude"
cat > "$tmp/source.json" <<'JSON'
{
  "oauthAccount": {"accessToken": "SECRET-MUST-NOT-BE-COPIED"},
  "primaryApiKey": "SECRET-TOO",
  "cachedGrowthBookFeaturesAt": 1789676079927,
  "cachedGrowthBookFeatures": {"tengu_breezy_crescent": true, "tengu_agents_md_mod": false, "other": true}
}
JSON
printf '{"live":"do not touch"}\n' > "$tmp/home/.claude/.claude.json"
live_before=$(cat "$tmp/home/.claude/.claude.json")

run seed "$tmp/cfg" --from "$tmp/source.json" >/dev/null 2>&1 || bad "seed exited nonzero"
out="$tmp/cfg/.claude.json"
[ -f "$out" ] && ok "seed wrote a config" || bad "seed wrote no config"
grep -q SECRET "$out" && bad "seeded config carries a credential" || ok "no credential copied"
python3 - "$out" "$tmp/cfg/.monitor-stopgap.json" <<'PY' && ok "allowlist, flag, manifest" || bad "content assertions"
import json, sys
d = json.load(open(sys.argv[1])); m = json.load(open(sys.argv[2]))
assert set(d) == {"cachedGrowthBookFeatures", "cachedGrowthBookFeaturesAt"}, sorted(d)
f = d["cachedGrowthBookFeatures"]
assert f["tengu_breezy_crescent"] is False and f["tengu_agents_md_mod"] is False and len(f) == 3
assert m["sha256"] and m["flagCount"] == 3
PY
mode=$(stat -f '%Lp' "$out" 2>/dev/null || stat -c '%a' "$out")
[ "$mode" = "600" ] && ok "mode 600" || bad "seeded config is mode $mode"

refuses "second seed over an existing config" seed "$tmp/cfg" --from "$tmp/source.json"
refuses "seeding the live config dir" seed "$tmp/home/.claude" --from "$tmp/source.json"
ln -s "$tmp/home/.claude" "$tmp/symlinked-dir"
refuses "a target dir symlinked to the live dir" seed "$tmp/symlinked-dir" --from "$tmp/source.json"
mkdir -p "$tmp/leaf"; ln -s "$tmp/home/.claude/.claude.json" "$tmp/leaf/.claude.json"
refuses "a leaf .claude.json symlink" seed "$tmp/leaf" --from "$tmp/source.json"
[ "$(cat "$tmp/home/.claude/.claude.json")" = "$live_before" ] \
  && ok "live config byte-unchanged through every negative" || bad "live config was modified"

mkdir -p "$tmp/unowned"; printf '{"not":"ours"}\n' > "$tmp/unowned/.claude.json"
refuses "revert without a manifest" revert "$tmp/unowned"
[ -f "$tmp/unowned/.claude.json" ] && ok "unowned config survived revert" || bad "revert deleted an unowned config"

cp "$out" "$tmp/seeded-copy"; printf '{"cachedGrowthBookFeatures":{},"oauthAccount":{"x":1}}\n' > "$out"
refuses "revert after a sign-in rewrote the config" revert "$tmp/cfg"
[ -s "$out" ] && ok "post-login config survived revert" || bad "revert deleted a post-login config"
run status "$tmp/cfg" 2>/dev/null | grep -q MODIFIED && ok "status reports MODIFIED" || bad "status missed the change"

[ "$(cat "$out")" = '{"cachedGrowthBookFeatures":{},"oauthAccount":{"x":1}}' ] \
  && ok "post-login config restored byte-identical" || bad "post-login config was altered"
ls "$tmp/cfg"/.claude.json.reverting-* >/dev/null 2>&1 && bad "revert left an archive behind" \
  || ok "no archive left after a restore"

# adversarial: replace the config by atomic rename while revert runs. a completed
# replacement whose payload is then findable nowhere is a loss, whatever is left at the
# config path: a stale or corrupt file there is not evidence the write survived.
lost=0
for i in $(seq 1 20); do
  race="$tmp/race$i"; run seed "$race" --from "$tmp/source.json" >/dev/null 2>&1
  printf '{"signed":"in-%d"}\n' "$i" > "$tmp/newcfg"
  ( for _ in 1 2 3 4 5 6 7 8; do
      cp "$tmp/newcfg" "$tmp/stage$i" 2>/dev/null || continue
      mv -f "$tmp/stage$i" "$race/.claude.json" 2>/dev/null && : > "$tmp/wrote$i"
    done ) &
  racer=$!
  run revert "$race" >/dev/null 2>&1
  wait $racer 2>/dev/null
  # only rounds where a replacement actually completed can show a loss
  if [ -e "$tmp/wrote$i" ] && ! grep -qs "in-$i" "$race/.claude.json" "$race"/.claude.json.reverting-* 2>/dev/null; then
    lost=$((lost+1))
  fi
done
[ "$lost" -eq 0 ] && ok "20 revert-vs-signin races lost no completed write" \
  || bad "$lost race(s) lost a completed write"

cp "$tmp/seeded-copy" "$out"
run revert "$tmp/cfg" >/dev/null 2>&1 || bad "revert of an unmodified seed exited nonzero"
[ -e "$out" ] || [ -e "$tmp/cfg/.monitor-stopgap.json" ] && bad "revert left files behind" || ok "revert removed both files"

# the documented layout is a dedicated sibling inside HOME, so HOME must be protected as a
# path and not as a subtree. the first cut refused this and the fixtures did not notice.
doc="$tmp/home/.claude-monitor-stopgap"
run seed "$doc" --from "$tmp/source.json" >/dev/null 2>&1 \
  && ok "documented ~/.claude-monitor-stopgap path allowed" || bad "documented path under HOME refused"
dmode=$(stat -f '%Lp' "$doc" 2>/dev/null || stat -c '%a' "$doc")
[ "$dmode" = "700" ] && ok "new dedicated dir is 0700" || bad "new dir is mode $dmode"
ls "$doc"/*.tmp >/dev/null 2>&1 && bad "staging files left behind" || ok "no staging files left"
refuses "seeding HOME itself" seed "$tmp/home" --from "$tmp/source.json"

# manifest collision has to be caught BEFORE any config is published, or a seed leaves a
# config that nothing can prove it owns
mkdir -p "$tmp/mcol"; : > "$tmp/mcol/.monitor-stopgap.json"
refuses "a pre-existing manifest" seed "$tmp/mcol" --from "$tmp/source.json"
[ -e "$tmp/mcol/.claude.json" ] && bad "published a config despite the manifest collision" \
  || ok "no config published on manifest collision"

mkdir -p "$tmp/pre"; printf '{"pre":"existing"}\n' > "$tmp/pre/.claude.json"
pre_before=$(cat "$tmp/pre/.claude.json")
refuses "a pre-existing final config" seed "$tmp/pre" --from "$tmp/source.json"
[ "$(cat "$tmp/pre/.claude.json")" = "$pre_before" ] && ok "pre-existing config byte-unchanged" \
  || bad "pre-existing config was modified"

mkdir -p "$tmp/loose"; chmod 755 "$tmp/loose"
refuses "a group- or world-accessible target dir" seed "$tmp/loose" --from "$tmp/source.json"

[ "$fails" -eq 0 ] && printf '\nall fixture checks passed\n' || { printf '\n%d check(s) failed\n' "$fails"; exit 1; }
