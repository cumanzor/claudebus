Output demo.gif

Set Shell bash
Set FontSize 14
Set Width 1280
Set Height 580
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms

Hide
Type `export CBUS_DIR=/tmp/cbus-demo; rm -rf /tmp/cbus-demo /tmp/demo; mkdir -p /tmp/demo; tmux -L vhsdemo kill-server 2>/dev/null; tmux -L vhsdemo -f /dev/null new-session -d -s demo -c /tmp/demo "CBUS_SESSION_ID=9f21c4ae-30d2-4b8e-a25e-1c05b1de6a77 PS1='❯ ' exec bash --norc" && tmux -L vhsdemo set -g status off && tmux -L vhsdemo set -g pane-border-style fg=colour238 && tmux -L vhsdemo set -g pane-active-border-style fg=colour245 && tmux -L vhsdemo split-window -h -t demo -c /tmp/demo "CBUS_SESSION_ID=4e8d19c3-77aa-4f01-9c4e-2ab0f37e5512 PS1='❯ ' exec bash --norc" && tmux -L vhsdemo select-pane -L && tmux -L vhsdemo attach -t demo`
Enter
Sleep 2s
Show

Type "cbus join demo"
Sleep 400ms
Enter
Sleep 1.2s
Type "cbus tail demo/main"
Sleep 400ms
Enter
Sleep 1.5s

Ctrl+B
Right
Sleep 400ms

Type "cbus join demo"
Sleep 400ms
Enter
Sleep 2s
Type "cbus send main 'tests are green — merging now'"
Sleep 500ms
Enter
Sleep 2.5s
Type "cbus list"
Sleep 400ms
Enter
Sleep 3.5s
