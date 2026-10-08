package client

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

const scanSid = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"

func TestParseResumedSids(t *testing.T) {
	ps := strings.Join([]string{
		"  100 node /opt/bin/ccs beta --resume " + scanSid,
		"  101 /opt/bin/claude --resume " + scanSid,
		"  102 /opt/bin/claude -r 11111111-2222-3333-4444-555555555555 --name x",
		"  103 /opt/bin/claude --resume=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"  104 grep -r pattern .",
		"  105 /opt/bin/claude --resume",
		"  106 /opt/bin/claude --resume not-a-session-id",
		"  107 /opt/bin/claude --resume 99999999-8888-7777-6666-555555555555",
		"garbage",
	}, "\n")
	got := parseResumedSids(ps, 107)
	want := map[string]int{
		scanSid:                                100,
		"11111111-2222-3333-4444-555555555555": 102,
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee": 103,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for sid, pid := range want {
		if got[sid] != pid {
			t.Errorf("%s -> %d, want %d", sid, got[sid], pid)
		}
	}
}

// the real ps path finds a live process by the sid in its argv
func TestResumedSidsSeesARealProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no argv read on windows")
	}
	// a sid per run: a fixed one could match a real session on the machine.
	// "; :" keeps sh resident; a lone command is exec'd and loses this argv
	sid := fmt.Sprintf("%08x-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffff, os.Getpid())
	cmd := exec.Command("/bin/sh", "-c", "sleep 30; :", "holder", "--resume", sid)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if pid, ok := resumedSids()[sid]; ok {
			if pid != cmd.Process.Pid {
				t.Errorf("pid %d, want the holder %d", pid, cmd.Process.Pid)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a process resuming the sid was not seen")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func stubResumedSids(t *testing.T, held map[string]int) {
	orig := resumedSids
	resumedSids = func() map[string]int { return held }
	t.Cleanup(func() { resumedSids = orig })
}

// an anchor resumed by hand, never joined: resume must refuse instead of
// launching a second process on the transcript
func TestResumeAnchorRefusesAProcessHeldSession(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CBUS_HOST", "host-a")
	stubResumedSids(t, map[string]int{"sid-anchor": 4242})
	fk := &recForker{}
	_, _, err := ResumeAnchor(resumeFixture(), "", fk)
	if err == nil || !strings.Contains(err.Error(), "pid 4242") {
		t.Fatalf("err = %v, want a refusal naming pid 4242", err)
	}
	if len(fk.specs) != 0 {
		t.Fatalf("forked %d times for a session already open", len(fk.specs))
	}
}

func TestPlanRefusesResumeOfAProcessHeldSession(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	stubResumedSids(t, map[string]int{"sid-coder": 4343})
	w, err := GatherPlanWorld("dd")
	if err != nil {
		t.Fatal(err)
	}
	if at := w.LiveSids["sid-coder"]; !strings.Contains(at, "pid 4343") {
		t.Errorf("LiveSids[sid-coder] = %q, want the holder pid", at)
	}
}

// a bus holder keeps its address; the process scan only fills gaps
func TestAddResumedSidsKeepsBusHolder(t *testing.T) {
	live := map[string]string{"a": "ch/alias"}
	addResumedSids(live, map[string]int{"a": 1, "b": 2})
	if live["a"] != "ch/alias" || !strings.Contains(live["b"], "pid 2") {
		t.Errorf("live = %v", live)
	}
}
