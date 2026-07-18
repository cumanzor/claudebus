package client

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ---- $ITERM_SESSION_ID parsing ---------------------------------------------------

// TestITermSessionUUID pins the parse of the documented "w0t0p0:UUID" form. The
// w/t/p coordinates are the part that goes STALE when a tab is moved to another
// window; the UUID after the colon is what survives, which is why the whole
// pane/tab fix keys on it. A value with no colon yields "" — callers treat that
// as "no locatable session" (pane errors, tab degrades to current window).
func TestITermSessionUUID(t *testing.T) {
	for _, tc := range []struct {
		env, want string
	}{
		{"w1t1p0:95B1EFE9-DDC0-4935-BD45-82DB4E6690AB", "95B1EFE9-DDC0-4935-BD45-82DB4E6690AB"},
		{"w0t0p0:U", "U"},
		{"", ""},                   // unset
		{"95B1EFE9-NO-PREFIX", ""}, // no colon => not the documented form
		{"w1t1p0:", ""},            // prefix with an empty uuid
		{":lead-colon", "lead-colon"},
		{"w1t1p0:a:b", "a:b"}, // splits on the FIRST colon only
	} {
		t.Setenv("ITERM_SESSION_ID", tc.env)
		if got := iTermSessionUUID(); got != tc.want {
			t.Errorf("iTermSessionUUID(%q) = %q, want %q", tc.env, got, tc.want)
		}
	}
}

// ---- AppleScript builders (byte-exact) -------------------------------------------

// TestFindSessionScriptByteExact pins the triple-loop session lookup, body and
// not-found block injected verbatim. Golden rather than substring-matched: this
// string is executed by osascript, where a stray newline or a lost `end repeat`
// is a runtime failure with no compile-time guard.
func TestFindSessionScriptByteExact(t *testing.T) {
	got := findSessionScript("UU-ID", "          BODY\n", "  NOTFOUND\n")
	want := "tell application \"iTerm2\"\n" +
		"  repeat with w in windows\n" +
		"    repeat with t in tabs of w\n" +
		"      repeat with s in sessions of t\n" +
		"        if id of s is \"UU-ID\" then\n" +
		"          BODY\n" +
		"        end if\n" +
		"      end repeat\n" +
		"    end repeat\n" +
		"  end repeat\n" +
		"  NOTFOUND\n" +
		"end tell"
	if got != want {
		t.Fatalf("findSessionScript:\n got  %q\n want %q", got, want)
	}
}

// TestPaneSplitScriptByteExact: the split runs against the located session `s`
// (never `current session`), picks its axis from the session's own geometry, and
// hands iTerm2 the launch command through the ATOMIC `split ... command` verb —
// there is no separate write-text injection step that a racing shell could beat.
func TestPaneSplitScriptByteExact(t *testing.T) {
	got := paneSplitScript("UU-ID", "/bin/bash /tmp/cc-branch.1.sh", "")
	want := "tell application \"iTerm2\"\n" +
		"  repeat with w in windows\n" +
		"    repeat with t in tabs of w\n" +
		"      repeat with s in sessions of t\n" +
		"        if id of s is \"UU-ID\" then\n" +
		"          if (columns of s) > (rows of s) * 2.2 then\n" +
		"            tell s to set newS to (split vertically with default profile command \"/bin/bash /tmp/cc-branch.1.sh\")\n" +
		"          else\n" +
		"            tell s to set newS to (split horizontally with default profile command \"/bin/bash /tmp/cc-branch.1.sh\")\n" +
		"          end if\n" +
		"          return id of newS\n" +
		"        end if\n" +
		"      end repeat\n" +
		"    end repeat\n" +
		"  end repeat\n" +
		"  error \"session \" & \"UU-ID\" & \" not found in any iTerm2 window\"\n" +
		"end tell"
	if got != want {
		t.Fatalf("paneSplitScript:\n got  %q\n want %q", got, want)
	}
}

