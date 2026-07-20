package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// M5.2(a) rig. Freezes the RENDERED BYTES of list / list --active / active / channels
// as they stand before the M5.2(b) refactor, which routes the text renderer and the
// --json encoder through one traversal. Anything that moves these strings afterwards
// is a behavior change wearing a refactor's clothes, and this test is how the reviewer
// tells the two apart without reading the diff.
//
// HARNESS EXCEPTION to the never-run-`cbus tail`-under-Bash doctrine, the same one
// TestTailArmsAndStreamsInProcess takes: the follower runs as a CHILD process with a
// kill on cleanup, so it cannot wedge this test the way a live session's blocking tail
// would. It is here rather than avoided because a `listen` row is only reachable
// through a real arm — MetaListenerAlive wants a live pid whose start time matches the
// recorded witness, and no hand-written meta can honestly produce that pair.
func TestListRenderingGolden(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cbus")
	if out, err := exec.Command("go", "build", "-o", bin, "claudebus/cmd/cbus").CombinedOutput(); err != nil {
		t.Fatalf("build cbus: %v\n%s", err, out)
	}
	root := t.TempDir()
	workdir := t.TempDir()

	env := func(sid string) []string {
		e := []string{"CBUS_DIR=" + root, "CLAUDE_CODE_SESSION_ID=" + sid}
		for _, kv := range os.Environ() {
			switch strings.SplitN(kv, "=", 2)[0] {
			case "CBUS_DIR", "CLAUDE_CODE_SESSION_ID", "CBUS_UPDATE_CHECK":
			default:
				e = append(e, kv)
			}
		}
		return e
	}
	cbus := func(sid string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env(sid)
		cmd.Dir = workdir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cbus %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	// real peers, written by the real Join — never hand-staged (doctrine 12)
	cbus("sid-one", "join", "alpha", "one")
	cbus("sid-two", "join", "alpha", "two")
	cbus("sid-solo", "join", "beta", "solo")

	// a legacy v1 entry: a meta.json directly at the CHANNEL level. This one IS
	// hand-staged, and that is the historically-reachable carve-out rather than a
	// violation: only the retired bash v1 client ever wrote this shape, when the bus
	// was flat and a peer lived at $CBUS_DIR/<alias>/meta.json with no channel above it
	// (docs/architecture/protocol.md:139, command-reference.md:1707). No Go path writes
	// it and none ever will, so a fixture is the only way to render the row that list
	// still carries for it.
	if err := os.MkdirAll(filepath.Join(root, "zlegacy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "zlegacy", "meta.json"),
		[]byte(`{"alias":"zlegacy","sessionId":"v1","listenerPid":null,"host":"old","cwd":"/w","ts":"2026-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// one genuinely armed listener, so the `listen` branch is pinned by a real arm
	tail := exec.Command(bin, "tail", "alpha/one")
	tail.Env = env("sid-one")
	tail.Dir = workdir
	if err := tail.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tail.Process.Kill()
		_, _ = tail.Process.Wait()
	})
	// wait on the META, never on the rendering: the arm is asynchronous, but polling
	// `list` for the word "listen" would make a broken liveness RENDERER look like a
	// slow arm, and the test would then die on a precondition instead of on the golden
	// it exists to check.
	waitArmed(t, filepath.Join(root, "alpha", "one", "meta.json"))

	norm := normalizer(t, tail.Process.Pid, workdir)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"list", []string{"list"}, listGolden},
		{"list --active", []string{"list", "--active"}, activeGolden},
		{"active", []string{"active"}, activeGolden},
		{"list alpha", []string{"list", "alpha"}, listAlphaGolden},
		{"channels", []string{"channels"}, channelsGolden},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := norm(cbus("sid-two", c.args...))
			if got != c.want {
				t.Errorf("cbus %s rendered:\n%s\nwant:\n%s", strings.Join(c.args, " "), got, c.want)
			}
		})
	}

	// the two empty-store messages, which no populated fixture can reach
	empty := t.TempDir()
	emptyEnv := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(env("sid-none"), "CBUS_DIR="+empty)
		cmd.Dir = workdir
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if got := emptyEnv("list"); got != "no peers registered\n" {
		t.Errorf("empty list = %q", got)
	}
	if got := emptyEnv("list", "--active"); got != "no active listeners\n" {
		t.Errorf("empty list --active = %q", got)
	}
	if got := emptyEnv("channels"); got != "no channels\n" {
		t.Errorf("empty channels = %q", got)
	}
}

// waitArmed blocks until the peer's meta records a listener with a structural witness,
// which is what MetaListenerAlive judges. Deliberately not a `list` poll — see the call
// site.
func waitArmed(t *testing.T, metaPath string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(metaPath); err == nil {
			var m struct {
				ListenerPid   int    `json:"listenerPid"`
				ListenerStart string `json:"listenerStart"`
			}
			if json.Unmarshal(b, &m) == nil && m.ListenerPid != 0 && m.ListenerStart != "" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the tail never wrote a live listener into meta — the fixture, not the renderer, failed")
}

// normalizer replaces the three environment-dependent values in a rendered row (the
// listener pid, this machine's short hostname, the peers' cwd) with fixed tokens. The
// FORMAT is what the golden pins — column widths, spacing, wording, row order — so
// substituting the values is what makes it pinnable at all, not a loosening.
func normalizer(t *testing.T, pid int, workdir string) func(string) string {
	t.Helper()
	host := shortHostnameForTest(t)
	real, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		real = workdir
	}
	return func(s string) string {
		s = strings.ReplaceAll(s, real, "<CWD>")
		s = strings.ReplaceAll(s, workdir, "<CWD>")
		// anchored on the "pid=" prefix, not the bare digits: a bare-number replace
		// would corrupt any other digit in the output that happened to match.
		s = strings.ReplaceAll(s, "pid="+strconv.Itoa(pid), "pid=<PID>")
		return strings.ReplaceAll(s, host, "<HOST>")
	}
}

func shortHostnameForTest(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	if i := strings.IndexByte(h, '.'); i >= 0 {
		h = h[:i]
	}
	return h
}

const listGolden = "listen  alpha/one                    pid=<PID>   <HOST>  <CWD>\noff     alpha/two                    pid=?       <HOST>  <CWD>\noff     beta/solo                    pid=?       <HOST>  <CWD>\noff     zlegacy                      legacy v1 entry — run: cbus prune\n"

const activeGolden = "listen  alpha/one                    pid=<PID>   <HOST>  <CWD>\n"

const listAlphaGolden = "listen  alpha/one                    pid=<PID>   <HOST>  <CWD>\noff     alpha/two                    pid=?       <HOST>  <CWD>\n"

const channelsGolden = "alpha                2 peers (1 listening)\nbeta                 1 peers (0 listening)\n"
