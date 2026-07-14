package client

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"claudebus/internal/core"
)

// HookExit runs the SessionEnd hook: it announces this session's departure by leaving
// its LOCAL registrations (a 'left' presence per local channel), reading the session
// id from the hook's stdin {session_id} JSON first, the environment second (the hook
// may not export CLAUDE_CODE_SESSION_ID). It leaves REMOTE markers UNTOUCHED — the
// relay has no leave endpoint, and a dead session's markers die via the ownerPid
// sweep; deleting them here would be a behavior change. Best-effort and SILENT: it
// never fails the session (the caller always exits 0), and it covers only graceful
// exits — a hard kill relies on the lazy-prune 'departed' backstop. bin/cbus:680-689.
func HookExit(stdin io.Reader) {
	sid := hookSessionID(stdin)
	if sid == "" {
		return
	}
	// bash runs cmd_leave in a subshell with CLAUDE_CODE_SESSION_ID exported (and a
	// "not joined" die contained to that subshell); Leave reads the id via SessionID().
	prev, had := os.LookupEnv("CLAUDE_CODE_SESSION_ID")
	_ = os.Setenv("CLAUDE_CODE_SESSION_ID", sid)
	_, _ = Leave("") // local registrations only; a "not joined" error is ignored
	if had {
		_ = os.Setenv("CLAUDE_CODE_SESSION_ID", prev)
	} else {
		_ = os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	}
}

// hookSessionID reads {"session_id":...} off the hook's stdin (tolerant of any
// non-JSON / missing-field payload), falling back to the environment.
func hookSessionID(stdin io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
	var m struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(b, &m) == nil && m.SessionID != "" {
		return m.SessionID
	}
	return os.Getenv("CLAUDE_CODE_SESSION_ID")
}

// ForkSpec is the terminal-agnostic description of a forked child session: what to
// run (Argv), the environment vars that MUST be replicated (Env — PATH and, under a
// CCS profile, CLAUDE_CONFIG_DIR), and the working directory (Dir). This env
// replication is the essential function cc-branch.sh existed for; a TerminalForker
// only places the spec in a new window/tab/pane. Modeling it as data makes it testable
// without a real terminal.
type ForkSpec struct {
	Target string            // window | tab | tmux
	Argv   []string          // launch command, e.g. ["ccs","personal","--resume",sid,"--fork-session",prompt]
	Env    map[string]string // env vars to replicate (PATH always; CLAUDE_CONFIG_DIR when set)
	Dir    string            // working directory to replicate
}

// TerminalForker places a forked child session in a new terminal surface. The real
// impl (OSAForker) drives iTerm2 via osascript and tmux via tmux; tests inject a fake
// that captures the spec and asserts env replication without launching anything.
type TerminalForker interface {
	Fork(ForkSpec) error
}

// Branch is the one-shot parent side of /bus-branch: derive the channel, join
// idempotently, and fork a bootstrapped child through forker. Returns the resolved
// (channel, alias). bin/cbus:803-822 + cc-branch.sh, natively — cc-branch.sh's env
// replication becomes the ForkSpec, and its mktemp/%q self-deleting-launcher shim is
// gone (Go builds the spec directly; the forker escapes natively).
func Branch(target, channel, model string, forker TerminalForker) (ch, alias string, err error) {
	switch target {
	case "window", "tab", "tmux":
	default:
		return "", "", fmt.Errorf("target must be window|tab|tmux")
	}
	// leading '-' is ValidName-legal but would be parsed as a flag by the child CLI
	// (an instant-close window) — reject the shape here.
	if model != "" && (!core.ValidName(model) || strings.HasPrefix(model, "-")) {
		return "", "", fmt.Errorf("bad model %q", model)
	}
	ch = channel
	if ch == "" {
		ch = branchChannelFromGit()
	}
	if !core.ValidName(ch) {
		return "", "", fmt.Errorf("bad channel %q", ch)
	}
	if _, _, jerr := Join(ch, ""); jerr != nil {
		return "", "", jerr
	}
	for _, reg := range ResolveSelf() { // requires this session's id — empty => "failed to join"
		if reg.Channel == ch {
			alias = reg.Alias
			break
		}
	}
	if alias == "" {
		return "", "", fmt.Errorf("failed to join %q", ch)
	}
	spec := ForkSpec{
		Target: target,
		Argv:   forkLaunchArgv(SessionID(), model, BootstrapPrompt(ch, alias)),
		Env:    forkReplicatedEnv(),
		Dir:    cwd(),
	}
	if err := forker.Fork(spec); err != nil {
		return "", "", err
	}
	return ch, alias, nil
}

// branchChannelFromGit derives the default channel from the git toplevel basename,
// keeping only [A-Za-z0-9._-] (bin/cbus:807), falling back to "global".
func branchChannelFromGit() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "global"
	}
	if c := keepNameChars(filepath.Base(strings.TrimSpace(string(out)))); c != "" {
		return c
	}
	return "global"
}

func keepNameChars(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// forkLaunchArgv builds the child launch command, replicating cc-branch.sh: relaunch
// through `ccs <profile>` when running under a CCS instance config dir (so the child
// gets the right profile/config/PATH), else a bare `claude`; always --resume <sid>
// --fork-session, an optional --model override, with the bootstrap prompt as the
// final positional turn when non-empty. The model token is passed through verbatim
// (the CLI validates the actual model set) — a bad value makes the child window die
// at launch, so callers pre-screen the token shape via core.ValidName.
func forkLaunchArgv(sid, model, prompt string) []string {
	var argv []string
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); strings.Contains(cfg, "/.ccs/instances/") {
		argv = []string{"ccs", filepath.Base(cfg), "--resume", sid, "--fork-session"}
	} else {
		argv = []string{"claude", "--resume", sid, "--fork-session"}
	}
	if model != "" {
		argv = append(argv, "--model", model)
	}
	if prompt != "" {
		argv = append(argv, prompt)
	}
	return argv
}

