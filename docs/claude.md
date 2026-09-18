# Claude Code native connections

Native receive requires a cbus build containing the Claude adapter. The v0.12.2
release supports native Codex only. The Claude path is tested with the interactive
2.1.277 CLI on macOS; Linux acceptance is recorded separately with its binary
version. Desktop clients and native Windows support remain outside v1.

## Join from an existing session

Run `/bus-join CHANNEL [ALIAS]`, or have Claude run:

```sh
cbus connect demo worker --json
cbus list demo
```

For cross-machine delivery, use `cbus connect demo@server worker --json` and retain
`@server` in later commands. Relay authentication and a matching acknowledged-delivery
relay are required. The native socket and token come from this exact session's
Bash environment; do not copy them into commands, prompts or another session.
Normal Bash permission controls still apply. No restart, launcher wrapper, Monitor,
tail process or periodic model action is needed for a supported ordinary session.

The caller binds the actual Claude process, its private socket and the exact
session transcript. Missing, conflicting or ambiguous evidence fails explicitly.
A newly created session must have persisted its identity before it can connect;
a missing transcript alone is not a reason to restart it. Bare mode without a
messaging endpoint is unsupported. Terminal choice does not select the transport:
iTerm2, tmux and manually launched terminals use the same connection.

`socket-ready` describes an available native endpoint. It does not prove receipt.
Busy sessions process peer input after their current turn. Claude's hold/refuse
settings remain effective; cbus does not bypass them or infer which policy caused
an unconfirmed submission. After joining, the command instructions check the roster
once, retain known roles and announce membership changes to the user without bus
acknowledgments or repeated roster polling.

## Delivery and resume

```sh
cbus connection status demo/worker --json
cbus connection reconcile demo/worker --json
cbus connection disconnect demo/worker
```

Status and reconciliation are on-demand tools, not maintenance loops. A successful
socket write remains pending until the exact session and message UUID appear as a
persisted user entry in the bound transcript. That receipt is not proof of a reply
or completed work. An absent receipt does not prove rejection, so cbus never
blindly retries an uncertain native submission.

The daemon keeps unread mail and pending attempts across its own restart and a
normal Claude exit. Resume the same Claude session, then run `cbus connect` again
with its original channel and alias. A changed runtime or capability cannot take
over an unresolved attempt until a positive receipt is reconciled or the operator
explicitly abandons that exact attempt. Abandonment permits later mail; it does not
cancel a message already submitted or prove that it failed to arrive. A different
session cannot silently take over the connection.

## Migrating an existing Monitor peer

A fresh alias is the simplest way to try native receive without touching an old
inbox. To reuse a legacy alias, stop only that session's known cbus Monitor, inspect
and export any unread mail, and explicitly decide whether it may be discarded.
`cbus leave` removes the legacy inbox; it is not a lossless migration. Then connect
natively to that alias. Do not run a Monitor and native receive for the same inbox,
and do not automatically replay exported messages that may already have arrived.

The opt-in [Monitor stopgap](claude-monitor-stopgap.md) is for sessions still using
the legacy transport. Native receive does not require the GrowthBook snapshot or
telemetry-off workaround.

## Updating instructions

Use `cbus install-commands` and `cbus install-roles` to install reviewed command and
role files. The installer reports differing existing files as potentially edited;
review those differences before choosing `--force`. Do not overwrite customized
instructions silently. A CCS profile with a separate commands directory needs
`--path "$CLAUDE_CONFIG_DIR/commands"` for command installation unless that directory
already shares the installed commands. Refresh old commands and role prompts that
still say to arm or repeatedly re-arm a Monitor.

Repository policy can live in `AGENTS.md`, with `CLAUDE.md` containing `@AGENTS.md`
as a compatibility import. This shares project rules; it does not replace the
harness-specific join instructions or runtime permissions. See
[shared instructions](shared-instructions.md).
