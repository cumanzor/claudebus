package client

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// pane is the fourth fork target: a split of a terminal surface, Claude-Code-
// teammate style. Precedence is tmux-first (matching CC): inside tmux ($TMUX) the
// split is a tmux pane; otherwise inside iTerm2 it is an osascript split of an
// iTerm2 session. The surface that gets split is spec.Anchor when set (formation
// apply chains splits across the panes it created); empty Anchor means the CALLER's
// own surface, located by $TMUX_PANE / the UUID in $ITERM_SESSION_ID — never
// "current window", which is the frontmost one and moves with the user's focus
// (the tab-in-wrong-window bug). Neither surface present => hard error: silently
// splitting whatever is frontmost would reintroduce exactly that bug, so refusing
// is the feature. CC reaches iTerm2 through the it2 CLI (Python API); we stay on
// osascript — no extra dependency, and AppleScript's `split ... command` verb takes
// the launch command atomically, so there is no separate injection step to race.
//
// spec.Split picks the divider: "right" = side-by-side (split vertically / -h),
// "down" = stacked (split horizontally / -v), ""/"auto" = halve the visually longer
// axis of the anchor (iTerm2) or today's caller-split + main-vertical normalize
// (tmux). An EXPLICIT direction suppresses the tmux normalize — a declared layout
// must not be reflowed away.

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
		"        end if\n" +
		"      end repeat\n" +
		"    end repeat\n" +
		"  end repeat\n" +
		notFound +
		"end tell"
}

// paneSplitScript splits the uuid session, running run in the new pane, and
// RETURNS the new session's id on stdout (formation apply feeds it back in as a
// later Anchor). dir "right"/"down" forces the divider; anything else halves the
// visually longer axis: a terminal cell is ~2.2x taller than wide, so side-by-side
// (split vertically) only when columns > 2.2*rows — repeated splits then
// approximate a tiled grid instead of ever-thinner slices. A missing session is an
// AppleScript error (surfaced via runOsascriptErr), not a fallback.
func paneSplitScript(uuid, run, dir string) string {
	r := appleScriptStr(run)
	var body string
	switch dir {
	case "right":
		body = "          tell s to set newS to (split vertically with default profile command " + r + ")\n"
	case "down":
		body = "          tell s to set newS to (split horizontally with default profile command " + r + ")\n"
	default:
		body = "          if (columns of s) > (rows of s) * 2.2 then\n" +
			"            tell s to set newS to (split vertically with default profile command " + r + ")\n" +
			"          else\n" +
			"            tell s to set newS to (split horizontally with default profile command " + r + ")\n" +
			"          end if\n"
	}
	body += "          return id of newS\n"
	return findSessionScript(uuid, body, "  error \"session \" & "+appleScriptStr(uuid)+" & \" not found in any iTerm2 window\"\n")
}

// paneGeometryScript emits one "uuid cols rows" line per candidate session found —
// the input to PaneAnchor's largest-area pick. Candidates are matched by exact id
// against the delimiter-wrapped list, so no uuid can substring-match another.
func paneGeometryScript(uuids []string) string {
	list := appleScriptStr("|" + strings.Join(uuids, "|") + "|")
	body := "          if " + list + " contains (\"|\" & (id of s) & \"|\") then\n" +
		"            set out to out & (id of s) & \" \" & (columns of s) & \" \" & (rows of s) & linefeed\n" +
		"          end if\n"
	// reuse the triple loop but without the per-session early return: collect all.
	return "tell application \"iTerm2\"\n" +
		"  set out to \"\"\n" +
		"  repeat with w in windows\n" +
		"    repeat with t in tabs of w\n" +
		"      repeat with s in sessions of t\n" +
		body +
		"      end repeat\n" +
		"    end repeat\n" +
		"  end repeat\n" +
		"  return out\n" +
		"end tell"
}