// forkReplicatedEnv is the env cc-branch.sh replicated verbatim: PATH always, plus
// CLAUDE_CONFIG_DIR when set (the CCS profile). The child inherits the rest from the
// terminal's fresh shell; these two are the ones a bare relaunch would get wrong.
func forkReplicatedEnv() map[string]string {
	env := map[string]string{"PATH": os.Getenv("PATH")}
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		env["CLAUDE_CONFIG_DIR"] = cfg
	}
	return env
}

// OSAForker is the real TerminalForker: iTerm2 (osascript) for window/tab, tmux for
// tmux. window/tab go through a self-deleting launcher SCRIPT (osaForkITerm); tmux
// runs a quoted one-liner (terminalCommand) — see each for the per-surface rationale.
type OSAForker struct{}

func (OSAForker) Fork(spec ForkSpec) error {
	switch spec.Target {
	case "window", "tab":
		return osaForkITerm(spec)
	case "tmux":
		if os.Getenv("TMUX") == "" {
			return fmt.Errorf("not inside a tmux session")
		}
		// tmux runs its command through /bin/sh, which DOES honor POSIX quoting, so a
		// quoted one-liner works here (unlike iTerm2 — see osaForkITerm).
		return exec.Command("tmux", "new-window", "-n", "cc-branch", terminalCommand(spec)).Run()
	default:
		return fmt.Errorf("unknown target %q", spec.Target)
	}
}

// osaForkITerm launches the child in a new iTerm2 window/tab through a self-deleting
// launcher script.
//
// CRITICAL — do NOT "simplify" this into a direct command string. iTerm2's AppleScript
// `command` parameter is tokenized by iTERM2 ITSELF and does NOT understand POSIX
// quoting; a quoted one-liner (e.g. `/bin/bash -c '…'`) is mis-tokenized and launches
// NOTHING — probe-verified live, twice, in the P2.5 go-port review. The launcher
// indirection is that tokenizer workaround, NOT quoting cruft: port-map §4.12's "the
// mktemp shim is quoting cruft" rationale was wrong. We write a 0700 self-deleting
// script holding the real (POSIX-quoted, /bin/sh-dialect) env exports + cd + exec, and
// hand iTerm2 only the BARE, whitespace-tokenized command `/bin/bash <tmpfile>`.
func osaForkITerm(spec ForkSpec) error {
	f, err := os.CreateTemp("", "cc-branch.*.sh")
	if err != nil {
		return err
	}
	path := f.Name()
	_, werr := io.WriteString(f, launcherScript(spec, path))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	run := iterm2Command(path) // bare `/bin/bash <tmpfile>` — tokenizer-proof
	switch spec.Target {
	case "window":
		return runOsascript(`tell application "iTerm2" to create window with default profile command ` + appleScriptStr(run))
	default: // tab
		return runOsascript("tell application \"iTerm2\"\n  tell current window to create tab with default profile command " + appleScriptStr(run) + "\nend tell")
	}
}

// iterm2Command is the bare command handed to iTerm2: `/bin/bash <tmpfile>` with NO
// quoting (mktemp paths carry no whitespace), so iTerm2's own tokenizer splits it into
// exactly two args. It exists so the tokenizer never sees POSIX quoting (see
// osaForkITerm).
func iterm2Command(scriptPath string) string { return "/bin/bash " + scriptPath }

// launcherScript is the self-deleting launcher iTerm2 runs. Inside the script ordinary
// /bin/sh quoting (shQuote) applies — the layer iTerm2's tokenizer bypasses. It
// replicates PATH/CLAUDE_CONFIG_DIR + cwd, rm's itself, then execs the child. Kept a
// pure function of (spec, scriptPath) so it is byte-for-byte unit-testable.
func launcherScript(spec ForkSpec, scriptPath string) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	for _, k := range sortedKeys(spec.Env) {
		b.WriteString("export " + k + "=" + shQuote(spec.Env[k]) + "\n")
	}
	b.WriteString("cd " + shQuote(spec.Dir) + "\n")
	b.WriteString("rm -f " + shQuote(scriptPath) + "\n")
	b.WriteString("exec")
	for _, a := range spec.Argv {
		b.WriteString(" " + shQuote(a))
	}
	b.WriteString("\n")
	return b.String()
}

func runOsascript(script string) error { return exec.Command("osascript", "-e", script).Run() }

// terminalCommand renders a ForkSpec into one /bin/sh command line — used for tmux,
// which execs through a POSIX shell. window/tab CANNOT use this (iTerm2 mis-tokenizes
// POSIX quoting — see osaForkITerm).
func terminalCommand(spec ForkSpec) string {
	return "/bin/bash -c " + shQuote(forkShellCommand(spec))
}

func forkShellCommand(spec ForkSpec) string {
	var b strings.Builder
	b.WriteString("cd " + shQuote(spec.Dir) + " && exec env")
	for _, k := range sortedKeys(spec.Env) { // deterministic order for testability
		b.WriteString(" " + k + "=" + shQuote(spec.Env[k]))
	}
	for _, a := range spec.Argv {
		b.WriteString(" " + shQuote(a))
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// shQuote wraps s in single quotes, POSIX-escaping embedded single quotes.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appleScriptStr renders s as an AppleScript double-quoted string literal.
func appleScriptStr(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