// TestPaneSplitScriptNeverFallsBack: a pane whose session cannot be located must
// ERROR, not degrade. Silently splitting whatever is frontmost is precisely the
// wrong-window bug this target exists to avoid, so the absence of a fallback is a
// feature and is pinned as one.
func TestPaneSplitScriptNeverFallsBack(t *testing.T) {
	got := paneSplitScript("UU-ID", "RUN", "")
	if strings.Contains(got, "current window") || strings.Contains(got, "current session") {
		t.Errorf("pane must never fall back to the frontmost surface:\n%s", got)
	}
	if !strings.Contains(got, "error \"session \"") {
		t.Errorf("a missing session must raise an AppleScript error:\n%s", got)
	}
}

// TestTabInOwningWindowScriptByteExact is the tab fix. `tell w` — the window
// OWNING the caller's session — replaces the old unconditional `tell current
// window`, which placed the tab wherever the user's focus happened to be.
func TestTabInOwningWindowScriptByteExact(t *testing.T) {
	got := tabInOwningWindowScript("UU-ID", "/bin/bash /tmp/cc-branch.1.sh")
	want := "tell application \"iTerm2\"\n" +
		"  repeat with w in windows\n" +
		"    repeat with t in tabs of w\n" +
		"      repeat with s in sessions of t\n" +
		"        if id of s is \"UU-ID\" then\n" +
		"          tell w to create tab with default profile command \"/bin/bash /tmp/cc-branch.1.sh\"\n" +
		"          return \"ok\"\n" +
		"        end if\n" +
		"      end repeat\n" +
		"    end repeat\n" +
		"  end repeat\n" +
		"  tell current window to create tab with default profile command \"/bin/bash /tmp/cc-branch.1.sh\"\n" +
		"end tell"
	if got != want {
		t.Fatalf("tabInOwningWindowScript:\n got  %q\n want %q", got, want)
	}
}

// TestTabFallbackIsPositionedAfterTheLoops guards the ORDER the byte-exact test
// above would also catch, but states the invariant on its own terms: the
// owning-window placement lives INSIDE the loop and the current-window fallback
// only after the final `end repeat`. If a refactor ever hoists the fallback above
// the loop, tab silently reverts to frontmost-window placement — a behavioral
// regression with no crash and no failing build to announce it.
func TestTabFallbackIsPositionedAfterTheLoops(t *testing.T) {
	got := tabInOwningWindowScript("UU-ID", "RUN")
	tellW := strings.Index(got, "tell w to create tab")
	lastEnd := strings.LastIndex(got, "end repeat")
	fallback := strings.Index(got, "tell current window")
	if tellW < 0 || lastEnd < 0 || fallback < 0 {
		t.Fatalf("missing an expected clause:\n%s", got)
	}
	if !(tellW < lastEnd && lastEnd < fallback) {
		t.Errorf("expected owning-window placement inside the loop and the fallback after it; got offsets tellW=%d lastEnd=%d fallback=%d:\n%s",
			tellW, lastEnd, fallback, got)
	}
}

// TestPaneScriptsEscapeTheirArguments: both the uuid and the launch command reach
// AppleScript as quoted literals. A tmpfile path or uuid carrying a quote or a
// backslash must not be able to terminate the literal and inject script text.
func TestPaneScriptsEscapeTheirArguments(t *testing.T) {
	nasty := `a"b\c`
	for name, got := range map[string]string{
		"paneSplitScript":         paneSplitScript(nasty, nasty, ""),
		"tabInOwningWindowScript": tabInOwningWindowScript(nasty, nasty),
	} {
		if strings.Contains(got, `"a"b`) {
			t.Errorf("%s: unescaped quote breaks out of the literal:\n%s", name, got)
		}
		if !strings.Contains(got, `"a\"b\\c"`) {
			t.Errorf("%s: want the appleScriptStr-escaped form in:\n%s", name, got)
		}
	}
}

// ---- tmux argv builder -----------------------------------------------------------

