//go:build darwin || linux

package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPredicateStructuralZombieReadsDead is F3: a dead-but-unreaped follower must not
// read alive. It is the one edge where the structural witness is WEAKER than the argv
// clause it replaced, so it needs its own case rather than riding the matrix.
//
// On linux /proc/<pid>/stat stays readable at state=Z with the ORIGINAL starttime
// intact, and kill -0 still succeeds for a zombie, so the recorded token byte-matches a
// process that has already exited. Without an explicit zombie guard the peer passes the
// send gate, survives prune and keeps receiving broadcasts, while nothing is listening.
// The argv clause this replaced read a zombie DEAD for free (a zombie's cmdline is
// empty), and the port pinned that in TestArgvClauseZombieDead, deleted with the clause
// itself — so this test is where that pinned edge now lives, not a new requirement.
//
// darwin degenerates here in the B4 sense: it is already safe, but by accident rather
// than by the guard. proc_pidinfo errors for a zombie, so procStartTime fails and R2's
// probe-error-is-dead rule catches it. The guard is what makes the behavior INTENDED on
// both platforms instead of load-bearing on a libproc implementation detail.
func TestPredicateStructuralZombieReadsDead(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "meta.json")

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Wait() }) // reap at the end, never before the assertion

	// capture the token while the process is genuinely ALIVE — this is what armMeta
	// would have recorded, so the fixture is what a real armed follower leaves behind.
	start, err := procStartTime(pid)
	if err != nil {
		t.Fatalf("procStartTime on a live child: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}

	// wait for it to become a zombie: exited, not reaped, kill -0 still succeeding.
	// two independent zombie signals, because neither is portable alone: linux reports
	// state=Z via procZombie, while a darwin zombie's args EINVAL out of KERN_PROCARGS2
	// (procZombie there is a documented fail-open that never fires).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, argvErr := procArgs(pid); procZombie(pid) || argvErr != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pidAlive(pid) {
		t.Skip("child was reaped before the zombie window could be asserted (kernel timing)")
	}

	if err := os.WriteFile(metaPath,
		[]byte(fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, pid, start)), 0o644); err != nil {
		t.Fatal(err)
	}
	if MetaListenerAlive(metaPath) {
		t.Error("a zombie listener must read DEAD — its token still matches, but nothing is listening")
	}
	if !PeerDead(metaPath) {
		t.Error("a zombie listener's peer must be PeerDead, or prune and the send gate will treat it as live")
	}
}
