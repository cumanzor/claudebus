# Fleet demo (demo-fleet.gif): an orchestrator session spawns coder, reviewer and
# documenter as tmux panes over native cbus connect (no Monitor), dispatches one small
# task, and announces the result. Nothing is typed after recording starts: the whole
# choreography is the orchestrator's first prompt. Model: haiku in all four seats.
#
# Off camera (a scratch setup under /tmp/cbd, never the real store):
#   - bin/cbus built from this repo; bin/claude a shim that runs
#     claude --strict-mcp-config --setting-sources project,local "$@"
#   - env.sh runs its command under env -i with only HOME, USER, SHELL, TERM, LANG, TMPDIR,
#     PATH=/tmp/cbd/bin:..., CBUS_DIR=/tmp/cbd/bus (own daemon) and CBUS_HOST=laptop
#   - work/.claude/settings.json: allow Bash(cbus:*), Bash(tmux:*), Bash(ls:*), Read,
#     Write, Edit, Glob, Grep; deny Monitor; statusLine muted
#   - a private tmux server (tmux -L cbdemo -f /dev/null), status off, pane titles on
#     top, focus-events on; the orchestrator is the first pane
#   - the orchestrator prompt:
#   You orchestrate a tiny dev team over cbus. Run each shell command in the foreground, one at a time, exactly as written:
#   1. cbus connect demo orchestrator
#   2. cbus spawn pane demo --name coder --model haiku, then end your turn and wait for coder's join notice.
#   3. Only after coder has joined: cbus spawn pane demo --name reviewer --model haiku, then wait for reviewer's join notice.
#   4. Only after reviewer has joined: cbus spawn pane demo --name documenter --model haiku, then wait for documenter's join notice.
#   5. Only after documenter has joined: tmux select-layout tiled
#   6. Join notices and bus messages arrive in this conversation by themselves: never use the Monitor tool, tail, sleep or polling. Just end your turn and wait.
#   7. cbus send demo/coder 'Create text.py in this directory with the Write tool: a function slugify(title) that lowercases and turns spaces into hyphens, so slugify("Hello World") returns "hello-world". Nothing more. Then reply with exactly: cbus send demo/orchestrator "text.py ready"'
#   8. When coder reports done: cbus send demo/reviewer 'Read text.py with the Read tool. The whole spec: lowercase, spaces become hyphens, slugify("Hello World") returns "hello-world". If it meets that spec reply with exactly: cbus send demo/orchestrator "approve" (otherwise send one line saying what fails).'
#   9. If the reviewer does not approve, send its note to demo/coder with cbus send and repeat step 8 once. When the reviewer approves: cbus send demo/documenter 'Create CHANGELOG.md in this directory with the Write tool, one line: slugify added and approved. Then reply with exactly: cbus send demo/orchestrator "done"'
#   10. When the documenter replies, say in one short line what the team shipped, then stop.
# Run: cd /tmp/cbd && ./env.sh vhs demo-fleet.tape
#
# Post: trimmed to 76 s, 1.5x, 10 fps. Claude Code appends a fixed peer-message notice
# ("This came from another Claude session ... permission laundering") to every
# delivered message; each occurrence is covered in place with one dim line,
# "[Claude Code peer-message notice]", box height unchanged, nothing else in the frame
# touched (per-frame template match on the fixed terminal grid, verified: zero notice
# text left, zero changed pixels outside the covered rectangles). Then:
#   ffmpeg -framerate 10 -i edit/%04d.png -filter_complex "scale=1280:-2:flags=lanczos,
#     split[a][b];[a]palettegen=max_colors=64:stats_mode=diff[p];
#     [b][p]paletteuse=dither=bayer:bayer_scale=3:diff_mode=rectangle" demo-fleet.gif
# Trim tools (scratch, not committed; tied to this font, size and theme): grid.py
# (box finder), detect.py (START/END template match), trim.py (cover + label),
# verify.py (whole-frame word search + outside-pixel diff), run from /tmp/cbd/trim.

Output rec/fleet.mp4

Set Shell bash
Set FontSize 15
Set Width 1600
Set Height 900
Set Padding 10
Set Theme "Catppuccin Mocha"

Hide
Type "/tmp/cbd/start-fleet.sh"
Enter
Sleep 1.5s
Show

Sleep 130s
