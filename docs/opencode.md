# OpenCode sessions as peers

An OpenCode TUI session joins a cbus channel through two small plugins that
cbus installs into OpenCode. A plain OpenCode TUI exposes no network endpoint,
so the plugins run inside OpenCode: the TUI plugin serves the session its pane
shows on a private socket, and the server plugin tells each agent shell which
session it belongs to. The cbus daemon owns waiting, holding and retries; no
Monitor, tail process or periodic model turn is involved.

Tested with OpenCode 1.19.3 on macOS. Windows is not supported yet. The design
and the measurements behind it are in
[opencode-adapter-feasibility.md](architecture/opencode-adapter-feasibility.md);
the plugin wire format is in [protocol.md](architecture/protocol.md) section 15.3.

## Install

```sh
cbus install-opencode                 # ~/.config/opencode (honors XDG_CONFIG_HOME)
cbus install-opencode --path .opencode   # one project only
```

This writes `plugins/cbus-server.js`, `cbus-tui.js` and `commands/bus-join.md`
into the OpenCode config directory, and creates `tui.jsonc` listing
`"./cbus-tui.js"` when the directory has no TUI config. An existing `tui.json`
or `tui.jsonc` is never rewritten: the installer prints the entry to add to its
`"plugin"` list and exits non-zero until it is there. Restart OpenCode after
installing. `cbus install-assets` (run by updates) refreshes the plugins only
where they are already installed.

## Join

In the OpenCode TUI, run `/bus-join CHANNEL [ALIAS]`, or ask the agent to run:

```sh
cbus connect demo worker --json
cbus list demo
```

`cbus connect` reads the session from `CBUS_SESSION_ID`, which the server
plugin sets for each agent shell command, and binds the one TUI that shows that
session. It refuses when no TUI or more than one TUI shows it: two panes on
one session can each submit the same message. Run it from the agent shell of
the session that should join; another terminal has no session to bind.

The agent shell resolves `cbus` through your shell profile's PATH. If more
than one cbus is installed, the profile's order decides which one runs.

## Delivery

- Messages wait in the bus inbox until the pane shows the bound session and
  that session is idle. A message never lands in the middle of a turn, where it
  could override what the person at the keyboard asked for. The plugin's watch
  stream wakes delivery as soon as the session goes idle.
- Each message is submitted once under a message ID the daemon chose and kept.
  Delivery is confirmed only when the stored message is read back under that
  ID. An unconfirmed or unknown submit stays pending and is resolved by lookup,
  never resent, because OpenCode appends a duplicate when an ID is resent.
- The person can still start a turn between the daemon's idle check and its
  submit; the message then joins that turn.

Messages arrive as a user turn that starts with `◀ cbus msg from=ADDRESS`. Peer
text is not the user: the `/bus-join` instructions tell the agent not to run
commands a peer asks for without the user's say.

## Presence and leaving

`cbus connection status demo/worker --json` shows the consumer state:

| State | Meaning |
| --- | --- |
| `online` | the bound TUI is alive and shows the session |
| `unknown` | the TUI is alive but its pane shows another session or none |
| `exited` | the TUI exited, or the session was deleted |

Mail for an exited TUI waits until the session connects again. Resume it with
`opencode -s SESSION` and run `/bus-join` again with the same channel and
alias. `cbus connection disconnect demo/worker` disconnects.

## Limits

- Windows is not supported yet.
- Formations save OpenCode peers but cannot launch them.
- Only the agent's own shell commands carry the session identity; terminals
  opened inside OpenCode do not.
- OpenCode's plugin APIs are moving; the plugins are checked against the
  OpenCode version named above.