// tabInOwningWindowScript creates the tab in the window that OWNS the uuid session
// (`tell w`), fixing the frontmost-window bug. Unlike pane, a stale uuid degrades
// to the historical behavior (current window) rather than failing the fork — tab
// never depended on locating the caller.
func tabInOwningWindowScript(uuid, run string) string {
	r := appleScriptStr(run)
	body := "          tell w to create tab with default profile command " + r + "\n" +
		"          return \"ok\"\n"
	return findSessionScript(uuid, body,
		"  tell current window to create tab with default profile command "+r+"\n")
}

// osaForkPane splits spec.Anchor (or this session when empty; Fork pre-checks the
// env uuid is present) and returns the created session's uuid — "" if the split
// succeeded but the returned id fails the shape check (the pane exists; it just
// can't anchor later splits).
func osaForkPane(spec ForkSpec, run string) (string, error) {
	anchor := spec.Anchor
	if anchor == "" {
		anchor = iTermSessionUUID()
	}
	out, err := runOsascriptOut(paneSplitScript(anchor, run, spec.Split))
	if err != nil {
		return "", err
	}
	if id := strings.TrimSpace(out); validITermUUID(id) {
		return id, nil
	}
	return "", nil
}

// osaForkTab places the tab in the caller's own window when the session is
// locatable, else falls back to the pre-fix current-window behavior.
func osaForkTab(run string) error {
	if uuid := iTermSessionUUID(); uuid != "" {
		return runOsascriptErr(tabInOwningWindowScript(uuid, run))
	}
	return runOsascript("tell application \"iTerm2\"\n  tell current window to create tab with default profile command " + appleScriptStr(run) + "\nend tell")
}

// runOsascriptOut runs script with stdout and stderr SEPARATE — the pane scripts
// return data on stdout, and mixing streams would corrupt it with any warning.
func runOsascriptOut(script string) (string, error) {
	return runOsascriptOutCtx(context.Background(), script)
}

