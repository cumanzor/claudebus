package main

// COMPAT(P3 #4): the const below still carries bash's `CBUS_PYTHON (default python3)`
// env line — a bash-ism (cbus-go has no python dependency). Drop that line at
// homogenization; it stays now only to keep the help within one ruled delta of bash.
//
// usage is `cbus --help`, byte-exact from the bash USAGE heredoc (bin/cbus:856-910)
// with ONE ruled delta: the obsolete `CC_BRANCH (fork helper path…)` env line is
// dropped — cbus-go's `branch` is native (P2.5 TerminalForker), so CC_BRANCH is no
// longer consulted (port-map delta table). Self-id stays `cbus` (Option X): cutover
// is a pure binary swap. Post-cutover additions with no bash counterpart: the
// `spawn` block (cbus-ijx.2) and the `--model` flag on branch/spawn. Everything
// else matches bash byte-for-byte.
const usage = `cbus — message bus between live Claude Code sessions, in named channels

  cbus join <channel> [alias]      join a channel (alias auto: main, fork-N;
                                   prunes dead peers in the channel first)
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
  cbus active [channel]            only peers currently listening (= list --active)
  cbus channels                    channels with peer counts
  cbus whoami                      this session's channel/alias registrations
  cbus inbox <channel>/<alias>     print inbox path
  cbus bootstrap <channel> [parent]  print the canonical fork-child prompt
  cbus branch [target] [channel]   join + fork a bootstrapped child in one shot
                                   (target: window|tab|tmux; channel auto-derives
                                   from the git repo name; arm the Monitor after)
       --model <m>                 launch the child on a specific model
                                   (e.g. sonnet, opus, fable)
  cbus spawn [target] [channel]    open a FRESH session (blank transcript, not
                                   a fork) that joins + arms the channel on its
                                   own (target: window|tab|tmux; local channel
                                   auto-derives; channel@host must be explicit,
                                   no alias — the child picks its own)
       --model <m>                 launch the child on a specific model
  cbus prune [channel]             remove dead peers (and empty channels)
  cbus leave [channel]             leave channel(s) this session joined
  cbus rename <new-alias> [channel]  rename this session's local alias (mv dir +
                                   meta); re-arm the Monitor on the new address
  cbus unregister <channel>/<alias>  force-remove any peer

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
  cbus leave <ch>@<host>           drop THIS session's identity marker
  cbus auth set <host> [--token V] [--cf-id V] [--cf-secret V]   (V='-'=stdin)
  cbus auth status [host]          credential state, masked

  aliases are explicit — pick a short hostname/role (e.g. mbp, nuc). endpoint
  autodetects: loopback :8090 on the relay host, else the public CF hostname.
  known hosts: nuc (override/add via CBUS_SITE_<HOST>_URL).

convention: channel "global" is the machine-wide orchestrator bus; per-task or
per-repo channels (e.g. the repo name) are the default for parent/fork pairs.

env: CBUS_DIR (default ~/.claude-bus), CBUS_PYTHON (default python3),
     CBUS_SITE_<HOST>_URL / CBUS_RELAY_LOCAL_URL (relay endpoints)
`
