#!/usr/bin/env bash
# Install / refresh cbus-go — the transitional Go client — SIDE-BY-SIDE with the bash
# cbus, per the ratified P1 coexistence plan. This script touches ONLY cbus-go:
#
#   - builds cmd/cbus with a -ldflags version stamp
#   - places it at ~/.local/bin/cbus-go (mode-agnostic, symlink-safe — M12)
#   - CHECKS and REPORTS the SessionEnd hook wiring (read-only)
#
# It NEVER touches the bash cbus, install.sh, or settings.json. The cutover — making
# `cbus` resolve to the Go binary and wiring the SessionEnd hook to `cbus-go
# hook-exit` — is a SEPARATE, user-gated step (P2.6 decision package), not this script.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${CLAUDEBUS_BIN_DIR:-$HOME/.local/bin}"
DST="$BIN_DIR/cbus-go"

# version stamp: prefer `git describe` (tags), else short hash, mark -dirty; "dev" if
# not a git checkout.
ver="$(git -C "$here" describe --tags --always --dirty 2>/dev/null \
      || git -C "$here" rev-parse --short HEAD 2>/dev/null \
      || echo dev)"

echo "building cbus-go ($ver)…"
tmp="$(mktemp -t cbus-go.XXXXXX)"
trap 'rm -f "$tmp"' EXIT
( cd "$here" && go build -ldflags "-X main.version=$ver" -o "$tmp" ./cmd/cbus )

# Mode-agnostic placement (M12 fix): rm the destination FIRST, so a plain `cp` can
# never follow an existing symlink and write THROUGH it into the repo source (nor
# abort on a copy-onto-symlink). This works whether $DST is absent, a regular file,
# or a symlink from an earlier `install.sh --link`.
mkdir -p "$BIN_DIR"
rm -f "$DST"
cp "$tmp" "$DST"
chmod +x "$DST"
echo "installed: $DST  ($("$DST" --version))"

# ---- SessionEnd hook-wiring CHECK — REPORT ONLY (never edits settings.json) --------
echo
echo "SessionEnd hook wiring (report only — editing settings.json is cutover-gated):"
settings="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/settings.json"
if [ ! -f "$settings" ]; then
  echo "  · settings.json not found at $settings — SessionEnd hook not wired"
elif grep -q 'cbus-go hook-exit' "$settings" 2>/dev/null; then
  echo "  ✓ SessionEnd wired to 'cbus-go hook-exit' (cutover state)"
elif grep -q 'cbus hook-exit' "$settings" 2>/dev/null; then
  echo "  · SessionEnd wired to bash 'cbus hook-exit' (pre-cutover) — cutover repoints it to cbus-go"
elif grep -q 'hook-exit' "$settings" 2>/dev/null; then
  echo "  · a 'hook-exit' hook is present but not recognized — inspect $settings"
else
  echo "  · no cbus hook-exit wiring in settings.json"
  echo "    to enable a graceful-departure announce at cutover, wire a SessionEnd hook"
  echo "    running 'cbus-go hook-exit' (fed the hook's {session_id} JSON on stdin)."
fi
