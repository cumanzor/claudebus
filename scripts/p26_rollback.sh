#!/usr/bin/env bash
# P2.6 rollback-safety: cbus-go WRITES state; bash cbus READS + OPERATES on all of it
# (so a rollback to bash after cbus-go ran finds fully-usable state). Hermetic.
# NB: checks capture output into vars then string-match — NOT `| grep -q`, which
# closes the pipe early and SIGPIPE-kills the producer under `set -o pipefail`.
set -uo pipefail
REPO="${REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"; BASH_CBUS="$REPO/bin/cbus"
ROOT=$(mktemp -d); GO=$ROOT/cbus-go; export CBUS_DIR="$ROOT/bus"; mkdir -p "$CBUS_DIR"
( cd "$REPO" && go build -o "$GO" ./cmd/cbus ) || exit 1
fail=0; ok(){ echo "  PASS $*"; }; no(){ echo "  FAIL $*"; fail=$((fail+1)); }
has(){ case "$1" in *"$2"*) return 0;; *) return 1;; esac; }

echo "=== cbus-go WRITES state ==="
CLAUDE_CODE_SESSION_ID=SGO   "$GO" join dev me   >/dev/null
CLAUDE_CODE_SESSION_ID=OTHER "$GO" join dev mbp  >/dev/null
CLAUDE_CODE_SESSION_ID=SGO   "$GO" send dev/mbp "hello from go" >/dev/null
echo "  go wrote dev/me (SGO) + dev/mbp (OTHER) + a send + join-presence"
echo "  meta.json D3 lastActivity: $(grep -o '"lastActivity": *"[^"]*"' "$CBUS_DIR/dev/me/meta.json" | head -1)"

echo "=== bash cbus READS the go-written state ==="
L=$(CLAUDE_CODE_SESSION_ID=SGO "$BASH_CBUS" list)
has "$L" "dev/me"  && ok "bash list sees dev/me"  || no "bash list dev/me"
has "$L" "dev/mbp" && ok "bash list sees dev/mbp" || no "bash list dev/mbp"
W=$(CLAUDE_CODE_SESSION_ID=SGO "$BASH_CBUS" whoami)
has "$W" "dev/me" && ok "bash whoami sees go registration" || no "bash whoami"
C=$(CLAUDE_CODE_SESSION_ID=SGO "$BASH_CBUS" channels)
has "$C" "dev" && ok "bash channels sees go channel" || no "bash channels"
I=$(CLAUDE_CODE_SESSION_ID=SGO "$BASH_CBUS" inbox dev/mbp)
has "$I" "dev/mbp/inbox.jsonl" && ok "bash inbox path on go peer" || no "bash inbox"

echo "=== bash cbus OPERATES on the go-written state ==="
CLAUDE_CODE_SESSION_ID=SGO "$BASH_CBUS" send dev/mbp "hello from bash rollback" >/dev/null
INB=$(cat "$CBUS_DIR/dev/mbp/inbox.jsonl")
has "$INB" "hello from bash" && ok "bash send appends to go inbox" || no "bash send"
has "$INB" "hello from go" && ok "go's earlier send still present (coexist)" || no "go send lost"
CLAUDE_CODE_SESSION_ID=BR "$BASH_CBUS" unregister dev/mbp >/dev/null
[ ! -d "$CBUS_DIR/dev/mbp" ] && ok "bash unregister removes go peer" || no "bash unregister"
CLAUDE_CODE_SESSION_ID=SGO "$BASH_CBUS" rename renamed >/dev/null 2>&1
[ -d "$CBUS_DIR/dev/renamed" ] && ok "bash rename mv's the go-written dir" || no "bash rename"
CLAUDE_CODE_SESSION_ID=SGO "$BASH_CBUS" leave >/dev/null
[ ! -d "$CBUS_DIR/dev/renamed" ] && ok "bash leave removes go registration" || no "bash leave"

echo
[ "$fail" = 0 ] && echo "ROLLBACK-SAFETY: ALL PASS — bash fully reads+operates on cbus-go-written state" || echo "ROLLBACK: $fail FAIL"
exit "$fail"
