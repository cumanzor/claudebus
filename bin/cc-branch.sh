#!/usr/bin/env bash
# fork the current Claude Code session into a new terminal/tmux window.
# must be run from inside a Claude Code session (reads CLAUDE_CODE_SESSION_ID).
# relaunches through `ccs <profile>` so the new window gets the correct CCS
# profile / config dir / PATH — a bare `claude` in a fresh login shell would
# resolve the wrong config dir and open a blank session.
set -euo pipefail

# args: [window|tab|tmux] [--prompt "initial turn for the forked session"]
target="window"
prompt=""
while [ $# -gt 0 ]; do
  case "$1" in
    --prompt) prompt="${2:-}"; shift 2 ;;
    window|tab|session|tmux) target="$1"; shift ;;
    *) target="$1"; shift ;;
  esac
done
sid="${CLAUDE_CODE_SESSION_ID:?not inside a Claude Code session}"
dir="$PWD"

# build the launch command, replicating this session's PATH so node/ccs/claude resolve.
if [[ "${CLAUDE_CONFIG_DIR:-}" == *"/.ccs/instances/"* ]]; then
  profile="$(basename "$CLAUDE_CONFIG_DIR")"
  launch="ccs $(printf '%q' "$profile") --resume $sid --fork-session"
else
  launch="claude --resume $sid --fork-session"
fi

# write a self-deleting launcher to avoid nested osascript/shell quoting.
work="$(mktemp -t cc-branch.XXXXXX)"
{
  printf '#!/usr/bin/env bash\n'
  printf 'export PATH=%q\n' "$PATH"
  [[ -n "${CLAUDE_CONFIG_DIR:-}" ]] && printf 'export CLAUDE_CONFIG_DIR=%q\n' "$CLAUDE_CONFIG_DIR"
  printf 'cd %q\n' "$dir"
  printf 'rm -f %q\n' "$work"
  if [[ -n "$prompt" ]]; then
    printf 'exec %s %q\n' "$launch" "$prompt"
  else
    printf 'exec %s\n' "$launch"
  fi
} > "$work"
chmod +x "$work"

run="/bin/bash $work"

case "$target" in
  window)
    osascript <<EOF
tell application "iTerm2" to create window with default profile command "$run"
EOF
    ;;
  tab|session)
    osascript <<EOF
tell application "iTerm2"
  tell current window to create tab with default profile command "$run"
end tell
EOF
    ;;
  tmux)
    [ -n "${TMUX:-}" ] || { echo "not inside a tmux session"; exit 1; }
    tmux new-window -n cc-branch "$run"
    ;;
  *)
    echo "unknown target '$target' (use: window | tab | tmux)"; exit 1
    ;;
esac

echo "forked session $sid into a new $target (${profile:-default} profile)"
