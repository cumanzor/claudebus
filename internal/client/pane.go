package client

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// pane is the fourth fork target: a split of the CALLER's own terminal surface,
// Claude-Code-teammate style. Precedence is tmux-first (matching CC): inside tmux
// ($TMUX) the split is a tmux pane targeted at $TMUX_PANE; otherwise inside iTerm2
// the split targets THIS session, located by the UUID in $ITERM_SESSION_ID — never
// "current window", which is the frontmost one and moves with the user's focus
// (the tab-in-wrong-window bug). Neither surface present => hard error: silently
// splitting whatever is frontmost would reintroduce exactly that bug, so refusing
// is the feature. CC reaches iTerm2 through the it2 CLI (Python API); we stay on
// osascript — no extra dependency, and AppleScript's `split ... command` verb takes
// the launch command atomically, so there is no separate injection step to race.

// iTermSessionUUID parses the session UUID out of $ITERM_SESSION_ID (documented
// format "w0t0p0:UUID"). Empty when unset or not in that format.
func iTermSessionUUID() string {
	v := os.Getenv("ITERM_SESSION_ID")
	if i := strings.IndexByte(v, ':'); i >= 0 && i+1 < len(v) {
		return v[i+1:]
	}
	return ""
}

// findSessionScript wraps body in the windows/tabs/sessions triple loop that
// locates the session whose id is uuid — s is the session, w its window. AppleScript
// has no flat session-by-id accessor, and `whose` clauses are unreliable across
// nested elements, so the loop is the dependable form. notFound runs after the loop
// when the uuid matched nothing (each caller picks: error out vs degrade).
func findSessionScript(uuid, body, notFound string) string {
	return "tell application \"iTerm2\"\n" +
		"  repeat with w in windows\n" +
		"    repeat with t in tabs of w\n" +
		"      repeat with s in sessions of t\n" +
		"        if id of s is " + appleScriptStr(uuid) + " then\n" +
		body +
		"          return \"ok\"\n" +
		"        end if\n" +
		"      end repeat\n" +
		"    end repeat\n" +
		"  end repeat\n" +
		notFound +
		"end tell"
}

// paneSplitScript splits the uuid session, running run in the new pane. Direction
// halves the visually longer axis: a terminal cell is ~2.2x taller than wide, so
// side-by-side (split vertically) only when columns > 2.2*rows — repeated splits
// then approximate a tiled grid instead of ever-thinner slices. A missing session
// is an AppleScript error (surfaced via runOsascriptErr), not a fallback.
func paneSplitScript(uuid, run string) string {
	r := appleScriptStr(run)
	body := "          if (columns of s) > (rows of s) * 2.2 then\n" +
		"            tell s to split vertically with default profile command " + r + "\n" +
		"          else\n" +
		"            tell s to split horizontally with default profile command " + r + "\n" +
		"          end if\n"
	return findSessionScript(uuid, body, "  error \"session \" & "+appleScriptStr(uuid)+" & \" not found in any iTerm2 window\"\n")
}

// tabInOwningWindowScript creates the tab in the window that OWNS the uuid session
// (`tell w`), fixing the frontmost-window bug. Unlike pane, a stale uuid degrades
// to the historical behavior (current window) rather than failing the fork — tab
// never depended on locating the caller.
func tabInOwningWindowScript(uuid, run string) string {
	r := appleScriptStr(run)
	body := "          tell w to create tab with default profile command " + r + "\n"
	return findSessionScript(uuid, body,
		"  tell current window to create tab with default profile command "+r+"\n")
}

// osaForkPane splits this iTerm2 session; Fork pre-checks the uuid is present.
func osaForkPane(run string) error {
	return runOsascriptErr(paneSplitScript(iTermSessionUUID(), run))
}

// osaForkTab places the tab in the caller's own window when the session is
// locatable, else falls back to the pre-fix current-window behavior.
func osaForkTab(run string) error {
	if uuid := iTermSessionUUID(); uuid != "" {
		return runOsascriptErr(tabInOwningWindowScript(uuid, run))
	}
	return runOsascript("tell application \"iTerm2\"\n  tell current window to create tab with default profile command " + appleScriptStr(run) + "\nend tell")
}

// runOsascriptErr runs script surfacing osascript's stderr in the error — the
// pane/tab scripts carry meaningful AppleScript `error` text (session not found)
// that .Run()'s bare exit status would swallow.
func runOsascriptErr(script string) error {
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// forkTmuxPane splits the caller's own tmux pane with the child, mirroring CC's
// teammate placement: targeted at $TMUX_PANE (focus-immune), -d so the child does
// not steal focus, new pane id captured via -P, and remain-on-exit failed so a
// crashed child stays inspectable while a clean exit closes its pane. First split
// gives the child 70% width (caller keeps a 30% leader column); beyond two panes
// the window is normalized to main-vertical with the caller re-held at 30%. The
// remain-on-exit/layout steps are best-effort — older tmux lacks the forms.
func forkTmuxPane(spec ForkSpec) error {
	caller := os.Getenv("TMUX_PANE")
	if caller == "" {
		return fmt.Errorf("$TMUX_PANE unset — cannot target the calling pane")
	}
	preCount := tmuxPaneCount(caller)
	out, err := exec.Command("tmux", tmuxSplitArgv(caller, terminalCommand(spec), preCount)...).Output()
	if err != nil && preCount == 1 {
		// -l 70% (percentage sizing) needs tmux >= 3.1; retry the plain split
		// before failing the fork — sizing is a nicety, the pane is the point.
		out, err = exec.Command("tmux", tmuxSplitArgv(caller, terminalCommand(spec), 0)...).Output()
	}
	if err != nil {
		return fmt.Errorf("tmux split-window: %v%s", err, cmdStderr(err))
	}
	// only decorate a shape-valid pane id (%N) — garbage from -P -F must not become
	// a -t that lands remain-on-exit on some other pane.
	if newPane := strings.TrimSpace(string(out)); strings.HasPrefix(newPane, "%") {
		_ = exec.Command("tmux", "set-option", "-p", "-t", newPane, "remain-on-exit", "failed").Run()
	}
	if tmuxPaneCount(caller) > 2 {
		_ = exec.Command("tmux", "select-layout", "-t", caller, "main-vertical").Run()
		_ = exec.Command("tmux", "resize-pane", "-t", caller, "-x", "30%").Run()
	}
	return nil
}

// tmuxSplitArgv is the pure argv builder for the split (testable without tmux):
// preCount is the pane count in the caller's window before the split; exactly 1
// means this is the first teammate, which takes 70% width beside the caller (any
// other value — including the 0 the retry path passes — builds the plain split).
func tmuxSplitArgv(caller, shellCmd string, preCount int) []string {
	argv := []string{"split-window", "-d", "-P", "-F", "#{pane_id}", "-t", caller}
	if preCount == 1 {
		argv = append(argv, "-h", "-l", "70%")
	}
	return append(argv, shellCmd)
}

// cmdStderr renders an *exec.ExitError's captured stderr as a ": …" suffix —
// .Output()'s bare %v is just "exit status 1", which buries tmux's actual message.
func cmdStderr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		if s := strings.TrimSpace(string(ee.Stderr)); s != "" {
			return ": " + s
		}
	}
	return ""
}

// tmuxPaneCount counts panes in the window owning pane (0 on any failure — callers
// treat that as "skip the layout niceties", never as fatal).
func tmuxPaneCount(pane string) int {
	out, err := exec.Command("tmux", "list-panes", "-t", pane, "-F", "x").Output()
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(out)))
}
