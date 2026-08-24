# Install

The client is a single static Go binary — no runtime dependencies (python3 is no
longer needed).

**From a release.** Bootstrap once, then update in place. The `gh` CLI must be
installed; the repo slug is passed in (it is not baked into the script):

```sh
curl -fsSL <raw get.sh> | CBUS_REPO=owner/repo sh   # downloads cbus + installs the skills
cbus selfupdate                                     # thereafter, update in place
```

`get.sh` writes `cbus` to `~/.local/bin` and installs Claude commands, role prompts,
and the Codex `cbus-connect` skill.
`cbus selfupdate` downloads the latest release, verifies the download reports the tag
it fetched before swapping the running binary, and refreshes commands, roles and
Codex skills. Codex skill receipts permit upgrades of unchanged shipped content
while preserving local edits; `--force` is an explicit overwrite. Updaters from
before the Codex integration only refresh their known assets: run
`cbus install-codex-skills` once after that first upgrade.
`cbus selfupdate --check` reports without applying. Set `CBUS_UPDATE_CHECK=1` for an
opt-in once-a-day "update available" hint. Release binaries carry the repo slug baked
in, so `CBUS_REPO` is only needed for a dev build. Release engineering (tags, `make
release`, the quiesce sequence) lives in [RELEASE-CHECKLIST.md](RELEASE-CHECKLIST.md).

**From source.** Build and install as `cbus`, then install the embedded skill commands
and role prompts:

```sh
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" \
  -o ~/.local/bin/cbus ./cmd/cbus
cbus install-commands   # the slash-command skills -> ~/.claude/commands
cbus install-roles      # role prompts -> $CBUS_DIR/roles (the spawn-outside-repo fallback)
cbus install-codex-skills # $CODEX_HOME/skills, default ~/.codex/skills
```

The install verbs are content-guarded: an unchanged file is left alone, a locally-edited
one is skipped (with a reason) unless `--force`. The commands placed are:

| file | destination | purpose |
|---|---|---|
| `commands/bus-join.md` | `~/.claude/commands/bus-join.md` | join a channel |
| `commands/bus-branch.md` | `~/.claude/commands/bus-branch.md` | fork + auto-join both sides |
| `commands/bus-spawn.md` | `~/.claude/commands/bus-spawn.md` | open a fresh session, joined to a channel |
| `commands/bus-rename.md` | `~/.claude/commands/bus-rename.md` | rename a legacy peer's alias (native rename is unsupported) |
| `commands/bus-formation.md` | `~/.claude/commands/bus-formation.md` | save/apply/bootstrap a [formation](formations.md) |
| `commands/bus-codex.md` | `~/.claude/commands/bus-codex.md` | bring a Codex CLI session onto a channel |
| `commands/bus-layout.md` | `~/.claude/commands/bus-layout.md` | rearrange live peers into a tmux pane layout |
| `commands/save-formation.md` | `~/.claude/commands/save-formation.md` | checkpoint this session's channel, then triage the save |

Make sure `~/.local/bin` is on your `PATH`. `cbus --version` shows what's installed.

> **Legacy installers — retired.** `install.sh` (bash-client restore) and
> `install-cbus-go.sh` (the transitional side-by-side installer) were removed once
> releases and `cbus selfupdate` shipped, and the bash client itself was deleted at
> P3 homogenization (see [compat-deletion-plan](architecture/compat-deletion-plan.md)).
> All of it is recoverable from git history if ever needed; the supported path is
> releases plus `cbus selfupdate`.

> **Forking:** `cbus branch` forks natively (iTerm2 window/tab via osascript, tmux
> new-window, or — for `pane` — a split of the caller's own surface: `tmux
> split-window` when `$TMUX` is set, else an iTerm2 session split located by
> `$ITERM_SESSION_ID`) and relaunches through `ccs <profile>` when it detects a CCS
> config dir. The old `bin/cc-branch.sh` helper is no longer consulted.

## Codex CLI permission and daemon setup

Use `$cbus-connect` from an existing ordinary Codex CLI session. No special cbus
launcher is required. In v0.13.0 native Claude and Codex receive support
macOS/Linux. Codex release field checks used 0.155.1 on macOS and 0.154.0 on Linux;
the app-server queue surface remains experimental. See the
[Codex cheat sheet](../CHEATSHEET.md#codex-cli-quick-reference).
Windows retains its existing cbus functionality and explicitly refuses native
`connect`/`daemon` in this release. Desktop harness clients are v2.

For seamless use across channels, opt into trusted cbus setup once:

```sh
cbus install-codex-skills --with-permissions
```

This installs the skill and rules trusting all subcommands of bare `cbus` from
PATH and the installed absolute executable, including connect, spawn, updates
and administration. Start a new Codex CLI session, or restart/resume once to load
the rules. Future joins, status checks, sends and disconnects need no new rule.
General sandbox/approval settings and unrelated commands retain their policy.
Ordinary installation/selfupdate does not opt users in; an existing bus rule
survives selfupdate. Rules use the active Codex home even with a custom skill
`--path`, and skill `--force` does not overwrite edited permission rules.

For send-only permission instead, use the narrower helper scope:

```sh
cbus codex-permissions --binary /absolute/path/to/cbus           # preview
cbus codex-permissions --binary /absolute/path/to/cbus --install # deliberate opt-in
```

The default `send` scope allows only that literal executable's `send` prefix
outside the command sandbox. Use that path in replies. `--scope bus` previews
the complete trust policy used by the seamless setup. Edited rule files are
protected. See [Codex setup](codex.md).

After updating a running installation, `cbus daemon restart` loads the new binary
while retaining pending mail. The daemon is started on demand, not installed as
a login service. A stale version/protocol is refused instead of silently reused.
Native cross-machine subscriptions also require the matching relay's
`/tail/durable-v1` endpoint; deploying that relay is a separate release action.
