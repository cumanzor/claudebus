#!/usr/bin/env bash
# LEGACY installer — installs the RETIRED bash client. Since the 2026-07-13 cutover,
# the production `cbus` is the Go binary (built from cmd/cbus; see
# docs/architecture/cutover-decision-package.md). Running THIS script overwrites
# ~/.local/bin/cbus with the bash implementation — i.e. it IS the rollback procedure.
# It also copies commands/*.md (currently the only script that does).
# Kept until P3 homogenization deletes the bash artifacts (compat-deletion-plan.md).
#   - bin/cbus         -> ~/.local/bin/cbus            (bash client — ROLLBACK)
#   - bin/cc-branch.sh -> ~/.claude/bin/cc-branch.sh   (retired fork helper)
#   - commands/*.md    -> ~/.claude/commands/          (slash commands)
#
# re-run safe (overwrites the installed copies). Use --link to symlink instead.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mode="copy"; [ "${1:-}" = "--link" ] && mode="link"

BIN_DIR="${CLAUDEBUS_BIN_DIR:-$HOME/.local/bin}"
CC_BIN_DIR="${CLAUDE_BIN_DIR:-$HOME/.claude/bin}"
CMD_DIR="${CLAUDE_COMMANDS_DIR:-$HOME/.claude/commands}"

place() { # <src> <dst>
  mkdir -p "$(dirname "$2")"
  if [ "$mode" = "link" ]; then ln -sfn "$1" "$2"; else cp "$1" "$2"; fi
  echo "  $2"
}

echo "installing claudebus ($mode):"
place "$here/bin/cbus"          "$BIN_DIR/cbus"
place "$here/bin/cc-branch.sh"  "$CC_BIN_DIR/cc-branch.sh"
place "$here/commands/bus-join.md" "$CMD_DIR/bus-join.md"
place "$here/commands/bus-branch.md" "$CMD_DIR/bus-branch.md"
place "$here/commands/bus-rename.md" "$CMD_DIR/bus-rename.md"
chmod +x "$BIN_DIR/cbus" "$CC_BIN_DIR/cc-branch.sh" 2>/dev/null || true

echo
echo "done. ensure $BIN_DIR is on your PATH."
echo "WARNING: this installed the LEGACY bash client over $BIN_DIR/cbus."
echo "         If you did not intend a rollback, rebuild the Go client:"
echo "         go build -ldflags \"-X main.version=\$(git describe --tags --always --dirty)\" -o $BIN_DIR/cbus ./cmd/cbus"
