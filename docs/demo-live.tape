# Live two-session demo: real Claude Code sessions talking over cbus.
# Prep before recording (the tape assumes it):
#   mkdir -p /tmp/cbus-live/.claude
#   settings.json there: {"permissions":{"allow":["Bash(cbus:*)","Monitor","Skill"]},
#                         "statusLine":{"type":"command","command":"true"}}
#   plus one headless warm-up run in that dir so no trust dialog appears.
# Raw runtime ~85s; the shipped gif is this recording at 2.5x (see ffmpeg cmd in
# the commit that added it).

Output demo-live.mp4
Output demo-live-raw.gif

Set Shell bash
Set FontSize 14
Set Width 1280
Set Height 640
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 15ms

Hide
Type `export CBUS_DIR=/tmp/cbus-demo2; rm -rf /tmp/cbus-demo2; tmux -L vhslive kill-server 2>/dev/null; tmux -L vhslive -f /dev/null new-session -d -s live -c /tmp/cbus-live "claude --strict-mcp-config --model haiku 'Join the cbus channel demo with alias main and arm the persistent Monitor listener exactly as the join output instructs, then say only: standing by. When a presence event announces a NEW PEER joining the channel, send that peer a short ping over the bus with cbus send, asking if it is alive. When its reply arrives, state in one line what it answered.'" && tmux -L vhslive set -g status off && tmux -L vhslive set -g pane-border-style fg=colour238 && tmux -L vhslive set -g pane-active-border-style fg=colour245 && tmux -L vhslive set -g default-command "PS1='❯ ' bash --norc" && tmux -L vhslive attach -t live`
Enter
Sleep 3s
Show

Sleep 30s

Ctrl+B
Type "%"
Sleep 1.5s

Type "claude --strict-mcp-config --model haiku 'join cbus channel demo and arm the Monitor listener as the join output instructs, then stand by; when a bus message arrives, reply to its sender over the bus: alive and standing by'"
Sleep 500ms
Enter
Sleep 62s
