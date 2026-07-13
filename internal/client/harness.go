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
func Branch(target, channel string, forker TerminalForker) (ch, alias string, err error) {
	switch target {
	case "window", "tab", "tmux":
	default:
		return "", "", fmt.Errorf("target must be window|tab|tmux")
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
		Argv:   forkLaunchArgv(SessionID(), BootstrapPrompt(ch, alias)),
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
// --fork-session, with the bootstrap prompt as the final positional turn when non-empty.
func forkLaunchArgv(sid, prompt string) []string {
	var argv []string
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); strings.Contains(cfg, "/.ccs/instances/") {
		argv = []string{"ccs", filepath.Base(cfg), "--resume", sid, "--fork-session"}
	} else {
		argv = []string{"claude", "--resume", sid, "--fork-session"}
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
// tmux. It builds the child command string with native Go quoting — the bash shim's
// mktemp self-deleting launcher (there only to dodge nested osascript/shell quoting)
// is gone.
type OSAForker struct{}

func (OSAForker) Fork(spec ForkSpec) error {
	run := terminalCommand(spec)
	switch spec.Target {
	case "window":
		return runOsascript(`tell application "iTerm2" to create window with default profile command ` + appleScriptStr(run))
	case "tab":
		return runOsascript("tell application \"iTerm2\"\n  tell current window to create tab with default profile command " + appleScriptStr(run) + "\nend tell")
	case "tmux":
		if os.Getenv("TMUX") == "" {
			return fmt.Errorf("not inside a tmux session")
		}
		return exec.Command("tmux", "new-window", "-n", "cc-branch", run).Run()
	default:
		return fmt.Errorf("unknown target %q", spec.Target)
	}
}

func runOsascript(script string) error { return exec.Command("osascript", "-e", script).Run() }

// terminalCommand renders a ForkSpec into a single shell command the terminal runs:
// `/bin/bash -c 'cd <dir> && exec env <K=V…> <argv…>'`, everything POSIX-quoted. This
// is the direct, temp-file-free replacement for cc-branch.sh's launcher script.
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
