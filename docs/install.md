# Install

The client is a single static Go binary — no runtime dependencies (python3 is no
longer needed).

**From a release.** Bootstrap once, then update in place. The `gh` CLI must be
installed; the repo slug is passed in (it is not baked into the script):

```sh
curl -fsSL <raw get.sh> | CBUS_REPO=owner/repo sh   # downloads cbus + installs the skills
cbus selfupdate                                     # thereafter, update in place
```

`get.sh` writes `cbus` to `~/.local/bin` and runs `install-commands` + `install-roles`.
`cbus selfupdate` downloads the latest release, verifies the download reports the tag
it fetched before swapping the running binary, and refreshes the commands and roles.
`cbus selfupdate --check` reports without applying. Set `CBUS_UPDATE_CHECK=1` for an
opt-in once-a-day "update available" hint. Release binaries carry the repo slug baked
in, so `CBUS_REPO` is only needed for a dev build. Release engineering (tags, `make
release`, the quiesce sequence) lives in [RELEASE-CHECKLIST.md](RELEASE-CHECKLIST.md).

**From source.** Build and install as `cbus`, then install the embedded skill commands
and role prompts:

```sh
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" \
  -o ~/.local/bin/cbus ./cmd/cbus
cbus install-commands   # the /bus-* skills -> ~/.claude/commands
cbus install-roles      # role prompts -> $CBUS_DIR/roles (the spawn-outside-repo fallback)
```

Both install verbs are sha-guarded: an unchanged file is left alone, a locally-edited
one is skipped (with a reason) unless `--force`. The commands placed are:

| file | destination | purpose |
|---|---|---|
| `commands/bus-join.md` | `~/.claude/commands/bus-join.md` | join a channel |
| `commands/bus-branch.md` | `~/.claude/commands/bus-branch.md` | fork + auto-join both sides |
| `commands/bus-spawn.md` | `~/.claude/commands/bus-spawn.md` | open a fresh session, joined to a channel |
| `commands/bus-rename.md` | `~/.claude/commands/bus-rename.md` | rename this session's alias |
| `commands/bus-formation.md` | `~/.claude/commands/bus-formation.md` | save/apply/bootstrap a [formation](formations.md) |

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