// TestTmuxSplitArgv pins the split argv. -d keeps focus on the caller, -P -F
// '#{pane_id}' returns the new pane's id (the -t for remain-on-exit), and -t
// targets the CALLER's pane rather than a bare window, so the split is immune to
// which pane the user is looking at. preCount == 1 (and only 1) adds the 70%
// first-teammate sizing; the retry path passes 0 to get the plain split back.
func TestTmuxSplitArgv(t *testing.T) {
	const cmd = "/bin/bash -c 'cd /w && exec claude'"
	base := []string{"split-window", "-d", "-P", "-F", "#{pane_id}", "-t", "%3"}

	sized := tmuxSplitArgv("%3", cmd, 1, "")
	if want := append(append([]string{}, base...), "-h", "-l", "70%", cmd); !slices.Equal(sized, want) {
		t.Fatalf("preCount=1 argv:\n got  %v\n want %v", sized, want)
	}
	// 0 is the tmux<3.1 retry (percentage sizing unsupported); 2+ is a window that
	// already has teammates and gets normalized by select-layout instead.
	for _, preCount := range []int{0, 2, 3, 9} {
		got := tmuxSplitArgv("%3", cmd, preCount, "")
		if want := append(append([]string{}, base...), cmd); !slices.Equal(got, want) {
			t.Fatalf("preCount=%d argv:\n got  %v\n want %v", preCount, got, want)
		}
	}
}

// TestTmuxSplitArgvCommandIsLast: the shell command must stay the final element.
// tmux treats the first non-flag operand as the command, so an argv that grew a
// trailing flag would run the wrong thing (or nothing) with no error from Go.
func TestTmuxSplitArgvCommandIsLast(t *testing.T) {
	const cmd = "/bin/bash -c 'exec claude'"
	for _, preCount := range []int{0, 1, 2} {
		got := tmuxSplitArgv("%7", cmd, preCount, "")
		if got[len(got)-1] != cmd {
			t.Errorf("preCount=%d: command must be the last operand, got %v", preCount, got)
		}
		if i := slices.Index(got, "-t"); i < 0 || got[i+1] != "%7" {
			t.Errorf("preCount=%d: -t must carry the caller's pane, got %v", preCount, got)
		}
	}
}

// TestTmuxSplitArgvSharesTheShellBuilder: the tmux leg goes through
// terminalCommand (POSIX-quoted, /bin/sh dialect) — the OPPOSITE of the iTerm2
// leg, which must hand over a bare, unquoted command because iTerm2 tokenizes it
// itself. Pinning that they differ keeps a future "unify these" refactor honest.
func TestTmuxSplitArgvSharesTheShellBuilder(t *testing.T) {
	spec := ForkSpec{
		Target: "pane",
		Argv:   []string{"claude", "hi 'there'"},
		Env:    map[string]string{"PATH": "/a b"},
		Dir:    "/work dir",
	}
	argv := tmuxSplitArgv("%1", terminalCommand(spec), 2, "")
	got := argv[len(argv)-1]
	if !strings.HasPrefix(got, "/bin/bash -c '") {
		t.Fatalf("the tmux leg must be a POSIX-quoted -c one-liner, got %s", got)
	}
	if !strings.Contains(got, `'\''there'\''`) {
		t.Errorf("embedded quotes must survive shQuote round-tripping: %s", got)
	}
	// the iTerm2 leg is deliberately the opposite shape: bare and unquoted, because
	// iTerm2 tokenizes the command itself and mis-parses POSIX quoting.
	if strings.HasPrefix(iterm2Command("/tmp/x.sh"), "/bin/bash -c") {
		t.Error("the iTerm2 leg must stay a BARE two-token command, not a -c one-liner")
	}
}

// TestTmuxPaneCountFailsToZero: the count feeds only layout niceties, so any
// failure (no tmux binary, no server, bogus target) must read as 0 and skip the
// decoration rather than propagate. Runs identically with or without tmux
// installed — both paths are failures that must produce 0.
func TestTmuxPaneCountFailsToZero(t *testing.T) {
	if got := tmuxPaneCount("%no-such-pane-999999"); got != 0 {
		t.Errorf("tmuxPaneCount on an unresolvable target = %d, want 0", got)
	}
}

// ---- stderr surfacing ------------------------------------------------------------

