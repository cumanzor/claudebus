package client

import "strings"

// The ancestor walk that answers "which coding harness owns this process", kept PURE
// over injected records. Only procLookup touches a syscall, so every stop condition —
// depth, root, a dangling parent, a parent younger than its child — is a table test on
// any host instead of a thing that can only be exercised on the platform that has the
// hazard.

// maxWalkDepth bounds the climb. It is a backstop against a cycle or a pathological
// tree, NOT the mechanism: a walk that reaches it has failed to find its own stop.
const maxWalkDepth = 16

// procRecord is everything the walk needs about one process.
//
// Created is an opaque platform ordering value for process creation, comparable only
// against another Created produced on the same platform, and 0 where the platform does
// not supply one. It is never rendered and never treated as a clock.
type procRecord struct {
	PPid    int
	Comm    string
	Argv    string
	Created uint64
}

// harnessWalk climbs from start toward rootPid and returns the first ancestor whose
// identity names a coding harness, or "" when none does — never guessed.
//
// Identity is argv[0]'s basename with comm as a fallback, or the reverse depending on
// the platform; both clauses are tried here and each platform fills the field it can
// answer honestly (see procLookup).
func harnessWalk(start, rootPid int, lookup func(int) (procRecord, bool)) string {
	p := start
	var prev procRecord
	var havePrev bool
	for depth := 0; p > rootPid && depth < maxWalkDepth; depth++ {
		rec, ok := lookup(p)
		if !ok {
			return ""
		}
		if havePrev && !ancestryPlausible(prev, rec) {
			return "" // the link we followed was stale; anything above it is a stranger
		}
		if base := commBase(rec.Comm); isHarnessComm(base) {
			return normalizeHarness(base)
		}
		if f := strings.Fields(rec.Argv); len(f) > 0 {
			if base := commBase(f[0]); isHarnessComm(base) {
				return normalizeHarness(base)
			}
		}
		if rec.PPid <= rootPid {
			break
		}
		prev, havePrev = rec, true
		p = rec.PPid
	}
	return ""
}

// ancestryPlausible reports whether parent can really be child's parent: a parent
// cannot have been created after its own child.
//
// This exists because windows does not clear a process's ParentProcessId when the
// parent exits, and it reuses pids. A dangling parent id can therefore come to name an
// unrelated LIVE process, and a walk that follows it crosses into a foreign tree and
// reports that tree's harness. Measured on the target machine rather than assumed: 15
// live processes there carried a parent id naming a pid that no longer existed, and 2
// claimed a parent created after the child.
//
// A zero Created is "this platform does not order processes" and never rejects, so on
// unix — where an orphan is reparented to init and a ppid always names a live process —
// the check is inert by construction rather than by luck.
func ancestryPlausible(child, parent procRecord) bool {
	if child.Created == 0 || parent.Created == 0 {
		return true
	}
	return parent.Created <= child.Created
}
