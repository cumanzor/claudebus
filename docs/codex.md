# Codex sessions as peers

The bus is harness-neutral: a Codex CLI session can hold a channel alias the
same as a Claude Code one, and the differences are absorbed by cbus rather
than pushed onto peers. Codex has no equivalent of the `Monitor` tool, so a
codex peer never runs `cbus tail` — cbus does the listening for it, three
ways depending on how codex is running:

- **`cbus codex [--channel CH] [--alias AL] [--thread ID] [codex args...]`** — the one-command
  interactive path. It stands up a per-peer `codex app-server`, launches a
  `codex --remote` TUI against it, learns the TUI's thread id from a passive
  server connection (hooks do not fire in this topology, so discovery is a
  protocol notification, not a hook), joins the bus as that thread, and runs a
  bridge that turns each bus message into a codex turn — steering an active
  turn when one is in flight, opening one when idle.
- **`cbus codex-bridge <ch>/<alias> --sock PATH [--thread ID] [--no-resume]`** — the bridge
  alone, for wiring up an app-server you manage yourself. It arms as the
  alias's listener (so liveness is real, same as any peer) and skips presence
  frames deliberately: a codex injection costs a full model turn, too much for
  join/leave ceremony.
- **`cbus codex-stop-hook`** — the fallback for plain `codex exec` workers,
  where there is no app-server but hooks do fire: on each Stop event it
  long-polls the inbox under codex's hook timeout and, when traffic arrived,
  returns a block decision that codex injects as a continuation turn. Silence
  lets the stop through — a timeout is treated as a failure, never a signal.

## Resuming a session onto the bus

Args pass through to codex, so an existing session joins the bus by resuming it:

```sh
cbus codex --channel cbus-winport --alias advisor resume 01a06968-3620-7a63-a5ba-1b25c394ccbd
cbus codex --channel cbus-winport --alias advisor resume --last
cbus codex --channel cbus-winport --alias advisor resume            # picker
```

The peer joins under the resumed session id, which is the thread id, so the alias comes back
carrying the history it had. Two things differ from a fresh launch, both forced by the
app-server's behaviour rather than chosen:

A resumed thread never emits `thread/started`. It announces itself through
`thread/status/changed`, so on a resume the wrapper accepts any notification that names a
threadId. A fresh launch emits nothing else in that window, so the cwd hard-check the fresh
path relies on is untouched. cwd is not an identity check on a resume anyway: the session
carries the cwd it was recorded in, which is legitimately not where you are resuming it from.
When the command line names a session id, that id is checked against the thread the server
announces and a mismatch is refused.

The thread's writer role goes to ONE connection, first come. The TUI has to be the one that
takes it, so the wrapper waits for the server to name the thread before starting the bridge,
and the bridge then attaches without resuming (`--no-resume` is the same thing for a bridge you
wire up yourself). Get that order wrong and the failure is loud in the wrong place: the human's
window exits 1 with "already has an active writer".

One consequence worth knowing: an app-server that outlives its wrapper keeps that lock, and the
next resume of the same session is refused until it dies. `pkill` on the wrapper leaves exactly
that orphan, because the group teardown never runs. Quit the TUI instead.

`cbus hook-join` rounds it out: a harness-neutral SessionStart hook that
auto-joins `$CBUS_CHANNEL`, so any harness with hooks can arrive on the bus
without a human typing the join. A committed `profiles/codex.md` carries the
peer-side doctrine (chiefly: never run `cbus tail`; the bridge listens for you).
