package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// plantIntent writes a marker aged by `age`, standing in for a resume that fired that
// long ago. Written through the same path the real one uses, so a test can never pass
// against a file the code would not have found.
func plantIntent(t *testing.T, ch, alias, sid string, age time.Duration) string {
	t.Helper()
	// the marker's own parent, not an assumed channel dir: a fixture that hard-codes
	// where the file lives measures the path choice instead of the mechanism, and dies
	// on its precondition when the location moves
	path := launchIntentPath(ch, alias)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(LaunchIntent{
		Channel: ch, Alias: alias, SessionID: sid, Pid: 4711, ProcStart: "planted",
		TS: time.Now().UTC().Add(-age).Format("2006-01-02T15:04:05Z"),
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func intentExists(ch, alias string) bool {
	_, err := os.Stat(launchIntentPath(ch, alias))
	return err == nil
}

func TestLaunchIntentRoundTrip(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	if err := WriteLaunchIntent("dd", "orchestrator", "sid-anchor"); err != nil {
		t.Fatalf("write: %v", err)
	}
	in, age, ok := FreshLaunchIntent("dd", "orchestrator")
	if !ok {
		t.Fatal("an intent written a moment ago must read fresh")
	}
	if in.SessionID != "sid-anchor" || in.Channel != "dd" || in.Alias != "orchestrator" {
		t.Errorf("intent = %+v", in)
	}
	if in.Pid != os.Getpid() {
		t.Errorf("pid = %d, want this process %d — the provenance names who promised the launch", in.Pid, os.Getpid())
	}
	if in.ProcStart == "" {
		t.Error("procStart is the structural half of the provenance; a blank one cannot tell a recycled pid apart")
	}
	if age > 5*time.Second {
		t.Errorf("age = %s, want ~0", age)
	}
	// the channel dir is created when absent: resuming into a store a reboot emptied
	// is legal, and the marker cannot depend on a peer dir that no longer exists
	if !intentExists("dd", "orchestrator") {
		t.Error("no marker on disk")
	}
}

func TestLaunchIntentExpires(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantIntent(t, "dd", "orchestrator", "sid-anchor", launchIntentTTL-10*time.Second)
	if _, age, ok := FreshLaunchIntent("dd", "orchestrator"); !ok {
		t.Fatalf("an intent inside the TTL must still refuse (age %s, ttl %s)", age, launchIntentTTL)
	}
	plantIntent(t, "dd", "orchestrator", "sid-anchor", launchIntentTTL+time.Second)
	if in, _, ok := FreshLaunchIntent("dd", "orchestrator"); ok {
		t.Errorf("an expired intent must not refuse: %+v", in)
	}
	// unreadable bytes read ABSENT, by decision: they carry no age, so nothing could
	// ever expire them, and a guard with no way out is worse than the window
	if err := os.WriteFile(launchIntentPath("dd", "orchestrator"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := FreshLaunchIntent("dd", "orchestrator"); ok {
		t.Error("a marker that cannot be parsed must not wedge the verb forever")
	}
}

// TestLaunchIntentClearsOnSameSidOnly is the clearing contract in one test: the
// session that was launched, or the TTL, and nothing else.
func TestLaunchIntentClearsOnSameSidOnly(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantIntent(t, "dd", "orchestrator", "sid-anchor", time.Second)

	ClearLaunchIntentFor("dd", "orchestrator", "")
	ClearLaunchIntentFor("dd", "orchestrator", "sid-someone-else")
	if !intentExists("dd", "orchestrator") {
		t.Fatal("a foreign session cleared a marker that was not its launch")
	}
	ClearLaunchIntentFor("dd", "orchestrator", "sid-anchor")
	if intentExists("dd", "orchestrator") {
		t.Error("the launched session's own arrival must clear the marker")
	}
}

// TestLaunchIntentIsInvisibleToTheChannel: the marker lives in the channel dir, so it
// has to be nothing to every walker that reads one. A file that became a peer would
// show up in the roster, be prunable, and be counted as a member.
func TestLaunchIntentIsInvisibleToTheChannel(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantPeer(t, "dd", "orchestrator", "sid-anchor")
	before, err := ChannelRoster("dd")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteLaunchIntent("dd", "coder", "sid-coder"); err != nil {
		t.Fatal(err)
	}
	after, err := ChannelRoster("dd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("the marker became a roster peer: %d -> %d (%+v)", len(before), len(after), after)
	}
	for _, r := range after {
		if strings.Contains(r.Alias, "launch-intent") {
			t.Errorf("marker surfaced as peer %q", r.Alias)
		}
	}
	if _, ok := liveSids()["sid-coder"]; ok {
		t.Error("the marker's sid must not read as a live session — nothing is armed on it yet")
	}
	PruneChannel("dd")
	if !intentExists("dd", "coder") {
		t.Error("prune ate the marker; its lifetime is the TTL, not a sweep")
	}
}

// intentSpyForker records whether the marker was ALREADY on disk at the moment the
// fork happened. That ordering is the whole guarantee: a marker written after the
// fork leaves the window it exists to close wide open, and a test that only checks
// the marker afterwards cannot tell the two apart.
type intentSpyForker struct {
	recForker
	ch, alias   string
	sawAtFork   bool
	sidAtFork   string
	failWithErr error
}

func (f *intentSpyForker) Fork(s ForkSpec) (string, error) {
	if in, _, ok := FreshLaunchIntent(f.ch, f.alias); ok {
		f.sawAtFork, f.sidAtFork = true, in.SessionID
	}
	if f.failWithErr != nil {
		return "", f.failWithErr
	}
	return f.recForker.Fork(s)
}

func TestResumeWritesIntentBeforeForking(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	fk := &intentSpyForker{ch: "dd", alias: "orchestrator"}
	if _, err := resumeAnchorWorld(resumeFixture(), "", fk, resumeWorld()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !fk.sawAtFork {
		t.Fatal("the intent was not on disk when the fork ran — the window is still open")
	}
	if fk.sidAtFork != "sid-anchor" {
		t.Errorf("intent named session %q at fork time, want the one being resumed", fk.sidAtFork)
	}
}

func TestResumeRefusesAFreshIntent(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantIntent(t, "dd", "orchestrator", "sid-anchor", 12*time.Second)
	fk := &recForker{}
	_, err := resumeAnchorWorld(resumeFixture(), "", fk, resumeWorld())
	if err == nil {
		t.Fatal("a second resume inside the launch window must refuse")
	}
	for _, want := range []string{"orchestrator", "4711", "expires in"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q so the operator can act on it: %v", want, err)
		}
	}
	// the age is asserted as a RANGE, not a literal: it is measured from the planted
	// timestamp at assertion time, so scheduler latency under a loaded suite crosses
	// the rounding boundary and a literal "12s" is a test that fails on the clock
	// rather than on the mechanism. The floor still catches a hard-coded or zero age.
	m := regexp.MustCompile(`launched (\d+)s ago`).FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("the refusal must name its own age in seconds: %v", err)
	}
	secs, convErr := strconv.Atoi(m[1])
	if convErr != nil {
		t.Fatal(convErr)
	}
	if secs < 12 || secs > 60 {
		t.Errorf("age = %ds, want the planted 12s (a little rounding drift allowed): %v", secs, err)
	}
	if len(fk.specs) != 0 {
		t.Errorf("refused and forked anyway: %v", fk.specs)
	}
}

func TestResumeProceedsPastAnExpiredIntent(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantIntent(t, "dd", "orchestrator", "sid-anchor", launchIntentTTL+time.Minute)
	fk := &recForker{}
	if _, err := resumeAnchorWorld(resumeFixture(), "", fk, resumeWorld()); err != nil {
		t.Fatalf("an expired intent must not block a legitimate resume: %v", err)
	}
	if len(fk.specs) != 1 {
		t.Fatalf("specs = %d, want the launch to have happened", len(fk.specs))
	}
	in, age, ok := FreshLaunchIntent("dd", "orchestrator")
	if !ok {
		t.Fatal("the new launch left no intent behind — the next resume would not see it")
	}
	if age > 5*time.Second || in.Pid != os.Getpid() {
		t.Errorf("the stale marker was not replaced: age=%s pid=%d", age, in.Pid)
	}
}

// TestResumeWritesNoIntentWhenItRefuses: a refusal must not lock out the next attempt.
// If the marker were written before the gates, fixing the cause (re-pointing the
// formation, standing the original down) would leave the operator blocked for the
// whole TTL by their own failed try.
func TestResumeWritesNoIntentWhenItRefuses(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*Formation)
		world func() *PlanWorld
	}{
		{"fork-born anchor", func(f *Formation) { f.Peers[0].Origin = OriginFork }, resumeWorld},
		{"unrecorded origin", func(f *Formation) { f.Peers[0].Origin = "" }, resumeWorld},
		{"no session recorded", func(f *Formation) { f.Peers[0].SessionID = "" }, resumeWorld},
		{"wrong machine", func(f *Formation) { f.Peers[0].Machine = "host-b" }, resumeWorld},
		{"live-armed sid", func(f *Formation) {}, func() *PlanWorld {
			w := resumeWorld()
			w.LiveSids = map[string]string{"sid-anchor": "dd/orchestrator"}
			return w
		}},
		{"gone transcript", func(f *Formation) {}, func() *PlanWorld {
			w := resumeWorld()
			w.HasTranscript = func(profile, sid string) bool { return false }
			return w
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CBUS_DIR", t.TempDir())
			f := resumeFixture()
			tc.mut(f)
			fk := &recForker{}
			if _, err := resumeAnchorWorld(f, "", fk, tc.world()); err == nil {
				t.Fatal("want a refusal")
			}
			if intentExists("dd", "orchestrator") {
				t.Error("a refused resume left an intent behind, blocking the next attempt for the whole TTL")
			}
			if len(fk.specs) != 0 {
				t.Errorf("refused and forked anyway: %v", fk.specs)
			}
		})
	}
}

// TestResumeLeavesTheIntentOnForkError is the ruled divergence from Unreserve's
// fork-failure cleanup (R3/(a)). A reservation guards a NAME, and losing one only
// strands an alias. This guards a TRANSCRIPT, in the window where a fork that
// REPORTS failure may still have opened a window — osascript can error after the
// launch. So the marker stands and expires on its own.
func TestResumeLeavesTheIntentOnForkError(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	fk := &intentSpyForker{ch: "dd", alias: "orchestrator", failWithErr: os.ErrPermission}
	if _, err := resumeAnchorWorld(resumeFixture(), "", fk, resumeWorld()); err == nil {
		t.Fatal("a failed fork must surface")
	}
	if !intentExists("dd", "orchestrator") {
		t.Error("the intent was cleared on a fork error; a fork that reports failure " +
			"does not prove no window opened")
	}
}

// TestJoinClearsTheLaunchIntent walks the whole mechanism: the child comes back, and
// its own join is what shuts the window. The foreign-sid half is the finding that
// moved this marker out of the peer dir — Join does os.RemoveAll(peerDir) on an
// explicit-alias claim, so a marker stored there would be destroyed by exactly the
// session it is meant to guard against.
func TestJoinClearsTheLaunchIntent(t *testing.T) {
	t.Run("same session clears it", func(t *testing.T) {
		t.Setenv("CBUS_DIR", t.TempDir())
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-anchor")
		plantIntent(t, "dd", "orchestrator", "sid-anchor", 5*time.Second)
		if _, _, err := Join("dd", "orchestrator"); err != nil {
			t.Fatalf("join: %v", err)
		}
		if intentExists("dd", "orchestrator") {
			t.Error("the resumed session joined and the marker survived — the next resume " +
				"would refuse a window that is already open")
		}
	})
	t.Run("a foreign session leaves it", func(t *testing.T) {
		t.Setenv("CBUS_DIR", t.TempDir())
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-stranger")
		plantIntent(t, "dd", "orchestrator", "sid-anchor", 5*time.Second)
		if _, _, err := Join("dd", "orchestrator"); err != nil {
			t.Fatalf("join: %v", err)
		}
		if !intentExists("dd", "orchestrator") {
			t.Error("a different session took the alias and cleared the guard protecting " +
				"sid-anchor's transcript")
		}
	})
}
