# Claude Monitor comparison, 2026-09-18

These are retained observations from actual Claude Code 2.1.277 interactive PTYs
using a local scripted fake provider. They are not fixtures invented after the
run, and are not real signed-in-account field evidence.

| Snapshot | What it contains |
| --- | --- |
| [Runtime pair](runtime-pair.json) | Original aggregate, including both arms and the separate socket regression. |
| [Bounded arm](bounded.json) | Flag true; Monitor expired at the requested two-second deadline. 21 assertions and four cleanup checks passed. |
| [Persistent arm](persistent.json) | Flag false; same waiter survived twelve seconds, woke on a late signal and stopped with TaskStop. 20 assertions and four cleanup checks passed. |
| [Provenance manifest](manifest.json) | Original and published byte counts/SHA-256 values, original paths and exact transformations. |

The aggregate's two Monitor cases equal their respective standalone snapshots.
Both used the same binary and script hashes, default permission mode, fixed
`tengu_amber_sentinel=true` and identical `persistent=true, timeout_ms=2000` input.
`tengu_breezy_crescent` is the changed flag. Process/session IDs, task output,
request timings, checks, cleanup and limitations are retained as recorded.

## Reproduce

The tested canary is committed in
[PR #26](https://github.com/cumanzor/claudebus/pull/26), source `6f7961f`.
Its tested SHA-256 is
`741d0e8c96a6e0d4ce902d84b975d6ad7d8f0d6a38d2c34ad00989625178d03e`.
With the matching Claude binary available, run:

```sh
python3 scripts/claude_interactive_wake_canary.py --transport monitor --monitor-bounded
python3 scripts/claude_interactive_wake_canary.py --transport monitor
```

The first command is the bounded control. The second seeds the flag false.
Original runtime paths inside the JSON are provenance, not usable download links.
New runs will have different paths, sessions, timestamps and task identifiers.

## Publication and limits

The local username/home prefix is replaced by `<LOCAL_HOME>`. String-valued
`description` fields inside `monitorSchema` are omitted because the native tool's
help text is unrelated to the result; validation structure and property names
remain, including the schema for the tool's `description` argument. No other
values are changed. The manifest distinguishes hashes of the original bytes from
hashes of these public snapshots. Complete originals are retained separately in
the project's evidence store; they are not needed to read these results.

The synthetic two-flag snapshot, fake API key and firstParty provider route do not
prove behavior with a real signed-in configuration. The two-second requested
limit does not measure the five-/30-minute defaults. Environment traffic controls
were used, not an OS-enforced network sandbox. This Monitor pair did not test cbus
integration; the aggregate's socket regression is a separate native capability
check. Successful cleanup is recorded, not inferred from process disappearance.
