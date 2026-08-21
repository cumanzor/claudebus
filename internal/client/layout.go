package client

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"claudebus/internal/core"
)

// layout is pane's post-spawn counterpart: pane PLACES a peer at birth, layout
// REARRANGES peers that already exist. tmux only, and that is the point — iTerm2
// has no verb for moving a live session between surfaces, so a formation's shape is
// frozen at spawn there. tmux's join-pane/break-pane make it mutable.
//
// The spec is a pane tree, never English: `orchestrator | (coder / reviewer)` is two
// columns with the right one split into two rows. `|` separates columns left→right,
// `/` separates rows top→bottom, `/` binds tighter, parens group, and `alias:30%`
// pins a size. Translating a sentence into that belongs in the /bus-layout skill;
// the binary stays deterministic so a layout is reproducible and hand-typable.
//
// Peers resolve to panes LIVE (PeerPane) rather than from a stored pane id: nothing
// to keep in sync, no id recycled by a tmux server restart pointing at a stranger,
// and a peer that joined long before this verb existed resolves like any other.

// LayoutNode is one cell of the tree. A leaf names a peer; an internal node splits
// its region among Kids, Rows choosing the axis.
type LayoutNode struct {
	Alias string
	Rows  bool // internal: true = stacked rows (-v), false = side-by-side columns (-h)
	Kids  []*LayoutNode
	Size  string // "30%" / "80" from alias:30%; "" leaves tmux's natural halving
}

// Leaf reports whether n names a peer rather than splitting a region.
func (n *LayoutNode) Leaf() bool { return len(n.Kids) == 0 }

// LayoutOp is one tmux invocation in a plan. Every op is a break-pane or a join-pane
// and every one is load-bearing, so there is no best-effort tier: the only tolerated
// failure is a sized join, which retries unsized via Fallback rather than being
// skipped.
type LayoutOp struct {
	Argv []string
	// Fallback is retried when Argv fails, for ops whose sizing form is newer than the
	// tmux that may be running: `-l N%` needs tmux >= 3.1, and the pane matters more
	// than its width. Empty means no retry.
	Fallback []string
}

