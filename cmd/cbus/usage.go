package main

// usage is `cbus --help`, from the bash USAGE heredoc (bin/cbus:856-910, now
// git-history) with two ruled deltas: the obsolete `CC_BRANCH (fork helper path…)`
// env line — cbus-go's `branch` is native (P2.5 TerminalForker), so CC_BRANCH is no
// longer consulted (port-map delta table) — and the `CBUS_PYTHON (default python3)`
// env line, dropped at P3 homogenization (cbus-go has no python dependency).
// Self-id stays `cbus` (Option X): cutover was a pure binary swap. Post-cutover
// additions with no bash counterpart: the `spawn` block (cbus-ijx.2), the
// `--model`/`--name` flags on branch/spawn, and the `formation` block.
const usage = `cbus — message bus between live Claude Code sessions, in named channels

  cbus join <channel> [alias]      join a channel (alias auto: main, fork-N;
                                   prunes dead peers in the channel first)
       --session-id <id>           act AS this session id on join/leave/rename/
                                   send (overrides the $*_SESSION_ID env chain);
                                   for hooks and scripted multi-session drivers
  cbus tail <channel>/<alias>      stream inbox — arm under the Monitor tool,
                                   NEVER in Bash (it blocks forever)
  cbus send <target> [opts] TEXT   append a message to a peer's inbox;
                                   target is <channel>/<alias>, or a bare
                                   <alias> within your own channel(s)
       --from <ch/alias>           override sender (default: auto-resolved)
       --force                     send even if target's listener died — best
                                   effort: a re-arm follows from the end of the
                                   inbox, so the line may never be delivered
                                   (a joined-but-not-yet-armed peer is always
                                   accepted: its first arm replays the inbox)
  cbus list [--active] [channel]   peers with listen/off state, host, cwd
       --json                      machine-readable; local targets only
  cbus active [channel]            only peers currently listening (= list --active)
  cbus channels [--json]           channels with peer counts
  cbus whoami [--json]             this session's channel/alias registrations
  cbus inbox <channel>/<alias>     print inbox path
  cbus bootstrap <channel> [parent] [child-alias]  print the canonical fork-child
                                   prompt (child-alias: the reserved-alias variant)
  cbus branch [target] [channel]   join + fork a bootstrapped child in one shot
                                   (target: window|tab|tmux|pane — pane splits
                                   your own tmux pane or iTerm2 session, and
                                   errors when in neither; channel auto-derives
                                   from the git repo name; arm the Monitor after;
                                   the child's alias is reserved at fork time and
                                   its session title matches it)
       --model <m>                 launch the child on a specific model
                                   (e.g. sonnet, opus, fable)
       --name <n>                  fix the child's alias AND session title
                                   (default: auto-pick — main, fork-N)
  cbus spawn [target] [channel]    open a FRESH session (blank transcript, not
                                   a fork) that joins + arms the channel on its
                                   own (target: window|tab|tmux|pane; local
                                   channel auto-derives — child alias reserved +
                                   titled like branch; channel@host must be
                                   explicit)
       --model <m>                 launch the child on a specific model
       --name <n>                  fix the child's alias AND session title
                                   (remote: pre-assigns the relay alias; omitted
                                   on remote, the child picks and the title is
                                   the address)
       --role <r>                  append the committed role prompt roles/<r>.md
                                   (spawn cwd's repo, else $CBUS_DIR/roles) to
                                   the child's first turn; defaults --name to the
                                   role and --model to its MODEL: line
                                   (spawn-only — a fork inherits its parent's
                                   intent, so branch refuses --role)
  cbus formation save <name> [ch]  capture a channel's topology (channel
                                   defaults to this session's); refreshes an
                                   existing file, preserving hand-edited fields
                                   (origin/model come from the birth-record when
                                   recorded; role/profile stay hand-maintained)
       --anchor key=value          record a hand anchor in drift_anchors
                                   (repeatable; a flag overwrites its own key,
                                   git_head stays machine-owned; convention:
                                   bdx=<epic-id> links the effort's tracker item)
  cbus formation apply <name>      relaunch a formation's MISSING peers on this
                                   host (sequential, anchor first); join the
                                   channel first — peers are briefed to answer you.
                                   name resolves runtime-first, then the repo's
                                   formations/ starter templates.
                                   pane peers split the LARGEST pane each time
                                   (applier + panes made this run), so a run tiles
                                   instead of shaving the applier. a peer's
                                   "split": right|down forces its divider; any
                                   declared direction turns off tmux's reflow for
                                   the whole run, so the file's layout stands
       --channel ch                target ch for this run (a template serves any
                                   effort; the envelope file is not changed)
       --dry-run                   print the plan, launch nothing
       --only a,b                  only these peers
       --wait <dur>                how long to wait for each peer to answer its
                                   kickoff (default 90s; 0 = launch and return)
       --brief TEXT                effort brief added to every peer's kickoff
  cbus formation bootstrap <name> <alias> [--brief TEXT]
                                   print ONE peer's first-turn prompt to paste
                                   by hand (the path for a peer apply will not
                                   launch — e.g. one recorded on another machine)
  cbus formation list              saved channel topologies ($CBUS_DIR/.formations)
  cbus formation show <name>       one formation's peers, flagging stale sids
                                   (recorded transcript gone) and TODO roles
  cbus formation rm <name>         delete a saved formation
  cbus selfupdate [--check] [--force]            update the running binary from
                                   the latest GitHub release (needs gh authed);
                                   --check reports without applying; then refreshes
                                   the installed commands + roles. Set CBUS_REPO
                                   or use a released binary (its repo is baked in)
  cbus install-commands [--path DIR] [--force]   write the embedded /bus-* skills
                                   to ~/.claude/commands (sha-guarded; --force
                                   overwrites a locally-edited file)
  cbus install-roles [--path DIR] [--force]      write the embedded role prompts
                                   to $CBUS_DIR/roles (the LoadRole fallback)
  cbus codex [--channel CH] [--alias AL] [codex args...]
                                   launch a codex --remote TUI as a bus peer: a
                                   per-peer app-server, the wrapper learns the
                                   TUI's thread and joins as it, and a bridge
                                   delivers bus messages into that thread (steer
                                   if busy, else a new turn). --channel auto-
                                   derives from the git repo; --alias defaults to
                                   codex
  cbus hook-join                   SessionStart hook: auto-join $CBUS_CHANNEL
                                   (alias $CBUS_ALIAS or auto) under the stdin
                                   session id; harness-neutral, silent, exit 0
  cbus codex-stop-hook [--wait D]  codex Stop hook (exec-worker fallback):
                                   long-poll this session's inbox, and on new
                                   traffic emit a block decision that codex
                                   injects as a continuation turn; no traffic
                                   before D (default 550s, under the codex
                                   timeout) allows the stop. never fails
  cbus codex-bridge <ch>/<al> --sock PATH [--thread ID]
                                   bridge a codex app-server thread to this
                                   alias's inbox: each bus message becomes a
                                   codex turn (steer if a turn is active, else
                                   start one). join the alias first; --thread
                                   adopts an existing thread, else one is made
  cbus prune [channel]             remove dead peers (and empty channels);
                                   [channel]@host reaps the RELAY spool instead
  cbus leave [channel]             leave channel(s) this session joined
  cbus rename <new-alias> [channel]  rename this session's local alias (mv dir +
                                   meta); re-arm the Monitor on the new address
  cbus unregister <channel>/<alias>  force-remove any peer
  cbus close <channel>/<alias> [...] [--force]   end peer sessions: SIGTERM the
                                   owning process, then sweep its terminal surface
                                   once the tty is dead (local only — a remote peer
                                   is closed on its own host). Registrations are
                                   left to the SessionEnd hook and prune; a peer
                                   that is already gone reports so and succeeds.
                                   --force escalates to SIGKILL after the grace

remote (relay-backed) channels — address form <channel>@<host>/<alias>:

  cbus send <ch>@<host>/<al> TEXT  POST to the relay (queues if peer offline)
       --from <ch@host/al>         override sender (default: THIS session's
                                   identity marker, set when it armed a remote
                                   tail on that channel; sessions never inherit
                                   another session's alias)
  cbus tail <ch>@<host>/<al>       print the Monitor ws arm spec (url +
                                   protocols) and claim the alias as this
                                   machine's identity on that channel
  cbus list [<ch>]@<host>          peers known to the relay (connected/queued)
  cbus prune [<ch>]@<host>         drop off relay peers with no queued mail
                                   (channel-scoped; omit <ch> to sweep the host)
  cbus leave <ch>@<host>           drop THIS session's identity marker
  cbus auth set <host> [--token V] [--cf-id V] [--cf-secret V]   (V='-'=stdin)
  cbus auth status [host]          credential state, masked

  aliases are explicit — pick a short hostname/role (e.g. mbp, nuc). endpoint
  autodetects: loopback :8090 on the relay host, else the public CF hostname.
  no built-in hosts: point each relay host at its base via CBUS_SITE_<HOST>_URL.

convention: channel "global" is the machine-wide orchestrator bus; per-task or
per-repo channels (e.g. the repo name) are the default for parent/fork pairs.

env: CBUS_DIR (default ~/.claude-bus),
     CBUS_SITE_<HOST>_URL / CBUS_RELAY_LOCAL_URL (relay endpoints),
     CBUS_REPO (owner/repo for selfupdate; baked into released binaries),
     CBUS_UPDATE_CHECK=1 (opt-in: a once-a-day 'update available' hint)
`
