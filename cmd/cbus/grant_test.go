package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"claudebus/internal/client"
)

// fakeTTY plays the operator: it reads the typed lines and records the prompts.
type fakeTTY struct {
	in  io.Reader
	out bytes.Buffer
}

func (f *fakeTTY) Read(p []byte) (int, error)  { return f.in.Read(p) }
func (f *fakeTTY) Write(p []byte) (int, error) { return f.out.Write(p) }
func (f *fakeTTY) Close() error                { return nil }

func operatorTypes(t *testing.T, typed string) *fakeTTY {
	t.Helper()
	tty := &fakeTTY{in: strings.NewReader(typed)}
	oldT, oldS, oldP := grantTerminal, grantStdTTY, grantProvenance
	grantTerminal = func() (io.ReadWriteCloser, string, error) { return tty, "dev=0x10", nil }
	grantStdTTY = func() bool { return true }
	grantProvenance = func(dev string) client.GrantProvenance {
		return client.GrantProvenance{TTY: dev, Ancestors: []string{"-zsh", "login"}}
	}
	t.Cleanup(func() { grantTerminal, grantStdTTY, grantProvenance = oldT, oldS, oldP })
	return tty
}

// storeHash fingerprints every path, mode and byte under the store.
func storeHash(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		info, _ := d.Info()
		line := rel + " " + info.Mode().String()
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			sum := sha256.Sum256(b)
			line += " " + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// grantStore is a scratch store where ch/coder is joined as sid-coder, the session
// a grant for ch/coder binds to.
func grantStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-coder")
	captureStdout(t, func() {
		if rc := run([]string{"join", "ch", "coder"}); rc != 0 {
			t.Fatalf("join ch/coder rc=%d", rc)
		}
	})
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	return root
}

func TestGrantRefusalsWriteNothing(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		setup func(t *testing.T)
		want  string
	}{
		{"no controlling terminal", []string{"ch/coder", "push"}, func(t *testing.T) {
			operatorTypes(t, "ch/coder\npush\n")
			grantTerminal = func() (io.ReadWriteCloser, string, error) { return nil, "", syscall.ENXIO }
		}, "grant needs the operator at a real terminal"},
		{"stdio not a terminal", []string{"ch/coder", "push"}, func(t *testing.T) {
			operatorTypes(t, "ch/coder\npush\n")
			grantStdTTY = func() bool { return false }
		}, "stdin and stdout must be one"},
		{"wrong address typed", []string{"ch/coder", "push"}, func(t *testing.T) { operatorTypes(t, "ch/reviewer\npush\n") }, "did not match"},
		{"wrong action typed", []string{"ch/coder", "push"}, func(t *testing.T) { operatorTypes(t, "ch/coder\npush --force\n") }, "did not match"},
		{"EOF before confirming", []string{"ch/coder", "push"}, func(t *testing.T) { operatorTypes(t, "ch/coder\n") }, "did not match"},
		{"bad target", []string{"coder", "push"}, func(t *testing.T) { operatorTypes(t, "coder\npush\n") }, "<channel>/<alias>"},
		{"remote target", []string{"ch@server/coder", "push"}, func(t *testing.T) { operatorTypes(t, "x\n") }, "local-only"},
		{"zero ttl", []string{"ch/coder", "push", "--ttl", "0s"}, func(t *testing.T) { operatorTypes(t, "ch/coder\npush\n") }, "--ttl must be more than 0"},
		{"negative ttl", []string{"ch/coder", "push", "--ttl", "-5m"}, func(t *testing.T) { operatorTypes(t, "ch/coder\npush\n") }, "--ttl must be more than 0"},
		{"overflowing ttl", []string{"ch/coder", "push", "--ttl", "99999999999h"}, func(t *testing.T) { operatorTypes(t, "ch/coder\npush\n") }, "--ttl: want a duration"},
		{"once with ttl", []string{"ch/coder", "push", "--once", "--ttl", "1h"}, func(t *testing.T) { operatorTypes(t, "ch/coder\npush\n") }, "exclusive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := grantStore(t)
			before := storeHash(t, root)
			c.setup(t)
			var rc int
			stderr := captureStderr(t, func() { rc = run(append([]string{"grant"}, c.args...)) })
			if rc == 0 {
				t.Fatalf("a refusal must exit non-zero")
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("stderr %q does not say %q", stderr, c.want)
			}
			if after := storeHash(t, root); after != before {
				t.Errorf("a refused grant changed the store")
			}
		})
	}
}