// ParseLayout builds the tree from a spec string. Errors carry the offending text
// because a layout is typed by hand often enough that "unexpected }" beats a
// grammar dump.
func ParseLayout(spec string) (*LayoutNode, error) {
	s := &layoutScanner{src: spec}
	root, err := s.expr()
	if err != nil {
		return nil, err
	}
	if c := s.peek(); c != 0 {
		return nil, fmt.Errorf("unexpected %q at position %d in layout spec", string(c), s.i)
	}
	seen := map[string]bool{}
	var check func(n *LayoutNode) error
	check = func(n *LayoutNode) error {
		if n.Leaf() {
			if !core.ValidName(n.Alias) {
				return fmt.Errorf("invalid alias %q in layout spec (want [A-Za-z0-9._-])", n.Alias)
			}
			if seen[n.Alias] {
				return fmt.Errorf("alias %q appears twice — a peer occupies one pane", n.Alias)
			}
			seen[n.Alias] = true
			return nil
		}
		for _, k := range n.Kids {
			if err := check(k); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(root); err != nil {
		return nil, err
	}
	return root, nil
}

// LayoutAliases lists the leaves left→right, top→bottom — the order the caller
// resolves peers in, so an error names the first alias the user wrote.
func LayoutAliases(n *LayoutNode) []string {
	if n.Leaf() {
		return []string{n.Alias}
	}
	var out []string
	for _, k := range n.Kids {
		out = append(out, LayoutAliases(k)...)
	}
	return out
}

// normalizeOps breaks every pane in the spec OUT of the anchor's window before any
// join runs, so that no join has to remove a pane from the window it is building.
//
// This is not tidiness, it is correctness. join-pane removes the source from wherever
// it is, and when that is the target's own window the removal frees space which tmux
// immediately reflows into the panes already placed — so every later `-l` percentage is
// measured against a region that just moved. Re-running the same arrange on an
// already-arranged window is therefore not idempotent, and not even stable: measured
// live, `orchestrator | (coder / reviewer)` went 86/87/87 then 42/131/131 then
// 20/153/153, halving the anchor every time.
//
// Only panes sharing the ANCHOR's window need breaking out. Spec panes sitting together
// in some other window can be joined directly: their removal reflows that window, which
// is about to be dismantled anyway, and never touches the one being built.
func normalizeOps(root *LayoutNode, panes, windows map[string]string) ([]LayoutOp, error) {
	if len(windows) == 0 {
		return nil, nil
	}
	anchor, err := repPane(root, panes)
	if err != nil {
		return nil, err
	}
	anchorWin, ok := windows[anchor]
	if !ok {
		return nil, nil
	}
	var ops []LayoutOp
	for _, alias := range LayoutAliases(root) {
		pane := panes[alias]
		if pane == anchor {
			continue
		}
		if windows[pane] == anchorWin {
			ops = append(ops, LayoutOp{Argv: []string{"break-pane", "-d", "-s", pane}})
		}
	}
	return ops, nil
}

// PlanLayout turns the tree plus an alias→pane map into the tmux calls that realize
// it. Pure, so --dry-run prints exactly what a real run executes.
//
// The walk is breadth-then-depth ON PURPOSE. Every sibling at a level is joined
// while it is still a single pane, and only then is each child's own subtree built;
// building depth-first would split a child that had already grown sub-panes, landing
// the next sibling one level too deep. Each subtree is represented by its first
// leaf's pane (repPane) — the pane that owns the region — and joins chain sibling to
// previous sibling so the aliases land in the order they were written.
//
// Sizing runs as a second pass over the whole tree, after every join, because a
// resize against a region that is still growing is a resize of the wrong geometry.
func PlanLayout(root *LayoutNode, panes, windows map[string]string) ([]LayoutOp, error) {
	ops, err := normalizeOps(root, panes, windows)
	if err != nil {
		return nil, err
	}
	var build func(n *LayoutNode) error
	build = func(n *LayoutNode) error {
		if n.Leaf() {
			return nil
		}
		flag := "-h"
		if n.Rows {
			flag = "-v"
		}
		prev, err := repPane(n.Kids[0], panes)
		if err != nil {
			return err
		}
		w, err := childWeights(n)
		if err != nil {
			return err
		}
		for i, k := range n.Kids[1:] {
			src, err := repPane(k, panes)
			if err != nil {
				return err
			}
			argv := []string{"join-pane", "-d", flag, "-s", src, "-t", prev}
			plain := append([]string{}, argv...)
			// -t prev currently holds the region for kids i+1..n-1 (0-based), and the
			// joined pane takes everything after prev. Sizing it by that suffix ratio is
			// what makes n children divide the PARENT evenly instead of each one halving
			// the previous pane, which is where 50/25/25 came from.
			if pct := suffixPct(w, i+1); pct > 0 && pct < 100 {
				argv = append(argv, "-l", strconv.Itoa(pct)+"%")
			}
			ops = append(ops, LayoutOp{Argv: argv, Fallback: plain})
			prev = src
		}
		for _, k := range n.Kids {
			if err := build(k); err != nil {
				return err
			}
		}
		return nil
	}
	if err := build(root); err != nil {
		return nil, err
	}
	return ops, nil
}

// childWeights turns one node's children into shares of the parent region: an
// explicit percentage is taken as written, and every unsized child takes an equal cut
// of whatever is left. That is the whole sizing rule — the split is inferred from how
// many children there are, unless the spec says otherwise — and even thirds for three
// peers falls out of it rather than being a special case.
func childWeights(n *LayoutNode) ([]int, error) {
	w := make([]int, len(n.Kids))
	explicit, unsized := 0, 0
	for i, k := range n.Kids {
		if pct, ok := pctSize(k.Size); ok {
			w[i] = pct
			explicit += pct
			continue
		}
		unsized++
	}
	if explicit > 100 {
		return nil, fmt.Errorf("sizes under one split add up to %d%%, over 100%%", explicit)
	}
	if unsized > 0 {
		share := (100 - explicit) / unsized
		for i := range w {
			if w[i] == 0 {
				w[i] = share
			}
		}
	}
	return w, nil
}

// pctSize reads "30%" as 30. The parser guarantees the suffix, so the only miss here
// is an absent size, which means "infer my share".
func pctSize(size string) (int, bool) {
	if !strings.HasSuffix(size, "%") {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSuffix(size, "%"))
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// suffixPct is the share of the CURRENT target region that the pane being joined
// should take: everything from index i onward, over everything from i-1 onward. The
// target still holds the whole tail at this point, so the ratio is against that tail
// and not against the window.
func suffixPct(w []int, i int) int {
	if i <= 0 || i >= len(w) {
		return 0
	}
	tail, whole := 0, 0
	for j := i; j < len(w); j++ {
		tail += w[j]
	}
	whole = tail + w[i-1]
	if whole <= 0 {
		return 0
	}
	return tail * 100 / whole
}

// repPane is the pane standing in for a whole subtree: its first leaf's, which is
// the pane that holds the subtree's region before the subtree is built.
func repPane(n *LayoutNode, panes map[string]string) (string, error) {
	for !n.Leaf() {
		n = n.Kids[0]
	}
	pane, ok := panes[n.Alias]
	if !ok {
		return "", fmt.Errorf("no pane resolved for %q", n.Alias)
	}
	return pane, nil
}

// tmuxRun is the single point every layout op goes through, indirected only so a
// test can drive the partial-failure and best-effort paths: both are about what
// happens AFTER a tmux call fails, which a real tmux cannot be asked to do on cue.
var tmuxRun = defaultTmuxRun

// RunLayoutOps executes a plan in order, stopping at the first hard failure and
// reporting how far it got. A half-applied rearrange is visible on screen and fixed
// by re-running, so stopping loudly beats pressing on through a broken tree — but
// the caller has to be TOLD which ops landed, hence the count.
func RunLayoutOps(ops []LayoutOp) (applied int, err error) {
	for _, op := range ops {
		out, runErr := tmuxRun(op.Argv)
		if runErr != nil && len(op.Fallback) > 0 {
			// the sizing form is the only reason a join fails on an older tmux, and a
			// correctly-placed pane at the wrong width beats no pane at all.
			out, runErr = tmuxRun(op.Fallback)
		}
		if runErr != nil {
			return applied, fmt.Errorf("tmux %s: %v: %s",
				strings.Join(op.Argv, " "), runErr, strings.TrimSpace(string(out)))
		}
		applied++
	}
	return applied, nil
}

// selfPane short-circuits the lookup for THIS session's own registration: $TMUX_PANE
// names the caller's pane directly, so the one peer running the command never needs
// the meta -> pid -> tty chain. It is also the only peer that can be located BEFORE it
// arms, because a fresh join writes ownerPid AND listenerPid null and both are stamped
// at arm time — without this, an orchestrator could not put itself in its own layout
// until it had armed a Monitor, and the failure read as "not running" about the very
// session typing the command.
//
// The env value is validated against the LIVE pane set rather than trusted: a stale
// $TMUX_PANE inherited from a dead pane would otherwise become a -t landing a join on
// whatever pane now holds that id. Falls through to the normal chain on any doubt.
func selfPane(ch, alias string, byTTY map[string]string) string {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || !validTmuxPaneID(pane) {
		return ""
	}
	live := false
	for _, p := range byTTY {
		if p == pane {
			live = true
			break
		}
	}
	if !live {
		return ""
	}
	for _, reg := range ResolveSelf() {
		if reg.Channel == ch && reg.Alias == alias {
			return pane
		}
	}
	return ""
}

// layoutScanner is a hand-rolled recursive-descent reader over the spec. The
// separators (| / ( ) :) are all outside core.ValidName's alias charset, so no
// tokenizer state is needed to tell an alias from punctuation.
type layoutScanner struct {
	src string
	i   int
}

// peek skips whitespace and returns the next byte, 0 at end. It advances i past the
// whitespace, so every caller can read from i directly afterwards.
func (s *layoutScanner) peek() byte {
	for s.i < len(s.src) && (s.src[s.i] == ' ' || s.src[s.i] == '\t') {
		s.i++
	}
	if s.i >= len(s.src) {
		return 0
	}
	return s.src[s.i]
}

// expr reads columns: the loosest binding, so `a | b / c` is a beside a stack.
func (s *layoutScanner) expr() (*LayoutNode, error) {
	return s.level('|', false, (*layoutScanner).row)
}

// row reads stacked terms, binding tighter than '|'.
func (s *layoutScanner) row() (*LayoutNode, error) {
	return s.level('/', true, (*layoutScanner).term)
}

// level is the shared shape of expr and row: one or more sub-nodes separated by sep.
// A single sub-node collapses to itself rather than becoming a one-child group, so
// `(a)` is just a.
func (s *layoutScanner) level(sep byte, rows bool, next func(*layoutScanner) (*LayoutNode, error)) (*LayoutNode, error) {
	first, err := next(s)
	if err != nil {
		return nil, err
	}
	kids := []*LayoutNode{first}
	for s.peek() == sep {
		s.i++
		n, err := next(s)
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
	if len(kids) == 1 {
		return first, nil
	}
	return &LayoutNode{Rows: rows, Kids: kids}, nil
}

// term reads a parenthesised group or a bare alias, each optionally sized.
func (s *layoutScanner) term() (*LayoutNode, error) {
	switch c := s.peek(); {
	case c == 0:
		return nil, fmt.Errorf("layout spec ends where an alias was expected")
	case c == '(':
		s.i++
		n, err := s.expr()
		if err != nil {
			return nil, err
		}
		if s.peek() != ')' {
			return nil, fmt.Errorf("unclosed ( in layout spec")
		}
		s.i++
		size, err := s.size()
		if err != nil {
			return nil, err
		}
		n.Size = size
		return n, nil
	case c == ')' || c == '|' || c == '/':
		return nil, fmt.Errorf("unexpected %q at position %d in layout spec", string(c), s.i)
	}
	start := s.i
	for s.i < len(s.src) && isAliasByte(s.src[s.i]) {
		s.i++
	}
	if s.i == start {
		return nil, fmt.Errorf("unexpected %q at position %d in layout spec", string(s.src[s.i]), s.i)
	}
	alias := s.src[start:s.i]
	size, err := s.size()
	if err != nil {
		return nil, err
	}
	return &LayoutNode{Alias: alias, Size: size}, nil
}

// size reads an optional :30% suffix. Percentages only, BY RULING: a size is a share
// of the parent region, and a cell count is not a share of anything the planner can
// know — it cannot be turned into a ratio until tmux has drawn the region, so it could
// only ever be applied as a post-hoc resize, which takes its cells from one neighbour
// and leaves the siblings uneven (`a:40 | b | c` measured 40/75/57 live). Half-support
// was worse than none.
//
// No whitespace is skipped before the colon: `coder :30%` is a typo, and reading it as
// a size would silently accept a spec the user did not write.
func (s *layoutScanner) size() (string, error) {
	if s.i >= len(s.src) || s.src[s.i] != ':' {
		return "", nil
	}
	s.i++
	start := s.i
	for s.i < len(s.src) && s.src[s.i] >= '0' && s.src[s.i] <= '9' {
		s.i++
	}
	digits := s.i - start
	if s.i >= len(s.src) || s.src[s.i] != '%' {
		return "", fmt.Errorf("a size must be a percentage like :30%% (got %q) — cell counts are not supported", s.src[start-1:s.i])
	}
	s.i++
	if digits == 0 {
		return "", fmt.Errorf("a size must be a percentage like :30%%")
	}
	return s.src[start:s.i], nil
}

// isAliasByte is core.ValidName's charset, byte-wise — the scanner needs the
// per-character test that ValidName only exposes over a whole string.
func isAliasByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	case b == '.' || b == '_' || b == '-':
		return true
	}
	return false
}

// parsePaneTable reads two-column tmux -F output into a map, keyed by the field
// that is NOT the pane id (paneField says which column holds it). Every row is
// shape-checked and a row whose pane id is malformed is DROPPED rather than kept:
// a bad id flows straight into a -t, where it would land a join or a resize on some
// other pane entirely. Short rows (the trailing newline, a format tmux did not
// understand) are skipped for the same reason.
func parsePaneTable(out string, paneField int) map[string]string {
	m := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || !validTmuxPaneID(f[paneField]) {
			continue
		}
		m[f[0]] = f[1]
	}
	return m
}
