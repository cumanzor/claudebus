package client

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// renderTree writes a tree back out in fully-parenthesised canonical form, so a
// parse assertion reads as the shape it means instead of a nested struct literal.
func renderTree(n *LayoutNode) string {
	if n.Leaf() {
		if n.Size != "" {
			return n.Alias + ":" + n.Size
		}
		return n.Alias
	}
	sep := " | "
	if n.Rows {
		sep = " / "
	}
	parts := make([]string, len(n.Kids))
	for i, k := range n.Kids {
		parts[i] = renderTree(k)
	}
	s := "(" + strings.Join(parts, sep) + ")"
	if n.Size != "" {
		s += ":" + n.Size
	}
	return s
}

// ---- spec parsing ----------------------------------------------------------------

// TestParseLayoutShapes pins the grammar, precedence first: '/' binds tighter than
// '|', so `a | b / c` is one column beside a stack and NOT a stack of `a|b` over c.
// Every other row is a form a user is likely to type without thinking about it.
func TestParseLayoutShapes(t *testing.T) {
	for _, tc := range []struct{ spec, want string }{
		{"orchestrator | (coder / reviewer)", "(orchestrator | (coder / reviewer))"},
		{"a | b / c", "(a | (b / c))"}, // '/' binds tighter
		{"a / b | c", "((a / b) | c)"}, // ...on either side
		{"a | b | c", "(a | b | c)"},   // one flat level, not nested pairs
		{"a / b / c", "(a / b / c)"},
		{"(a)", "a"},                             // a one-child group collapses to the child
		{"((a))", "a"},                           // ...repeatedly
		{"  a  |  b  ", "(a | b)"},               // whitespace around separators
		{"a:30% | b", "(a:30% | b)"},             // percentage size
		{"a:80 | b", "(a:80 | b)"},               // cell-count size
		{"(a / b):70% | c", "((a / b):70% | c)"}, // a group carries a size too
		{"a-1.x_y | b", "(a-1.x_y | b)"},         // the full alias charset
	} {
		n, err := ParseLayout(tc.spec)
		if err != nil {
			t.Errorf("ParseLayout(%q) errored: %v", tc.spec, err)
			continue
		}
		if got := renderTree(n); got != tc.want {
			t.Errorf("ParseLayout(%q) = %s, want %s", tc.spec, got, tc.want)
		}
	}
}

// TestParseLayoutErrors: a layout is typed by hand, so every malformed spec must be
// REFUSED rather than silently reinterpreted. The two that matter most are the last
// pair — a duplicated alias (a peer has one pane, so the spec is unrealisable) and a
// space before ':' (a typo that must not be read as a size the user did not write).
func TestParseLayoutErrors(t *testing.T) {
	for _, tc := range []struct{ spec, want string }{
		{"", "ends where an alias"},
		{"   ", "ends where an alias"},
		{"a | ", "ends where an alias"},
		{"a | | b", "unexpected"},
		{"| a", "unexpected"},
		{"(a | b", "unclosed"},
		{"a | b)", "unexpected"},
		{"()", "unexpected"},
		{"a@b", "unexpected"},
		{"a | a", "twice"},
		{"a | (b / a)", "twice"},
		{"a: | b", "must be a number"},
		{"a:% | b", "must be a number"},
		{"a :30% | b", "unexpected"},
	} {
		n, err := ParseLayout(tc.spec)
		if err == nil {
			t.Errorf("ParseLayout(%q) accepted, got %s — want error %q", tc.spec, renderTree(n), tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseLayout(%q) error = %q, want it to mention %q", tc.spec, err, tc.want)
		}
	}
}

// TestLayoutAliasesOrder: the order is left→right, top→bottom because it is what the
// caller resolves peers in, so the first failure names the first alias the user wrote
// rather than whichever one the map happened to yield.
func TestLayoutAliasesOrder(t *testing.T) {
	n, err := ParseLayout("orchestrator | (coder / reviewer) | tester")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"orchestrator", "coder", "reviewer", "tester"}
	if got := LayoutAliases(n); !slices.Equal(got, want) {
		t.Errorf("LayoutAliases = %v, want %v", got, want)
	}
}

// ---- planning --------------------------------------------------------------------

// planOf parses and plans in one step, rendering each op as the command line it
// becomes — the same text --dry-run prints, so a golden here is a golden on what the
// user is shown.
func planOf(t *testing.T, spec string, panes map[string]string) []string {
	t.Helper()
	n, err := ParseLayout(spec)
	if err != nil {
		t.Fatalf("ParseLayout(%q): %v", spec, err)
	}
	ops, err := PlanLayout(n, panes)
	if err != nil {
		t.Fatalf("PlanLayout(%q): %v", spec, err)
	}
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = "tmux " + strings.Join(op.Argv, " ")
	}
	return out
}

