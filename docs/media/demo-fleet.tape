# Fleet demo: an orchestrator session spawns its own coder + reviewer as panes
# via `cbus spawn pane`, then drives a tiny dev loop over the bus. Nothing is
# typed after the recording starts — the whole choreography is the orchestrator's
# first prompt.
# Prep (the tape assumes it): /tmp/cbus-live with .claude/settings.json allowing
# Bash(cbus:*), Bash(tmux:*), Monitor, Skill, Write, Edit, Read and a muted
# statusLine, plus /tmp/cbus-live/bin/claude shim adding --strict-mcp-config.
# Raw runtime ~150s; shipped gif is ~3x.

Output demo-fleet.mp4

Set Shell bash
Set FontSize 13
Set Width 1500
Set Height 640
Set Padding 8
Set Theme "Catppuccin Mocha"

Hide
Type `export CBUS_DIR=/tmp/cbus-demo3 PATH=/tmp/cbus-live/bin:$PATH; rm -rf /tmp/cbus-demo3 /tmp/cbus-live/greet.py; tmux -L vhsfleet kill-server 2>/dev/null; tmux -L vhsfleet -f /dev/null new-session -d -s fleet -c /tmp/cbus-live "claude --model haiku 'You orchestrate a tiny dev formation. 1: join the cbus channel devdemo with alias main and arm the persistent Monitor listener exactly as the join output instructs. 2: one at a time, each in the FOREGROUND, never backgrounded with &: run cbus spawn pane devdemo --name coder --model haiku, wait for it to return, run cbus spawn pane devdemo --name reviewer --model haiku, then run tmux select-layout even-horizontal. 3: when both workers have announced themselves on the bus, send coder this task with cbus send: write greet.py in the current directory, a greet(name) function returning a friendly greeting, report back to main in one line when done. 4: when coder reports done, ask reviewer via cbus send to read greet.py and reply to main with a one-line verdict. 5: when the verdict arrives, announce the outcome in one line and stand down.'" && tmux -L vhsfleet set -g status off && tmux -L vhsfleet set -g pane-border-style fg=colour238 && tmux -L vhsfleet set -g pane-active-border-style fg=colour245 && tmux -L vhsfleet attach -t fleet`
Enter
Sleep 3s
Show

Sleep 170s