// TestCmdStderr: .Output()'s bare error is "exit status 1", which buries tmux's
// actual complaint. cmdStderr appends the captured stderr when there is one and
// stays empty for every other error shape (including nil).
func TestCmdStderr(t *testing.T) {
	if got := cmdStderr(&exec.ExitError{Stderr: []byte("  can't find pane: %9\n")}); got != ": can't find pane: %9" {
		t.Errorf("captured stderr = %q", got)
	}
	if got := cmdStderr(&exec.ExitError{Stderr: []byte("   \n")}); got != "" {
		t.Errorf("whitespace-only stderr = %q, want empty", got)
	}
	if got := cmdStderr(&exec.ExitError{}); got != "" {
		t.Errorf("no stderr = %q, want empty", got)
	}
	if got := cmdStderr(exec.ErrNotFound); got != "" {
		t.Errorf("non-ExitError = %q, want empty", got)
	}
	if got := cmdStderr(nil); got != "" {
		t.Errorf("nil error = %q, want empty", got)
	}
}

// ---- target acceptance + passthrough ---------------------------------------------

// TestBranchAndSpawnAcceptPane: pane reaches the forker as a target on both verbs
// (the CLI threads it, the forker dispatches on it).
func TestBranchAndSpawnAcceptPane(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-pane-target")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	f := &fakeForker{}
	if _, _, _, err := Branch("pane", "panech", "", "", f); err != nil {
		t.Fatalf("Branch pane: %v", err)
	}
	if !f.called || f.spec.Target != "pane" {
		t.Fatalf("branch spec.Target = %q called=%v", f.spec.Target, f.called)
	}
	g := &fakeForker{}
	if _, _, err := Spawn("pane", "panech2", "", "", "", g); err != nil {
		t.Fatalf("Spawn pane: %v", err)
	}
	if !g.called || g.spec.Target != "pane" {
		t.Fatalf("spawn spec.Target = %q called=%v", g.spec.Target, g.called)
	}
}

// TestTargetSetGrewWithoutOpeningUp: adding pane must not weaken validation. An
// unknown target is still refused before any join/reserve/fork, and the error
// text advertises the new token.
func TestTargetSetGrewWithoutOpeningUp(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-bad-target")
	for _, bad := range []string{"popup", "panel", "Pane", "pane "} {
		f := &fakeForker{}
		_, _, _, berr := Branch(bad, "ch", "", "", f)
		if berr == nil || !strings.Contains(berr.Error(), "window|tab|tmux|pane") {
			t.Errorf("Branch(%q) err = %v, want the target list including pane", bad, berr)
		}
		_, _, serr := Spawn(bad, "ch", "", "", "", f)
		if serr == nil || !strings.Contains(serr.Error(), "window|tab|tmux|pane") {
			t.Errorf("Spawn(%q) err = %v, want the target list including pane", bad, serr)
		}
		if f.called {
			t.Errorf("Spawn/Branch(%q) must not reach the forker", bad)
		}
	}
}

// TestLaunchTargetDefaultUnchanged: pane must not become the default by side
// effect. A formation peer with no recorded target still launches as a tab.
func TestLaunchTargetDefaultUnchanged(t *testing.T) {
	if got := launchTarget(""); got != "tab" {
		t.Errorf("launchTarget(\"\") = %q — the default must stay tab", got)
	}
	if got := launchTarget("pane"); got != "pane" {
		t.Errorf("launchTarget(\"pane\") = %q", got)
	}
}

// TestValidatePeerAcceptsPaneTarget: a saved formation may record target: pane,
// and still rejects anything outside the set.
func TestValidatePeerAcceptsPaneTarget(t *testing.T) {
	p := FormationPeer{Alias: "coder", Target: "pane"}
	if err := p.validate(); err != nil {
		t.Errorf("target pane must validate: %v", err)
	}
	p.Target = "popup"
	if err := p.validate(); err == nil || !strings.Contains(err.Error(), "pane") {
		t.Errorf("bad target err = %v, want the set including pane", err)
	}
}

// ---- no-surface refusal + reservation hygiene ------------------------------------

