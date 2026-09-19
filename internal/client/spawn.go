package client

import (
	"fmt"
	"strings"

	"claudebus/internal/core"
)

// SpawnPrompt renders a fresh session's native receive instructions.
func SpawnPrompt(address string) string {
	return "You are a fresh Claude Code session on the cbus message bus. " + claudeNativeReceivePrompt(address, "")
}

// SpawnPromptAliased keeps the launcher's chosen alias and birth record.
func SpawnPromptAliased(address, alias string) string {
	assignment := "pre-reserved"
	if IsRemote(address) {
		assignment = "pre-assigned"
	}
	return "You are a fresh Claude Code session on the cbus message bus. Your alias was " + assignment + " and your session title already matches it. " + claudeNativeReceivePrompt(address, alias)
}

// Spawn opens a FRESH session (blank transcript — no --resume/--fork-session) in a
// new terminal surface, prompted to join and arm the given channel on its own. The
// spawning side does NOT join — for a local channel it only RESERVES the child's
// alias (a placeholder the child's join reclaims; swept by the unarmed grace if the
// child never boots), so spawn still works when the caller is not on the channel
// (unlike Branch). The session title is the child's alias (--name at launch).
// A local channel derives like branch when omitted (own registration first, then
// git toplevel, then global); a remote address (channel@host) must be explicit —
// there `name` pre-assigns the child's relay alias, or the child picks its own and
// the title falls back to the address (the relay has no reservations).
// A non-empty `role` appends the committed role prompt (LoadRole) to the child's
// first turn and defaults name to the role and model to the file's MODEL: line.
// Roles are spawn-only by design: a fork inherits its parent's intent, so the CLI
// refuses --role on branch.
// Returns the resolved address and the fixed child alias ("" = remote self-pick).
func Spawn(target, address, model, name, role string, forker TerminalForker) (addr, childAlias string, err error) {
	return SpawnWithOptions(target, address, model, name, role, SpawnOptions{}, forker)
}

// SpawnOptions selects the harness independently of terminal placement. Empty
// Harness preserves historical Claude behavior for existing library callers.
type SpawnOptions struct {
	Harness string
	Profile string
}

func SpawnWithOptions(target, address, model, name, role string, opts SpawnOptions, forker TerminalForker) (addr, childAlias string, err error) {
	if opts.Harness == "" {
		opts.Harness = "claude"
	}
	if opts.Harness != "claude" && opts.Harness != "codex" {
		return "", "", fmt.Errorf("unsupported spawn harness %q; use claude or codex", opts.Harness)
	}
	if opts.Profile != "" && (opts.Harness != "codex" || !core.ValidName(opts.Profile) || strings.HasPrefix(opts.Profile, "-")) {
		return "", "", fmt.Errorf("--profile requires a valid Codex profile name and --harness codex")
	}
	// Resolve the selected runtime before reserving an alias.
	var codexLaunch codexSpawnContext
	if opts.Harness == "codex" {
		codexLaunch, err = resolveCodexSpawnContext()
		if err != nil {
			return "", "", err
		}
	}
	switch target {
	case "window", "tab", "tmux", "pane":
	default:
		return "", "", fmt.Errorf("target must be window|tab|tmux|pane")
	}
	var roleBody string
	if role != "" {
		var roleDefault string
		if roleBody, roleDefault, err = LoadRole(role); err != nil {
			return "", "", err
		}
		if model == "" && opts.Harness == "claude" {
			model = roleDefault
		}
		if name == "" {
			name = role
		}
	}
	// see Branch: reject the flag-shaped model token pre-fork (instant-close trap).
	if model != "" && (!core.ValidName(model) || strings.HasPrefix(model, "-")) {
		return "", "", fmt.Errorf("bad model %q", model)
	}
	// name IS the child's alias now, so it must pass the store rule the reservation
	// enforces. Checked here too, pre-fork, so the error names the flag.
	if why := core.StoreNameReason(name); name != "" && why != "" {
		return "", "", fmt.Errorf("bad name %q: %s", name, why)
	}
	addr = address
	if addr == "" {
		addr = spawnDefaultAddress()
	}
	if strings.Contains(addr, "/") {
		return "", "", fmt.Errorf("spawn takes a channel or channel@host, no alias — use --name to fix the child's alias")
	}
	childEnv := codexLaunch.env
	var unsetEnv []string
	if opts.Harness == "claude" {
		childEnv, err = forkReplicatedEnv()
		if err != nil {
			return "", "", err
		}
		unsetEnv = claudeLaunchUnset
	}
	var title, prompt string
	if IsRemote(addr) {
		at := strings.Index(addr, "@")
		ch, host := addr[:at], addr[at+1:]
		if !core.ValidName(ch) {
			return "", "", fmt.Errorf("bad channel %q", ch)
		}
		if !core.ValidName(host) {
			return "", "", fmt.Errorf("bad host %q", host)
		}
		if name != "" {
			childAlias, title, prompt = name, name, SpawnPromptAliased(addr, name)
		} else {
			title, prompt = addr, SpawnPrompt(addr) // alias unknowable — child picks
		}
	} else {
		if why := core.StoreNameReason(addr); why != "" {
			return "", "", fmt.Errorf("bad channel %q: %s", addr, why)
		}
		// spawn is always a fresh, blank-transcript session (cbus-m9l birth-record).
		if childAlias, err = ReserveAlias(addr, name, OriginFresh, model); err != nil {
			return "", "", err
		}
		title, prompt = childAlias, SpawnPromptAliased(addr, childAlias)
	}
	if opts.Harness == "codex" {
		prompt = CodexSpawnPrompt(codexLaunch.cbus, addr, childAlias)
	}
	if roleBody != "" {
		// role brief rides AFTER the join/arm instructions, matching how briefs
		// were dispatched manually; the file is designed to be pasted alone.
		prompt = prompt + "\n\n" + strings.TrimSpace(roleBody)
	}
	spec := ForkSpec{
		Target:   target,
		Argv:     freshLaunchArgv(model, title, prompt),
		Env:      childEnv,
		UnsetEnv: unsetEnv,
		Dir:      cwd(),
		Title:    title,
	}
	if opts.Harness == "codex" {
		spec.Argv = codexLaunch.argv(opts.Profile, model, prompt)
		spec.Env = codexLaunch.env
	}
	if _, err := forker.Fork(spec); err != nil {
		if childAlias != "" && !IsRemote(addr) {
			Unreserve(addr, childAlias)
		}
		return "", "", err
	}
	return addr, childAlias, nil
}

// spawnDefaultAddress prefers this session's own local channel (first registration,
// ResolveSelf order), falling back to branch's git/global derivation.
func spawnDefaultAddress() string {
	if regs := ResolveSelf(); len(regs) > 0 {
		return regs[0].Channel
	}
	return branchChannelFromGit()
}

// freshLaunchArgv builds a BLANK-session launch — forkLaunchArgv minus the
// --resume/--fork-session pair: `ccs <profile> [--model m] [--name n] <prompt>`
// under a CCS instance config dir, else `claude [--model m] [--name n] <prompt>`.
func freshLaunchArgv(model, name, prompt string) []string {
	argv := launchPrefix("")
	if model != "" {
		argv = append(argv, "--model", model)
	}
	if name != "" {
		argv = append(argv, "--name", name)
	}
	if prompt != "" {
		argv = append(argv, prompt)
	}
	return argv
}
