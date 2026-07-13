#!/usr/bin/env bash
# P2.6 Class A/B differential sweep: every verb, bash cbus vs cbus-go, hermetic
# CBUS_DIR, outputs normalized for volatiles (ts/pid/session/dir) then diffed.
set -uo pipefail
REPO="${REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"; BASH_CBUS="$REPO/bin/cbus"
ROOT=$(mktemp -d); GO=$ROOT/cbus-go
( cd "$REPO" && go build -o "$GO" ./cmd/cbus ) || exit 1
pass=0; fail=0

norm() { # normalize volatile fields to placeholders
  sed -E \
    -e "s#$ROOT/bus[^ ]*#<DIR>#g" \
    -e 's/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z/<TS>/g' \
    -e 's/pid=[0-9?]+/pid=<PID>/g' \
    -e 's/session [A-Za-z0-9-]+/session <SID>/g' \
    -e 's/\(session [A-Za-z0-9-]+\)/(session <SID>)/g'
}

# run one verb differentially: $1=label; remaining after -- = argv. Reads a setup
# function name in SETUP (run under a fresh hermetic dir for EACH client).
cmp_verb() {
  local label="$1"; shift
  local setup="$1"; shift
  local bo go brc grc
  export CBUS_DIR="$ROOT/bus"
  rm -rf "$CBUS_DIR"; mkdir -p "$CBUS_DIR"; "$setup" "$BASH_CBUS"
  bo=$(CLAUDE_CODE_SESSION_ID=SID "$BASH_CBUS" "$@" 2>&1); brc=$?
  rm -rf "$CBUS_DIR"; mkdir -p "$CBUS_DIR"; "$setup" "$GO"
  go=$(CLAUDE_CODE_SESSION_ID=SID "$GO" "$@" 2>&1); grc=$?
  local bn gn; bn=$(printf '%s' "$bo" | norm); gn=$(printf '%s' "$go" | norm)
  if [ "$bn" = "$gn" ] && [ "$brc" = "$grc" ]; then
    printf 'PASS  %-28s (rc=%s)\n' "$label" "$brc"; pass=$((pass+1))
  else
    printf 'FAIL  %-28s bash-rc=%s go-rc=%s\n' "$label" "$brc" "$grc"; fail=$((fail+1))
    diff <(printf '%s\n' "$bn") <(printf '%s\n' "$gn") | head -12
  fi
}

# ---- setup fixtures ----
noop() { :; }
joined() { CLAUDE_CODE_SESSION_ID=SID "$1" join dev >/dev/null 2>&1; }
joined_peer() { # a live-ish peer 'dev/mbp' owned by another session + a witness
  CLAUDE_CODE_SESSION_ID=OTHER "$1" join dev mbp >/dev/null 2>&1
  CLAUDE_CODE_SESSION_ID=SID   "$1" join dev me  >/dev/null 2>&1
}
two_peers() {
  CLAUDE_CODE_SESSION_ID=OTHER "$1" join dev mbp >/dev/null 2>&1
  CLAUDE_CODE_SESSION_ID=OTHER "$1" join dev nuc >/dev/null 2>&1
}

echo "=== Class A (read-only) ==="
# help is a RULED delta: cbus-go help must equal bash help EXCEPT the obsolete
# CC_BRANCH env line (branch is native). Assert the diff is EXACTLY that one line.
export CBUS_DIR="$ROOT/bus"; rm -rf "$CBUS_DIR"; mkdir -p "$CBUS_DIR"
hd=$(diff <("$BASH_CBUS" --help) <("$GO" --help) || true)
if [ "$(printf '%s\n' "$hd" | grep -c '^[<>]')" = 1 ] && printf '%s' "$hd" | grep -q 'CC_BRANCH'; then
  printf 'PASS  %-28s (ruled delta: only the CC_BRANCH env line differs)\n' "help"; pass=$((pass+1))
else
  printf 'FAIL  %-28s (help differs beyond the ruled CC_BRANCH line)\n' "help"; printf '%s\n' "$hd"; fail=$((fail+1))
fi
cmp_verb "whoami-empty"      noop        whoami
cmp_verb "whoami-joined"     joined      whoami
cmp_verb "channels-empty"    noop        channels
cmp_verb "channels"          two_peers   channels
cmp_verb "list-empty"        noop        list
cmp_verb "list"              two_peers   list
cmp_verb "active-none"       two_peers   active
cmp_verb "inbox"             noop        inbox dev/mbp
cmp_verb "inbox-bare-refuse" noop        inbox bare
cmp_verb "auth-status"       noop        auth status nuc
cmp_verb "bootstrap"         noop        bootstrap dev lead
cmp_verb "bootstrap-default" noop        bootstrap dev
cmp_verb "unknown-verb"      noop        frobnicate

echo "=== Class B (mutating) ==="
cmp_verb "join"              noop        join dev
cmp_verb "join-explicit"     noop        join dev myalias
cmp_verb "join-idempotent"   joined      join dev
cmp_verb "send-to-peer"      joined_peer send dev/mbp hello world
cmp_verb "send-bare-alias"   joined_peer send mbp hi
cmp_verb "send-empty"        joined_peer send dev/mbp
cmp_verb "leave"             joined      leave
cmp_verb "leave-not-joined"  noop        leave
cmp_verb "rename"            joined      rename newname
cmp_verb "unregister"        joined_peer unregister dev/mbp
cmp_verb "unregister-missing" noop       unregister dev/ghost
cmp_verb "prune-empty"       noop        prune
cmp_verb "register-dep"      noop        register

echo "=== trailing-junk ruled delta (go errors, bash discards -> EXPECTED divergence) ==="
export CBUS_DIR="$ROOT/bus"; rm -rf "$CBUS_DIR"; mkdir -p "$CBUS_DIR"
gj=$(CLAUDE_CODE_SESSION_ID=SID "$GO" whoami junk 2>&1); gjr=$?
bj=$(CLAUDE_CODE_SESSION_ID=SID "$BASH_CBUS" whoami junk 2>&1); bjr=$?
echo "whoami junk: bash rc=$bjr ('$bj') | go rc=$gjr ('$gj')  [delta: go rejects trailing junk]"
[ "$gjr" != 0 ] && echo "  -> go correctly rejects (ruled delta): PASS" || { echo "  FAIL"; fail=$((fail+1)); }

echo
echo "SWEEP: $pass pass / $fail fail"
echo "workdir: $ROOT"
exit "$fail"