// TestOSAForkPaneRefusesWithoutASurface: with neither $TMUX nor $ITERM_SESSION_ID,
// pane is a hard error naming both surfaces and the usable alternatives. It must
// NOT quietly split whatever is frontmost — that is the bug, not the fallback.
func TestOSAForkPaneRefusesWithoutASurface(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("ITERM_SESSION_ID", "")
	_, err := OSAForker{}.Fork(ForkSpec{Target: "pane", Argv: []string{"claude"}, Dir: "/tmp"})
	if err == nil {
		t.Fatal("pane with no terminal surface must error")
	}
	for _, want := range []string{"tmux", "iTerm2", "window|tab"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

type errForker struct{ called bool }

func (f *errForker) Fork(ForkSpec) (string, error) {
	f.called = true
	return "", errors.New("pane needs tmux or iTerm2")
}

// TestFailedPaneForkLeavesNoReservation: the no-surface refusal surfaces through
// Fork, so it rides the existing unreserve-on-fork-error path. A refused pane must
// not leave a phantom peer squatting the alias — the next spawn would be pushed to
// fork-1 and `cbus list` would show a peer that never existed.
func TestFailedPaneForkLeavesNoReservation(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-fork-fail")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	f := &errForker{}
	if _, _, err := Spawn("pane", "failch", "", "kid", "", f); err == nil {
		t.Fatal("a failing forker must surface its error")
	}
	if !f.called {
		t.Fatal("the forker should have been reached")
	}
	if _, ok := ReadPeerMeta(filepath.Join(CBUSDir(), "failch", "kid", "meta.json")); ok {
		t.Error("a failed pane fork must unreserve the child alias")
	}
	// the alias is free again: the next spawn takes it rather than sliding to fork-1
	g := &fakeForker{}
	_, alias, err := Spawn("pane", "failch", "", "kid", "", g)
	if err != nil {
		t.Fatal(err)
	}
	if alias != "kid" {
		t.Errorf("alias after a failed fork = %q, want kid (reservation not released)", alias)
	}
}

// ---- declared split direction ----------------------------------------------------

// TestPaneSplitScriptHonorsDeclaredDirection: a declared direction REPLACES the 2.2
// heuristic rather than seeding it — the whole point of writing one in the envelope
// is that the layout stops depending on the surface's current proportions.
func TestPaneSplitScriptHonorsDeclaredDirection(t *testing.T) {
	for dir, want := range map[string]string{
		"right": "split vertically",   // AppleScript: a vertical divider = side by side
		"down":  "split horizontally", // a horizontal divider = stacked
	} {
		got := paneSplitScript("UU-ID", "RUN", dir)
		if !strings.Contains(got, want) {
			t.Errorf("dir %q must use %q:\n%s", dir, want, got)
		}
		if strings.Contains(got, "columns of s") {
			t.Errorf("dir %q must not consult the auto heuristic:\n%s", dir, got)
		}
		// the id is still read back — a directed split anchors later splits too
		if !strings.Contains(got, "return id of newS") {
			t.Errorf("dir %q must still return the created id:\n%s", dir, got)
		}
	}
	// auto keeps both branches AND the heuristic that chooses between them
	auto := paneSplitScript("UU-ID", "RUN", "")
	for _, want := range []string{"columns of s", "split vertically", "split horizontally"} {
		if !strings.Contains(auto, want) {
			t.Errorf("auto must keep %q:\n%s", want, auto)
		}
	}
}

// TestTmuxSplitArgvDirection: right/down map to tmux's -h/-v, and a DECLARED
// direction also drops the first-teammate 70% sizing — a file that states its layout
// gets that layout, not a 70/30 the heuristic would have imposed. Pinned because it
// is a silent behavior difference between `split: right` and auto's first split.
func TestTmuxSplitArgvDirection(t *testing.T) {
	const cmd = "CMD"
	for dir, flag := range map[string]string{"right": "-h", "down": "-v"} {
		got := tmuxSplitArgv("%3", cmd, 1, dir) // preCount 1 = would be the 70% case
		if !slices.Contains(got, flag) {
			t.Errorf("dir %q must pass %s, got %v", dir, flag, got)
		}
		if slices.Contains(got, "-l") {
			t.Errorf("a declared direction must not also force 70%% sizing, got %v", got)
		}
		if got[len(got)-1] != cmd {
			t.Errorf("command must stay the last operand, got %v", got)
		}
	}
	// "down" must not smuggle in -h alongside -v
	if got := tmuxSplitArgv("%3", cmd, 1, "down"); slices.Contains(got, "-h") {
		t.Errorf("down must not pass -h, got %v", got)
	}
}

// ---- anchor selection ------------------------------------------------------------

// TestPickLargestPolicy pins the whole anchor rule in one table: biggest area wins,
// and ties go to the NEWEST created pane so the applier — always first in the
// candidate list — keeps its size. A regression here degrades silently into
// "everything splits the applier", which is the layout this feature exists to fix.
func TestPickLargestPolicy(t *testing.T) {
	const self = "%0"
	for name, tc := range map[string]struct {
		created []string
		areas   map[string]int
		want    string
	}{
		"applier strictly largest wins": {
			[]string{"%1", "%2"}, map[string]int{self: 1000, "%1": 500, "%2": 400}, self,
		},
		"largest teammate wins": {
			[]string{"%1", "%2"}, map[string]int{self: 100, "%1": 900, "%2": 100}, "%1",
		},
		"all equal -> newest teammate, applier last": {
			[]string{"%1", "%2"}, map[string]int{self: 500, "%1": 500, "%2": 500}, "%2",
		},
		"tie between teammates -> newest": {
			[]string{"%1", "%2"}, map[string]int{self: 100, "%1": 900, "%2": 900}, "%2",
		},
		"tie between applier and teammate -> teammate": {
			[]string{"%1"}, map[string]int{self: 900, "%1": 900}, "%1",
		},
		"ids with no geometry are skipped": {
			[]string{"%1", "%2"}, map[string]int{self: 100, "%2": 50}, self,
		},
		"no geometry at all -> no anchor": {
			[]string{"%1"}, map[string]int{}, "",
		},
		"applier alone": {
			nil, map[string]int{self: 42}, self,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := pickLargest(append([]string{self}, tc.created...), tc.areas); got != tc.want {
				t.Errorf("pickLargest = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseGeometry: "id cols rows" lines become areas, and anything malformed is
// DROPPED rather than defaulted — a zero-area candidate would otherwise win a tie
// against a pane whose geometry simply failed to parse.
func TestParseGeometry(t *testing.T) {
	got := parseGeometry("%0 80 24\n%1 40 24\nnot-a-line\n%2 x 24\n%3 80\n%4 0 24\n%5 -5 10\n")
	want := map[string]int{"%0": 1920, "%1": 960}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want exactly %v", got, want)
	}
	for id, area := range want {
		if got[id] != area {
			t.Errorf("%s = %d, want %d", id, got[id], area)
		}
	}
}

// TestValidatePeerSplitDirection: the envelope accepts the three documented values
// and nothing else. Empty is auto by design — the same convention target and onStale
// use, where an unset field and an explicitly blank one mean the same thing — so it
// is pinned as ACCEPTED rather than treated as a missing-value error.
func TestValidatePeerSplitDirection(t *testing.T) {
	for _, ok := range []string{"", "auto", "right", "down"} {
		p := FormationPeer{Alias: "coder", Target: "pane", Split: ok}
		if err := p.validate(); err != nil {
			t.Errorf("split %q must validate: %v", ok, err)
		}
	}
	// plausible-but-wrong spellings are the ones a human actually types
	for _, bad := range []string{"left", "up", "horizontal", "vertical", "h", "v", "RIGHT"} {
		p := FormationPeer{Alias: "coder", Target: "pane", Split: bad}
		if err := p.validate(); err == nil {
			t.Errorf("split %q must be refused", bad)
		} else if !strings.Contains(err.Error(), "split") {
			t.Errorf("split %q: error should name the field, got %v", bad, err)
		}
	}
}