func TestGrantConfirmedAtTheTerminalWritesALiveGrant(t *testing.T) {
	root := grantStore(t)
	tty := operatorTypes(t, "ch/coder\npush the branch\n")
	var rc int
	out := captureStdout(t, func() { rc = run([]string{"grant", "ch/coder", "push the branch"}) })
	if rc != 0 || !strings.Contains(out, "granted g-") || !strings.Contains(out, "session sid-coder") {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	for _, want := range []string{"operator grant for ch/coder", "session: sid-coder", "action:  push the branch", "Type the peer address", "Type the action exactly"} {
		if !strings.Contains(tty.out.String(), want) {
			t.Errorf("the terminal prompt is missing %q:\n%s", want, tty.out.String())
		}
	}
	views, err := client.ListGrants(nil, true)
	if err != nil || len(views) != 1 || views[0].State != client.GrantLive || views[0].GrantedBy.TTY != "dev=0x10" {
		t.Fatalf("want one live grant recording the terminal: %+v %v", views, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".grants", "ch", "coder", views[0].ID+".json")); err != nil {
		t.Fatalf("record not at the documented path: %v", err)
	}
}

func TestGrantFromAHarnessIsRecordedSuspectAndFails(t *testing.T) {
	grantStore(t)
	operatorTypes(t, "ch/coder\npush\n")
	grantProvenance = func(dev string) client.GrantProvenance {
		return client.GrantProvenance{TTY: dev, Harness: "claude", HarnessAncestor: true}
	}
	var rc int
	var stdout string
	stderr := captureStderr(t, func() { stdout = captureStdout(t, func() { rc = run([]string{"grant", "ch/coder", "push"}) }) })
	if rc == 0 || !strings.Contains(stderr, "inside a claude process tree") {
		t.Fatalf("rc=%d stderr=%q", rc, stderr)
	}
	if !strings.Contains(stdout, "as SUSPECT (unusable)") || strings.Contains(stdout, "granted") {
		t.Fatalf("a suspect mint must never print the success line, stdout=%q", stdout)
	}
	views, _ := client.ListGrants(nil, true)
	if len(views) != 1 || views[0].State != client.GrantSuspect {
		t.Fatalf("the record must exist and list suspect: %+v", views)
	}
}

func TestGrantsUseAndRevokeThroughTheCLI(t *testing.T) {
	grantStore(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-coder")
	if rc := run([]string{"join", "ch", "coder"}); rc != 0 {
		t.Fatalf("join rc=%d", rc)
	}
	operatorTypes(t, "ch/coder\npush\n")
	captureStdout(t, func() { run([]string{"grant", "ch/coder", "push"}) })
	views, _ := client.ListGrants(nil, true)
	if len(views) != 1 {
		t.Fatalf("want one grant, got %+v", views)
	}
	id := views[0].ID

	if out := captureStdout(t, func() { run([]string{"grants"}) }); !strings.Contains(out, id+"  live") {
		t.Errorf("cbus grants must list it live for its grantee:\n%s", out)
	}
	var rc int
	if out := captureStdout(t, func() { rc = run([]string{"grants", "use", id}) }); rc != 0 || !strings.Contains(out, "using "+id) {
		t.Fatalf("grants use rc=%d out=%q", rc, out)
	}
	if stderr := captureStderr(t, func() { rc = run([]string{"grants", "use", id}) }); rc == 0 || !strings.Contains(stderr, "used, not live") {
		t.Fatalf("a second use must fail: rc=%d %q", rc, stderr)
	}

	operatorTypes(t, "ch/coder\nanother\n")
	captureStdout(t, func() { run([]string{"grant", "ch/coder", "another"}) })
	views, _ = client.ListGrants(nil, true)
	id2 := views[1].ID
	operatorTypes(t, id2+"\n")
	if out := captureStdout(t, func() { rc = run([]string{"grant", "revoke", id2}) }); rc != 0 || !strings.Contains(out, "revoked "+id2) {
		t.Fatalf("revoke rc=%d out=%q", rc, out)
	}
	if stderr := captureStderr(t, func() { rc = run([]string{"grants", "use", id2}) }); rc == 0 || !strings.Contains(stderr, "revoked, not live") {
		t.Fatalf("a revoked grant must not be usable: rc=%d %q", rc, stderr)
	}
}

func TestGrantsUseRefusesANonGrantee(t *testing.T) {
	grantStore(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-reviewer")
	if rc := run([]string{"join", "ch", "reviewer"}); rc != 0 {
		t.Fatalf("join rc=%d", rc)
	}
	operatorTypes(t, "ch/coder\npush\n")
	captureStdout(t, func() { run([]string{"grant", "ch/coder", "push"}) })
	views, _ := client.ListGrants(nil, true)
	var rc int
	if out := captureStdout(t, func() { rc = run([]string{"grants"}) }); rc != 0 || !strings.Contains(out, "no grants") {
		t.Errorf("another alias sees no grants by default: rc=%d %q", rc, out)
	}
	if stderr := captureStderr(t, func() { rc = run([]string{"grants", "use", views[0].ID}) }); rc == 0 || !strings.Contains(stderr, "not for this session") {
		t.Fatalf("a non-grantee must be refused: rc=%d %q", rc, stderr)
	}
	if v, _ := client.ListGrants(nil, true); v[0].State != client.GrantLive {
		t.Fatalf("the refused use must not consume the grant: %+v", v[0])
	}
}

func TestGrantTerminalErrorIsReported(t *testing.T) {
	grantStore(t)
	operatorTypes(t, "")
	grantTerminal = func() (io.ReadWriteCloser, string, error) { return nil, "", errors.New("device not configured") }
	if stderr := captureStderr(t, func() { run([]string{"grant", "revoke", "g-0000000000"}) }); !strings.Contains(stderr, "grant needs the operator at a real terminal") {
		t.Errorf("revoke must take the same gate: %q", stderr)
	}
}

func TestGrantRefusesUnbindableTargets(t *testing.T) {
	root := grantStore(t)
	if _, err := client.ReserveAlias("ch", "fresh", client.OriginFresh, ""); err != nil {
		t.Fatal(err)
	}
	// an armed peer whose listener has gone: the pid no longer runs
	dead := filepath.Join(root, "ch", "dead")
	if err := os.MkdirAll(dead, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"alias":"dead","channel":"ch","sessionId":"sid-dead","listenerPid":2147483646,"listenerStart":"1","ownerPid":null,"host":"h","ts":"2026-10-01T00:00:00Z","lastActivity":"2026-10-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dead, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	before := storeHash(t, root)
	for target, want := range map[string]string{
		"ch/nobody": "no peer ch/nobody is registered",
		"ch/fresh":  "reserved but has not joined",
		"ch/dead":   "listener is dead",
	} {
		operatorTypes(t, target+"\npush\n")
		var rc int
		stderr := captureStderr(t, func() { rc = run([]string{"grant", target, "push"}) })
		if rc == 0 || !strings.Contains(stderr, want) {
			t.Errorf("%s: rc=%d stderr=%q, want %q", target, rc, stderr, want)
		}
	}
	if storeHash(t, root) != before {
		t.Error("a refused grant changed the store")
	}
}

func TestGrantsListingShowsHowEachGrantWasMinted(t *testing.T) {
	grantStore(t)
	mintWith := func(action string, ancestors []string) {
		operatorTypes(t, "ch/coder\n"+action+"\n")
		grantProvenance = func(string) client.GrantProvenance {
			return client.GrantProvenance{TTY: "/dev/ttys004", Ancestors: ancestors}
		}
		captureStdout(t, func() { run([]string{"grant", "ch/coder", action}) })
	}
	mintWith("from-login", []string{"zsh", "?(pid 9: operation not permitted)", "iTermServer-3.7", "iTerm2", "extra"})
	mintWith("from-tmux", []string{"zsh", "tmux"})
	out := captureStdout(t, func() { run([]string{"grants", "--all"}) })
	for _, want := range []string{
		"session sid-coder, minted on /dev/ttys004 via zsh < ?(pid 9: operation not permitted) < iTermServer-3.7 < iTerm2 < ...",
		"session sid-coder, minted on /dev/ttys004 via zsh < tmux\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing must show the minting chain %q:\n%s", want, out)
		}
	}
}
