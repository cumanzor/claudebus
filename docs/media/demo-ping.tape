# Ping demo (demo-ping.gif): two Claude Code sessions that are already open join one
# cbus channel with /bus-join (native connect), then each pings the other and gets an
# ack. Only the four typed prompts are on camera. Model: claude-opus-5-5 in both.
#
# Off camera (a scratch setup under /tmp/cbd, never the real store):
#   - bin/cbus built from this repo; bin/claude a shim that runs
#     claude --strict-mcp-config --setting-sources project,local "$@"
#   - env.sh runs its command under env -i with only HOME, USER, SHELL, TERM, LANG, TMPDIR,
#     PATH=/tmp/cbd/bin:..., CBUS_DIR=/tmp/cbd/bus (own daemon) and CBUS_HOST=laptop
#   - work/.claude/settings.json: allow Bash(cbus:*), Bash(tmux:*), Bash(ls:*), Read,
#     Write, Edit, Glob, Grep; deny Monitor; statusLine muted; promptSuggestionEnabled
#     false (no ghost-text suggestion in the input box)
#   - work/.claude/commands/bus-join.md copied from this repo's commands/
#   - a private tmux server (tmux -L cbdemo -f /dev/null), two panes side by side, each
#     running an idle "claude --model claude-opus-5-5"; the left pane is focused
# Run: cd /tmp/cbd && ./env.sh vhs demo-ping.tape
#
# Post: idle stretches (a model thinking or waiting) shortened to 0.8 s each, 3 s final
# hold, 10 fps. Claude Code's fixed peer-message notice is covered in place with one dim
# line, "[Claude Code peer-message notice]" (same method and verification as
# demo-fleet.tape). Then scale=1200, fps=8, 48-colour palette as in demo-fleet.tape.
# Trim tools (scratch, not committed; tied to this font, size and theme): grid.py
# (box finder), detect.py (START/END template match), trim.py (cover + label),
# verify.py (whole-frame word search + outside-pixel diff), run from /tmp/cbd/trim.

Output rec/ping.mp4

Set Shell bash
Set FontSize 15
Set Width 1400
Set Height 700
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms

Hide
Type "/tmp/cbd/start-ping.sh"
Enter
Sleep 5s
Show

Sleep 1.5s
Type "/bus-join demo alice"
Sleep 600ms
Enter
Sleep 18s

Ctrl+B
Type "o"
Sleep 800ms
Type "/bus-join demo bob"
Sleep 600ms
Enter
Sleep 17s

Ctrl+B
Type "o"
Sleep 800ms
Type "ping bob over the bus and wait for his ack"
Sleep 600ms
Enter
Sleep 19s

Ctrl+B
Type "o"
Sleep 800ms
Type "ping alice over the bus and wait for her ack"
Sleep 600ms
Enter
Sleep 18s