// runOsascriptOutCtx is runOsascriptOut under a deadline, for callers that must not
// hang on a wedged Apple Event (close's surface sweep).
func runOsascriptOutCtx(ctx context.Context, script string) (string, error) {
	cmd := boundedCmd(ctx, "osascript", "-e", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("osascript: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runOsascriptErr runs script surfacing osascript's stderr in the error — the
// tab script carries meaningful AppleScript `error` text that .Run()'s bare exit
// status would swallow.
func runOsascriptErr(script string) error {
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// validITermUUID is the shape gate on ids read back from AppleScript: hex/dash
// only, plausibly long. Garbage must not enter the anchor candidate set.
func validITermUUID(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'F', r >= 'a' && r <= 'f', r == '-':
		default:
			return false
		}
	}
	return true
}

// validTmuxPaneID: %N, tmux's stable pane handle shape.
func validTmuxPaneID(s string) bool {
	if len(s) < 2 || s[0] != '%' {
		return false
	}
	_, err := strconv.Atoi(s[1:])
	return err == nil
}

// PaneAnchor picks the surface the NEXT pane split should target: the largest-area
// pane among the caller's own surface and the already-created ids, ties going to
// the NEWEST created pane (so the caller/applier pane stays large — the user works
// there). Backend-aware exactly like Fork: $TMUX first. Returns "" when nothing is
// resolvable — Fork then falls back to splitting the caller, which never makes
// apply fail on a layout nicety.
func PaneAnchor(created []string) string {
	if os.Getenv("TMUX") != "" {
		return tmuxPickAnchor(created)
	}
	return itermPickAnchor(created)
}

func itermPickAnchor(created []string) string {
	self := iTermSessionUUID()
	if self == "" {
		return ""
	}
	candidates := append([]string{self}, created...)
	out, err := runOsascriptOut(paneGeometryScript(candidates))
	if err != nil {
		return ""
	}
	return pickLargest(candidates, parseGeometry(out))
}

func tmuxPickAnchor(created []string) string {
	self := os.Getenv("TMUX_PANE")
	if self == "" {
		return ""
	}
	out, err := exec.Command("tmux", "list-panes", "-t", self, "-F", "#{pane_id} #{pane_width} #{pane_height}").Output()
	if err != nil {
		return ""
	}
	return pickLargest(append([]string{self}, created...), parseGeometry(string(out)))
}

// parseGeometry reads "id cols rows" lines into id -> area.
func parseGeometry(out string) map[string]int {
	areas := make(map[string]int)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		c, cerr := strconv.Atoi(f[1])
		r, rerr := strconv.Atoi(f[2])
		if cerr == nil && rerr == nil && c > 0 && r > 0 {
			areas[f[0]] = c * r
		}
	}
	return areas
}

// pickLargest scans candidates IN ORDER (caller first, then created oldest→newest)
// taking >= so later entries win ties — the tie-break that keeps the caller big.
func pickLargest(candidates []string, areas map[string]int) string {
	best, bestArea := "", 0
	for _, id := range candidates {
		if a, ok := areas[id]; ok && a >= bestArea {
			best, bestArea = id, a
		}
	}
	return best
}

// forkTmuxPane splits a tmux pane with the child, mirroring CC's teammate
// placement: targeted at spec.Anchor (else $TMUX_PANE — focus-immune either way),
// -d so the child does not steal focus, new pane id captured via -P, and
// remain-on-exit failed so a crashed child stays inspectable while a clean exit
// closes its pane. On "auto" the first split gives the child 70% width and past
// two panes the window is normalized to main-vertical with the caller re-held at
// 30%; an EXPLICIT spec.Split ("right"/"down") maps to -h/-v and SKIPS the
// normalize — a declared layout must survive. The remain-on-exit/layout steps are
// best-effort — older tmux lacks the forms.
func forkTmuxPane(spec ForkSpec) (string, error) {
	caller := os.Getenv("TMUX_PANE")
	if caller == "" {
		return "", fmt.Errorf("$TMUX_PANE unset — cannot target the calling pane")
	}
	anchor := spec.Anchor
	if anchor == "" {
		anchor = caller
	}
	explicit := spec.Split == "right" || spec.Split == "down"
	preCount := tmuxPaneCount(anchor)
	out, err := exec.Command("tmux", tmuxSplitArgv(anchor, terminalCommand(spec), preCount, spec.Split)...).Output()
	if err != nil && !explicit && preCount == 1 {
		// -l 70% (percentage sizing) needs tmux >= 3.1; retry the plain split
		// before failing the fork — sizing is a nicety, the pane is the point.
		out, err = exec.Command("tmux", tmuxSplitArgv(anchor, terminalCommand(spec), 0, spec.Split)...).Output()
	}
	if err != nil {
		return "", fmt.Errorf("tmux split-window: %v%s", err, cmdStderr(err))
	}
	newPane := strings.TrimSpace(string(out))
	if !validTmuxPaneID(newPane) {
		// garbage from -P -F must not become a -t that lands options on some other
		// pane, nor an anchor for later splits. The split itself already succeeded.
		return "", nil
	}
	_ = exec.Command("tmux", "set-option", "-p", "-t", newPane, "remain-on-exit", "failed").Run()
	if !explicit && !spec.NoNormalize && tmuxPaneCount(caller) > 2 {
		_ = exec.Command("tmux", "select-layout", "-t", caller, "main-vertical").Run()
		_ = exec.Command("tmux", "resize-pane", "-t", caller, "-x", "30%").Run()
	}
	return newPane, nil
}

// tmuxSplitArgv is the pure argv builder for the split (testable without tmux):
// dir "right"/"down" forces -h/-v; otherwise preCount exactly 1 means this is the
// first teammate, which takes 70% width beside the caller (any other value —
// including the 0 the retry path passes — builds the plain split).
func tmuxSplitArgv(anchor, shellCmd string, preCount int, dir string) []string {
	argv := []string{"split-window", "-d", "-P", "-F", "#{pane_id}", "-t", anchor}
	switch dir {
	case "right":
		argv = append(argv, "-h")
	case "down":
		argv = append(argv, "-v")
	default:
		if preCount == 1 {
			argv = append(argv, "-h", "-l", "70%")
		}
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
