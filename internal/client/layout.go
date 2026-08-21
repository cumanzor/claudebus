package client

import (
	"fmt"
	"os/exec"
	"path/filepath"
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

// LayoutOp is one tmux invocation in a plan. BestEffort marks the sizing tail: an
// older tmux without percentage sizing must not fail an otherwise-good arrange, the
// same call the pane splitter already makes for its resize.
type LayoutOp struct {
	Argv       []string
	BestEffort bool
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
func PlanLayout(root *LayoutNode, panes map[string]string) ([]LayoutOp, error) {
	var ops []LayoutOp
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
		for _, k := range n.Kids[1:] {
			src, err := repPane(k, panes)
			if err != nil {
				return err
			}
			ops = append(ops, LayoutOp{Argv: []string{"join-pane", "-d", flag, "-s", src, "-t", prev}})
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
	// parentRows picks the axis: a child of a columns node is sized across (-x), a
	// child of a rows node down (-y). The root has no parent region to divide, so its
	// own Size is meaningless and deliberately dropped rather than errored on — the
	// spec `(a|b):50%` is harmless, just unenforceable.
	var size func(n *LayoutNode, parentRows bool, isRoot bool) error
	size = func(n *LayoutNode, parentRows, isRoot bool) error {
		if n.Size != "" && !isRoot {
			pane, err := repPane(n, panes)
			if err != nil {
				return err
			}
			axis := "-x"
			if parentRows {
				axis = "-y"
			}
			ops = append(ops, LayoutOp{Argv: []string{"resize-pane", "-t", pane, axis, n.Size}, BestEffort: true})
		}
		for _, k := range n.Kids {
			if err := size(k, n.Rows, false); err != nil {
				return err
			}
		}
		return nil
	}
	if err := size(root, false, true); err != nil {
		return nil, err
	}
	return ops, nil
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

func defaultTmuxRun(argv []string) ([]byte, error) {
	return exec.Command("tmux", argv...).CombinedOutput()
}

// RunLayoutOps executes a plan in order, stopping at the first hard failure and
// reporting how far it got. A half-applied rearrange is visible on screen and fixed
// by re-running, so stopping loudly beats pressing on through a broken tree — but
// the caller has to be TOLD which ops landed, hence the count.
func RunLayoutOps(ops []LayoutOp) (applied int, err error) {
	for _, op := range ops {
		out, runErr := tmuxRun(op.Argv)
		if runErr != nil {
			if op.BestEffort {
				continue
			}
			return applied, fmt.Errorf("tmux %s: %v: %s",
				strings.Join(op.Argv, " "), runErr, strings.TrimSpace(string(out)))
		}
		applied++
	}
	return applied, nil
}

// TmuxPanesByTTY maps /dev/ttysNNN → %N for every pane on the running server in ONE
// call, so a whole channel resolves without one tmux invocation per peer. It is the
// same tty→pane join the close sweep does per peer, hoisted.
func TmuxPanesByTTY() (map[string]string, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_tty} #{pane_id}").Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %v%s", err, cmdStderr(err))
	}
	return parsePaneTable(string(out), 1), nil
}

// PeerPane resolves ch/alias to the tmux pane the peer runs in, entirely live: meta
// → owning pid → controlling tty → pane. The owner fallback and its identity test
// are ClosePeer's, for the same reason — a recycled listener pid must not donate a
// stranger's process, and here that would drag an unrelated session's pane into
// someone's layout.
func PeerPane(ch, alias string, byTTY map[string]string) (string, error) {
	metaPath := filepath.Join(CBUSDir(), ch, alias, "meta.json")
	m, ok := ReadPeerMeta(metaPath)
	if !ok {
		return "", fmt.Errorf("no peer %s/%s", ch, alias)
	}
	pid := m.OwnerPid
	if pid == 0 && m.ListenerPid > 0 && pidAlive(m.ListenerPid) && listenerIdentityHolds(m, metaPath) {
		pid, _ = ownerFromPid(m.ListenerPid)
	}
	if pid == 0 || !pidAlive(pid) || procZombie(pid) {
		return "", fmt.Errorf("%s is not running", alias)
	}
	tty := ttyOf(pid)
	if tty == "" {
		return "", fmt.Errorf("%s has no controlling tty — nothing to arrange", alias)
	}
	pane, ok := byTTY["/dev/"+tty]
	if !ok {
		return "", fmt.Errorf("%s is not in a tmux pane (tty %s) — layout is tmux-only", alias, tty)
	}
	return pane, nil
}

// ResolvePeerPanes resolves every alias in one pass, reporting ALL failures rather
// than the first: told "coder is not running" a user fixes coder and reruns, only to
// learn reviewer is in iTerm2. One message, one round trip.
func ResolvePeerPanes(ch string, aliases []string) (map[string]string, error) {
	byTTY, err := TmuxPanesByTTY()
	if err != nil {
		return nil, err
	}
	panes := make(map[string]string, len(aliases))
	var bad []string
	for _, a := range aliases {
		pane, err := PeerPane(ch, a, byTTY)
		if err != nil {
			bad = append(bad, err.Error())
			continue
		}
		panes[a] = pane
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return panes, nil
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

// size reads an optional :30% or :80 suffix. No whitespace is skipped before the
// colon: `coder :30%` is a typo, and reading it as a size would silently accept a
// spec the user did not write.
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
	if s.i < len(s.src) && s.src[s.i] == '%' {
		s.i++
	}
	if digits == 0 {
		return "", fmt.Errorf("size after ':' must be a number of columns or a percentage")
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

// TmuxPaneWindows maps %N → @M for every pane on the server. scatter needs it to
// tell a pane that shares a window (breakable) from one that is already alone in
// its own window, which break-pane refuses — checking beforehand turns that refusal
// into an accurate "already its own window" instead of an error the user must read
// past.
func TmuxPaneWindows() (map[string]string, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{window_id}").Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %v%s", err, cmdStderr(err))
	}
	return parsePaneTable(string(out), 0), nil
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
