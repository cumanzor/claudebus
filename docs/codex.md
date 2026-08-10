# Codex sessions as peers

The bus is harness-neutral: a Codex CLI session can hold a channel alias the
same as a Claude Code one, and the differences are absorbed by cbus rather
than pushed onto peers. Codex has no equivalent of the `Monitor` tool, so a
codex peer never runs `cbus tail` — cbus does the listening for it, three
ways depending on how codex is running:

- **`cbus codex [--channel CH] [--alias AL] [codex args...]`** — the one-command
  interactive path. It stands up a per-peer `codex app-server`, launches a
  `codex --remote` TUI against it, learns the TUI's thread id from a passive
  server connection (hooks do not fire in this topology, so discovery is a
  protocol notification, not a hook), joins the bus as that thread, and runs a
  bridge that turns each bus message into a codex turn — steering an active
  turn when one is in flight, opening one when idle.
- **`cbus codex-bridge <ch>/<alias> --sock PATH [--thread ID]`** — the bridge
  alone, for wiring up an app-server you manage yourself. It arms as the
  alias's listener (so liveness is real, same as any peer) and skips presence
  frames deliberately: a codex injection costs a full model turn, too much for
  join/leave ceremony.
- **`cbus codex-stop-hook`** — the fallback for plain `codex exec` workers,
  where there is no app-server but hooks do fire: on each Stop event it
  long-polls the inbox under codex's hook timeout and, when traffic arrived,
  returns a block decision that codex injects as a continuation turn. Silence
  lets the stop through — a timeout is treated as a failure, never a signal.

`cbus hook-join` rounds it out: a harness-neutral SessionStart hook that
auto-joins `$CBUS_CHANNEL`, so any harness with hooks can arrive on the bus
without a human typing the join. A committed `profiles/codex.md` carries the
peer-side doctrine (chiefly: never run `cbus tail`; the bridge listens for you).
