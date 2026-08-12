package client

import "testing"

func lookupFrom(recs map[int]procRecord) func(int) (procRecord, bool) {
	return func(pid int) (procRecord, bool) {
		r, ok := recs[pid]
		return r, ok
	}
}

// TestHarnessWalkStops covers every way the walk ends, on the build host, because the
// walk is pure over injected records. The two that matter most are the inversion pair:
// a parent created AFTER its child is a stale link and must not be believed, and the
// same tree with no creation times must still resolve — otherwise the check that
// protects windows would be silently rejecting unix ancestry too.
func TestHarnessWalkStops(t *testing.T) {
	cases := []struct {
		name  string
		start int
		root  int
		recs  map[int]procRecord
		want  string
	}{
		{
			name:  "comm match at the first hop",
			start: 100, root: 1,
			recs: map[int]procRecord{100: {PPid: 200, Comm: "claude"}},
			want: "claude",
		},
		{
			name:  "comm match two hops up",
			start: 100, root: 1,
			recs: map[int]procRecord{
				100: {PPid: 200, Comm: "sh"},
				200: {PPid: 300, Comm: "zsh"},
				300: {PPid: 1, Comm: "claude-dev"},
			},
			want: "claude",
		},
		{
			name:  "argv clause matches where comm does not",
			start: 100, root: 1,
			recs: map[int]procRecord{100: {PPid: 1, Comm: "2.1.214", Argv: "/opt/bin/claude --resume"}},
			want: "claude",
		},
		{
			name:  "dangling parent id: the lookup misses and nothing is guessed",
			start: 100, root: 1,
			recs: map[int]procRecord{100: {PPid: 999, Comm: "sh"}},
			want: "",
		},
		{
			name:  "parent created AFTER the child: stale link, harness above it is a stranger",
			start: 100, root: 1,
			recs: map[int]procRecord{
				100: {PPid: 200, Comm: "sh", Created: 5000},
				200: {PPid: 1, Comm: "claude", Created: 9000},
			},
			want: "",
		},
		{
			name:  "no creation times at all: the age check is inert, not rejecting",
			start: 100, root: 1,
			recs: map[int]procRecord{
				100: {PPid: 200, Comm: "sh"},
				200: {PPid: 1, Comm: "claude"},
			},
			want: "claude",
		},
		{
			// The case the unknown-guard actually exists for. An all-zero pair compares
			// 0 <= 0 and survives even without the guard, so it pins nothing; only a
			// MIXED pair does. Reachable on windows whenever the child's creation time
			// is unreadable (access denied) and the parent's is not.
			name:  "child creation time unknown, parent known: unknown must not reject",
			start: 100, root: 1,
			recs: map[int]procRecord{
				100: {PPid: 200, Comm: "sh"},
				200: {PPid: 1, Comm: "claude", Created: 9000},
			},
			want: "claude",
		},
		{
			// Windows creation times quantize at the system timer granularity, about
			// 15.6ms, so a parent and child spawned in the same quantum carry the SAME
			// tick. Equal has to stay plausible: a strict less-than here would read
			// genuine ancestry as a stale link and HarnessName would return empty,
			// silently, for any session started in the same tick as its parent.
			name:  "parent and child created in the same tick: equal is plausible",
			start: 100, root: 1,
			recs: map[int]procRecord{
				100: {PPid: 200, Comm: "sh", Created: 5000},
				200: {PPid: 1, Comm: "claude", Created: 5000},
			},
			want: "claude",
		},
		{
			name:  "parent older than child is plausible and the walk continues",
			start: 100, root: 1,
			recs: map[int]procRecord{
				100: {PPid: 200, Comm: "sh", Created: 9000},
				200: {PPid: 1, Comm: "claude", Created: 5000},
			},
			want: "claude",
		},
		{
			name:  "unix root: the walk stops above pid 1 and never inspects init",
			start: 100, root: 1,
			recs: map[int]procRecord{
				100: {PPid: 1, Comm: "sh"},
				1:   {PPid: 0, Comm: "claude"},
			},
			want: "",
		},
		{
			name:  "windows root: the walk stops above pid 0",
			start: 100, root: 0,
			recs: map[int]procRecord{
				100: {PPid: 0, Comm: "sh"},
				0:   {PPid: 0, Comm: "claude"},
			},
			want: "",
		},
		{
			name:  "windows root: pid 1 is an ordinary process and IS inspected",
			start: 100, root: 0,
			recs: map[int]procRecord{
				100: {PPid: 1, Comm: "sh"},
				1:   {PPid: 0, Comm: "claude"},
			},
			want: "claude",
		},
		{
			name:  "a cycle terminates on the depth backstop rather than spinning",
			start: 100, root: 1,
			recs: map[int]procRecord{
				100: {PPid: 101, Comm: "sh"},
				101: {PPid: 100, Comm: "zsh"},
			},
			want: "",
		},
		{
			name:  "no harness anywhere",
			start: 100, root: 1,
			recs: map[int]procRecord{100: {PPid: 1, Comm: "sh"}},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := harnessWalk(c.start, c.root, lookupFrom(c.recs)); got != c.want {
				t.Errorf("harnessWalk = %q, want %q", got, c.want)
			}
		})
	}
}

// TestHarnessWalkDepthIsBounded pins the backstop itself: a chain longer than the cap
// gives up rather than walking a pathological tree to its root.
func TestHarnessWalkDepthIsBounded(t *testing.T) {
	recs := map[int]procRecord{}
	for i := 0; i < maxWalkDepth+4; i++ {
		recs[100+i] = procRecord{PPid: 101 + i, Comm: "sh"}
	}
	recs[100+maxWalkDepth+4] = procRecord{PPid: 1, Comm: "claude"}
	if got := harnessWalk(100, 1, lookupFrom(recs)); got != "" {
		t.Errorf("harnessWalk past the depth cap = %q, want \"\"", got)
	}
}