var threePanes = map[string]string{"orchestrator": "%1", "coder": "%2", "reviewer": "%3"}

// TestPlanLayoutMarquee is the worked example from the usage text: two columns, the
// right one stacked. Two joins and nothing else — no create, no kill, no select.
func TestPlanLayoutMarquee(t *testing.T) {
	want := []string{
		"tmux join-pane -d -h -s %2 -t %1",
		"tmux join-pane -d -v -s %3 -t %2",
	}
	if got := planOf(t, "orchestrator | (coder / reviewer)", threePanes); !slices.Equal(got, want) {
		t.Errorf("plan =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestPlanLayoutBuildsLevelBeforeDescending is the whole placement invariant, and
// ORDER is the entire assertion: the op SET is identical either way.
//
// For `(a / b) | (c / d)` the columns must be joined while a and b are still one
// pane. Descending first would emit `-v b -t %1` before `-h c -t %1`, and by then
// %1 is only the top half of the left column — c would land inside the left column's
// top row, one level too deep, and the window would come out three-deep instead of
// two columns of two.
func TestPlanLayoutBuildsLevelBeforeDescending(t *testing.T) {
	panes := map[string]string{"a": "%1", "b": "%2", "c": "%3", "d": "%4"}
	want := []string{
		"tmux join-pane -d -h -s %3 -t %1", // columns first, both sides still single panes
		"tmux join-pane -d -v -s %2 -t %1", // then inside the left column
		"tmux join-pane -d -v -s %4 -t %3", // then inside the right
	}
	got := planOf(t, "(a / b) | (c / d)", panes)
	if !slices.Equal(got, want) {
		t.Errorf("plan =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestPlanLayoutChainsSiblings: three columns join sibling-to-previous-sibling, not
// all against the first. Joining c against a would place it between a and b, putting
// the panes in an order the user did not write.
func TestPlanLayoutChainsSiblings(t *testing.T) {
	panes := map[string]string{"a": "%1", "b": "%2", "c": "%3"}
	want := []string{
		"tmux join-pane -d -h -s %2 -t %1",
		"tmux join-pane -d -h -s %3 -t %2",
	}
	if got := planOf(t, "a | b | c", panes); !slices.Equal(got, want) {
		t.Errorf("plan =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestPlanLayoutSizingRunsLastAndFollowsParentAxis: every resize lands after every
// join, because a resize against a region that is still growing sizes the wrong
// geometry. The axis comes from the PARENT — a child of a columns node is sized
// across (-x), a child of a rows node down (-y) — so the two resizes here differ
// despite both being written the same way in the spec.
func TestPlanLayoutSizingRunsLastAndFollowsParentAxis(t *testing.T) {
	panes := map[string]string{"a": "%1", "b": "%2", "c": "%3"}
	want := []string{
		"tmux join-pane -d -h -s %2 -t %1",
		"tmux join-pane -d -v -s %3 -t %2",
		"tmux resize-pane -t %1 -x 30%", // a: child of the columns root
		"tmux resize-pane -t %2 -y 40%", // b: child of the rows group
	}
	if got := planOf(t, "a:30% | (b:40% / c)", panes); !slices.Equal(got, want) {
		t.Errorf("plan =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestPlanLayoutSizeOpsAreBestEffort: an older tmux without percentage sizing must
// not fail an otherwise-good arrange — the panes are the point, the width is a
// nicety. The joins must NOT carry the same flag, or a failed join would be skipped
// past and leave the tree silently wrong.
func TestPlanLayoutSizeOpsAreBestEffort(t *testing.T) {
	n, err := ParseLayout("a:30% | b")
	if err != nil {
		t.Fatal(err)
	}
	ops, err := PlanLayout(n, map[string]string{"a": "%1", "b": "%2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		wantBestEffort := op.Argv[0] == "resize-pane"
		if op.BestEffort != wantBestEffort {
			t.Errorf("op %v BestEffort = %v, want %v", op.Argv, op.BestEffort, wantBestEffort)
		}
	}
}

// TestPlanLayoutRootSizeDropped: the root has no sibling to take space from, so
// `(a | b):50%` is unenforceable. Dropping it beats erroring — the spec is harmless
// and a user who wrote it still gets the layout they asked for.
func TestPlanLayoutRootSizeDropped(t *testing.T) {
	got := planOf(t, "(a | b):50%", map[string]string{"a": "%1", "b": "%2"})
	want := []string{"tmux join-pane -d -h -s %2 -t %1"}
	if !slices.Equal(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestPlanLayoutMissingPane: an unresolved alias must stop the plan by NAME. Planning
// around it would arrange the peers that did resolve and leave the user comparing
// their window against the spec to work out which one is missing.
func TestPlanLayoutMissingPane(t *testing.T) {
	n, err := ParseLayout("orchestrator | coder")
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanLayout(n, map[string]string{"orchestrator": "%1"})
	if err == nil {
		t.Fatal("PlanLayout with an unresolved alias should error")
	}
	if !strings.Contains(err.Error(), "coder") {
		t.Errorf("error %q should name the unresolved alias", err)
	}
}

// ---- tmux output parsing ---------------------------------------------------------

// TestParsePaneTable: a malformed pane id is DROPPED, not kept. Every value in these
// maps becomes a `-t` argument, so a garbage id would aim a join or a resize at
// whatever tmux resolves it to — the trailing empty line from Split is the common
// case, but a partially-written row must fail the same way.
func TestParsePaneTable(t *testing.T) {
	byTTY := parsePaneTable("/dev/ttys001 %0\n/dev/ttys004 %12\n/dev/ttys009 notapane\n/dev/ttys010\n\n", 1)
	want := map[string]string{"/dev/ttys001": "%0", "/dev/ttys004": "%12"}
	if fmt.Sprint(byTTY) != fmt.Sprint(want) {
		t.Errorf("byTTY = %v, want %v", byTTY, want)
	}
	windows := parsePaneTable("%0 @0\n%3 @1\nbad @2\n", 0)
	wantW := map[string]string{"%0": "@0", "%3": "@1"}
	if fmt.Sprint(windows) != fmt.Sprint(wantW) {
		t.Errorf("windows = %v, want %v", windows, wantW)
	}
}

// ---- execution -------------------------------------------------------------------

// TestRunLayoutOpsStopsAtFirstHardFailure: a failed join means every later op is
// aimed at a tree that was never built, so pressing on would compound the damage.
// The applied count is what tells the caller the window is half-arranged rather than
// untouched, and it must count only what actually ran.
func TestRunLayoutOpsStopsAtFirstHardFailure(t *testing.T) {
	var ran [][]string
	tmuxRun = func(argv []string) ([]byte, error) {
		ran = append(ran, argv)
		if argv[len(argv)-1] == "%boom" {
			return []byte("can't find pane"), fmt.Errorf("exit status 1")
		}
		return nil, nil
	}
	t.Cleanup(func() { tmuxRun = defaultTmuxRun })

	applied, err := RunLayoutOps([]LayoutOp{
		{Argv: []string{"join-pane", "-t", "%1"}},
		{Argv: []string{"join-pane", "-t", "%boom"}},
		{Argv: []string{"join-pane", "-t", "%3"}},
	})
	if err == nil {
		t.Fatal("a failing hard op should error")
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1 (only the op before the failure)", applied)
	}
	if len(ran) != 2 {
		t.Errorf("ran %d ops, want 2 — the op after the failure must not run", len(ran))
	}
	if !strings.Contains(err.Error(), "can't find pane") {
		t.Errorf("error %q should carry tmux's own message", err)
	}
}

// TestRunLayoutOpsBestEffortFailureDoesNotAbort is the other half, and the one that
// would rot silently: a resize that an old tmux rejects must leave the arrange
// standing. It must also NOT be counted as applied — reporting a step that failed as
// done is how "applied N of M" stops meaning anything.
func TestRunLayoutOpsBestEffortFailureDoesNotAbort(t *testing.T) {
	var ran int
	tmuxRun = func(argv []string) ([]byte, error) {
		ran++
		if argv[0] == "resize-pane" {
			return []byte("unknown option"), fmt.Errorf("exit status 1")
		}
		return nil, nil
	}
	t.Cleanup(func() { tmuxRun = defaultTmuxRun })

	applied, err := RunLayoutOps([]LayoutOp{
		{Argv: []string{"join-pane", "-t", "%1"}},
		{Argv: []string{"resize-pane", "-t", "%1", "-x", "30%"}, BestEffort: true},
		{Argv: []string{"join-pane", "-t", "%3"}},
	})
	if err != nil {
		t.Fatalf("a failing best-effort op must not fail the run: %v", err)
	}
	if ran != 3 {
		t.Errorf("ran %d ops, want all 3", ran)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2 — the failed resize must not count", applied)
	}
}
