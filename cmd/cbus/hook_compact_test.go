package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cbusRunner builds the REAL binary once and returns a runner for it. Env is rebuilt
// from scratch rather than appended to os.Environ(), so the developer's own live
// CLAUDE_CODE_SESSION_ID / CBUS_DIR cannot leak into a case (this test would otherwise
// broadcast into the real bus).
func cbusRunner(t *testing.T) (func(sid, stdin string, args ...string) (string, string, int), string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cbus")
	if out, err := exec.Command("go", "build", "-o", bin, "claudebus/cmd/cbus").CombinedOutput(); err != nil {
		t.Fatalf("build cbus: %v\n%s", err, out)
	}
	root := t.TempDir()
	run := func(sid, stdin string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		env := []string{"CBUS_DIR=" + root}
		for _, kv := range os.Environ() {
			switch strings.SplitN(kv, "=", 2)[0] {
			case "CBUS_DIR", "CLAUDE_CODE_SESSION_ID", "CBUS_UPDATE_CHECK":
			default:
				env = append(env, kv)
			}
		}
		if sid != "" {
			env = append(env, "CLAUDE_CODE_SESSION_ID="+sid)
		}
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run %v: %v", args, err)
			}
			code = ee.ExitCode()
		}
		return out.String(), errb.String(), code
	}
	return run, root
}

// lastCompactEvent returns the final compact-* line in a peer's inbox.
func lastCompactEvent(t *testing.T, root, ch, al string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ch, al, "inbox.jsonl"))
	if err != nil {
		t.Fatalf("read %s/%s inbox: %v", ch, al, err)
	}
	var last map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]string
		if line == "" || json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if strings.HasPrefix(m["event"], "compact-") {
			last = m
		}
	}
	if last == nil {
		t.Fatalf("no compact event in %s/%s inbox:\n%s", ch, al, b)
	}
	return last
}

// TestHookCompactThroughTheRealBinary drives the SHIPPED entry point end to end: it
// builds cbus, joins a watcher and a compacting session through the CLI, then feeds
// `cbus hook-compact` the literal PreCompact/PostCompact payloads from the hooks
// reference — WITHOUT CLAUDE_CODE_SESSION_ID in the env, which is the real hook's
// situation (the id arrives on stdin only). An in-process test cannot catch a verb that
// never reaches dispatch, a nonzero exit (rc 2 would block compaction), or a stray
// stdout line that Claude Code parses as hook JSON on exit 0.
func TestHookCompactThroughTheRealBinary(t *testing.T) {
	run, root := cbusRunner(t)

	if _, errb, rc := run("WATCHERSID", "", "join", "zig", "watcher"); rc != 0 {
		t.Fatalf("join watcher rc=%d: %s", rc, errb)
	}
	if _, errb, rc := run("SELFSID", "", "join", "zig", "worker"); rc != 0 {
		t.Fatalf("join worker rc=%d: %s", rc, errb)
	}

	preIn := `{"session_id":"SELFSID","transcript_path":"/x/t.jsonl","cwd":"/x","hook_event_name":"PreCompact","trigger":"auto","custom_instructions":""}`
	out, errb, rc := run("", preIn, "hook-compact", "pre")
	if rc != 0 {
		t.Errorf("hook-compact pre rc=%d (rc 2 would BLOCK compaction), stderr=%s", rc, errb)
	}
	if out != "" {
		t.Errorf("hook-compact wrote stdout %q — exit-0 stdout is parsed as hook JSON", out)
	}
	got := lastCompactEvent(t, root, "zig", "watcher")
	if got["kind"] != "presence" || got["event"] != "compact-pre" {
		t.Errorf("kind/event = %q/%q, want presence/compact-pre", got["kind"], got["event"])
	}
	if want := "about to compact (auto), in-context state will be lost"; got["text"] != want {
		t.Errorf("text = %q, want %q", got["text"], want)
	}
	if got["from"] != "zig/worker" || got["to"] != "zig/watcher" {
		t.Errorf("from/to = %q/%q", got["from"], got["to"])
	}

	postIn := `{"session_id":"SELFSID","transcript_path":"/x/t.jsonl","cwd":"/x","hook_event_name":"PostCompact","trigger":"manual","compact_summary":"SUMMARYLEAK"}`
	if out, errb, rc := run("", postIn, "hook-compact", "post"); rc != 0 || out != "" {
		t.Errorf("hook-compact post rc=%d stdout=%q stderr=%s", rc, out, errb)
	}
	got = lastCompactEvent(t, root, "zig", "watcher")
	if got["event"] != "compact-post" {
		t.Errorf("event = %q, want compact-post", got["event"])
	}
	if want := "compacted (manual), in-context state was reset"; got["text"] != want {
		t.Errorf("text = %q, want %q", got["text"], want)
	}
	b, err := os.ReadFile(filepath.Join(root, "zig", "watcher", "inbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SUMMARYLEAK") {
		t.Errorf("compact_summary reached a peer inbox:\n%s", b)
	}

	// the compacting session stays joined and never notifies itself
	if _, err := os.Stat(filepath.Join(root, "zig", "worker", "meta.json")); err != nil {
		t.Errorf("hook-compact deregistered the session: %v", err)
	}
	if wb, err := os.ReadFile(filepath.Join(root, "zig", "worker", "inbox.jsonl")); err == nil && strings.Contains(string(wb), "compact-") {
		t.Errorf("session notified itself:\n%s", wb)
	}

	// a mis-wired hook must be diagnosable WITHOUT failing the session
	out, errb, rc = run("", preIn, "hook-compact", "PreCompact")
	if rc != 0 || out != "" {
		t.Errorf("bad phase: rc=%d stdout=%q, want rc 0 and no stdout", rc, out)
	}
	if !strings.Contains(errb, "pre|post") {
		t.Errorf("bad phase stderr = %q, want a diagnosable message", errb)
	}
	// and a missing phase behaves the same way
	if out, errb, rc := run("", preIn, "hook-compact"); rc != 0 || out != "" || !strings.Contains(errb, "pre|post") {
		t.Errorf("missing phase: rc=%d stdout=%q stderr=%q", rc, out, errb)
	}
}
