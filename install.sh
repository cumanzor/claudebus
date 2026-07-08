#!/usr/bin/env bash
# install claudebus into your Claude Code / shell environment.
#   - bin/cbus         -> ~/.local/bin/cbus            (the message-bus CLI)
#   - bin/cc-branch.sh -> ~/.claude/bin/cc-branch.sh   (fork helper, only for /bus-branch)
#   - commands/*.md    -> ~/.claude/commands/          (slash commands)
#
# re-run safe (overwrites the installed copies). Use --link to symlink instead
# of copy so future `git pull`s take effect without reinstalling.
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
place "$here/commands/bus-listen.md" "$CMD_DIR/bus-listen.md"
place "$here/commands/bus-branch.md" "$CMD_DIR/bus-branch.md"
place "$here/commands/bus-rename.md" "$CMD_DIR/bus-rename.md"
chmod +x "$BIN_DIR/cbus" "$CC_BIN_DIR/cc-branch.sh" 2>/dev/null || true

echo
echo "done. ensure $BIN_DIR is on your PATH."
echo "NOTE: commands/bus-branch.md hardcodes the path to cc-branch.sh for one"
echo "      user — edit it if your \$HOME or config dir differs. See README.md."
