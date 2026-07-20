package client

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claudebus/internal/core"
)

// ---- inbox path + the surviving transition needle -------------------------------

// TestInboxPathIsCleaned: the bash-era raw concatenation is gone with the argv it fed.
// InboxPath is now just a path, so it may be cleaned like any other.
func TestInboxPathIsCleaned(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CBUS_DIR", base+"/") // trailing slash: the old raw form kept the '//'
	got := InboxPath("go-port", "coder")
	want := filepath.Join(base, "go-port", "coder", "inbox.jsonl")
	if got != want {
		t.Errorf("InboxPath = %q, want the cleaned %q", got, want)
	}
}

// ---- rotation predicate + reopen retry -------------------------------------------

// TestRotatedPredicate exercises the dev+ino-change and size-regression branches.
func TestRotatedPredicate(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	sa, _ := os.Stat(a)
	sb, _ := os.Stat(b)
	devA, inoA, _ := statDevIno(sa)

	if rotated(devA, inoA, 0, sa) {
		t.Error("same file, no size regression: must NOT be rotated")
	}
	if !rotated(devA, inoA, 0, sb) {
		t.Error("different inode (rm+recreate): must be rotated")
	}
	n := sa.Size()
	if rotated(devA, inoA, n, sa) {
		t.Error("size == consumed: not a regression")
	}
	if !rotated(devA, inoA, n+1, sa) {
		t.Error("size < consumed (truncate): must be rotated")
	}
}

// TestReopenUntilSuccess proves the ruled vanish-race fix: reopen blocks while the
// path is missing and returns a fresh fd (reading from byte 0) once it appears.
func TestReopenUntilSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.jsonl")
	stop := make(chan struct{})
	type res struct {
		f  *os.File
		ok bool
	}
	ch := make(chan res, 1)
	go func() {
		f, ok := reopenUntilSuccess(path, 2*time.Millisecond, stop)
		ch <- res{f, ok}
	}()
	time.Sleep(20 * time.Millisecond) // still missing — reopen must be spinning, not returning
	select {
	case <-ch:
		t.Fatal("reopenUntilSuccess returned while the path was still missing")
	default:
	}
	if err := os.WriteFile(path, []byte("line-from-zero\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-ch:
		if !r.ok || r.f == nil {
			t.Fatal("reopen did not succeed after the file appeared")
		}
		defer r.f.Close()
		if off, _ := r.f.Seek(0, 1); off != 0 {
			t.Fatalf("reopened fd starts at offset %d, want 0", off)
		}
	case <-time.After(time.Second):
		close(stop)
		t.Fatal("reopen did not return after the file appeared")
	}
}

// ---- follower loop: replay tri-state, rotation, never-self-exit -------------------

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startFollow runs follow in a goroutine with a fast poll and returns the output
// sink plus a stop+join func. running() reports whether the loop is still alive.
func startFollow(t *testing.T, inbox string, resume resumePoint) (buf *syncBuf, running func() bool, stopJoin func()) {
	return startFollowAs(t, inbox, resume, nil)
}

// startFollowAs is startFollow with an explicit identity, for the dormancy tests.
func startFollowAs(t *testing.T, inbox string, resume resumePoint, id *listenerIdentity) (buf *syncBuf, running func() bool, stopJoin func()) {
	t.Helper()
	buf = &syncBuf{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); follow(inbox, resume, id, buf, 3*time.Millisecond, stop) }()
	running = func() bool {
		select {
		case <-done:
			return false
		default:
			return true
		}
	}
	stopJoin = func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("follow did not stop after close(stop)")
		}
	}
	return buf, running, stopJoin
}

func appendLine(t *testing.T, inbox, from, to, text string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"from": from, "to": to, "ts": "t", "text": text})
	f, err := os.OpenFile(inbox, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func frameOf(from, to, text string) string {
	b, _ := json.Marshal(map[string]string{"from": from, "to": to, "ts": "t", "text": text})
	return string(core.LocalEmit(b))
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

func writeInbox(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return inbox
}

// TestFollowFirstArmReplaysFromZero: a first arm (no cursor, never armed) replays the whole
// inbox — everything queued since join (which truncated it) is delivered.
func TestFollowFirstArmReplaysFromZero(t *testing.T) {
	inbox := writeInbox(t, filepath.Join(t.TempDir(), "ch", "al"))
	appendLine(t, inbox, "ch/orch", "ch/al", "prelude one")
	appendLine(t, inbox, "ch/orch", "ch/al", "prelude two")

	buf, _, stopJoin := startFollow(t, inbox, resumePoint{})
	defer stopJoin()
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "prelude one")) &&
			strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "prelude two"))
	}, "first-arm replay of both prelude lines")

	appendLine(t, inbox, "ch/orch", "ch/al", "live three")
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "live three"))
	}, "live append after replay")
}

// TestFollowReArmSeeksEnd: the migration path (seekEnd) replays NOTHING — only appends
// after the arm are delivered.
func TestFollowReArmSeeksEnd(t *testing.T) {
	inbox := writeInbox(t, filepath.Join(t.TempDir(), "ch", "al"))
	appendLine(t, inbox, "ch/orch", "ch/al", "old prelude")

	buf, _, stopJoin := startFollow(t, inbox, resumePoint{seekEnd: true})
	defer stopJoin()
	time.Sleep(40 * time.Millisecond) // give the loop time to (not) replay
	if got := buf.String(); got != "" {
		t.Fatalf("re-arm must not replay the prelude; got %q", got)
	}
	appendLine(t, inbox, "ch/orch", "ch/al", "new live")
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "new live"))
	}, "re-arm live append")
	if strings.Contains(buf.String(), "old prelude") {
		t.Fatal("re-arm leaked the pre-arm prelude")
	}
}

// TestFollowRotationTruncate: a truncate-in-place (same inode, size < consumed —
// os.WriteFile's O_TRUNC) is followed by reopening from byte 0.
func TestFollowRotationTruncate(t *testing.T) {
	inbox := writeInbox(t, filepath.Join(t.TempDir(), "ch", "al"))
	for i := 0; i < 5; i++ {
		appendLine(t, inbox, "ch/orch", "ch/al", strings.Repeat("long-original-", 8))
	}
	buf, _, stopJoin := startFollow(t, inbox, resumePoint{})
	defer stopJoin()
	waitFor(t, func() bool {
		return strings.Count(buf.String(), "◀ cbus end from=ch/orch") == 5
	}, "all 5 original frames")

	// truncate to a single SHORT line — size stays below consumed forever, so the
	// size-regression branch fires deterministically.
	short, _ := json.Marshal(map[string]string{"from": "ch/orch", "to": "ch/al", "ts": "t", "text": "after-truncate"})
	if err := os.WriteFile(inbox, append(short, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "after-truncate"))
	}, "post-truncate reopen delivers the new line")
}

// TestFollowRotationRecreate: an rm+recreate (new inode — the rejoin path) is
// followed by reopening; the follower never self-exits during the vanish window.
func TestFollowRotationRecreate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ch", "al")
	inbox := writeInbox(t, dir)
	appendLine(t, inbox, "ch/orch", "ch/al", "before recreate")
	buf, running, stopJoin := startFollow(t, inbox, resumePoint{})
	defer stopJoin()
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "before recreate"))
	}, "pre-recreate line")

	if err := os.Remove(inbox); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if !running() {
		t.Fatal("follower self-exited on a vanished inbox — must keep polling")
	}
	// recreate with fresh content (new inode).
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	appendLine(t, inbox, "ch/orch", "ch/al", "after recreate")
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "after recreate"))
	}, "post-recreate reopen delivers new content")
}

// TestFollowDirDeletionNeverExits (ruled at P2.4 review): removing the WHOLE alias dir
// (not just the inbox file) under a running follower must NOT self-exit — it keeps the
// unlinked fd and polls (os.Stat -> ENOENT -> continue), then reopens once the dir +
// inbox are recreated (reopenUntilSuccess retries os.Open while the parent dir is
// absent). Complements TestFollowRotationRecreate (file-only rm).
func TestFollowDirDeletionNeverExits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ch", "al")
	inbox := writeInbox(t, dir)
	appendLine(t, inbox, "ch/orch", "ch/al", "before dirdel")
	buf, running, stopJoin := startFollow(t, inbox, resumePoint{})
	defer stopJoin()
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "before dirdel"))
	}, "pre-dirdel line")

	if err := os.RemoveAll(dir); err != nil { // remove the WHOLE alias dir
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if !running() {
		t.Fatal("follower self-exited on whole-dir deletion — must keep polling")
	}
	// recreate dir + inbox (new inode under a re-made parent) and append.
	inbox2 := writeInbox(t, dir)
	appendLine(t, inbox2, "ch/orch", "ch/al", "after dirdel")
	waitFor(t, func() bool {
		return strings.Contains(buf.String(), frameOf("ch/orch", "ch/al", "after dirdel"))
	}, "post-dirdel reopen delivers new content")
	if !running() {
		t.Fatal("follower died after recreate")
	}
}

// ---- arm: meta record + tri-state decision ---------------------------------------

// TestArmMetaRecordsListenerAndGrace: armMeta stamps listenerPid=own pid and
// refreshes lastActivity (D3), preserving every other field; and the tri-state
// decision then reads a recorded pid as a re-arm.
func TestArmMetaRecordsListenerAndGrace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	alDir := filepath.Join(dir, "ch", "al")
	if err := os.MkdirAll(alDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(alDir, "meta.json")
	// a join-shaped meta: listenerPid null, a stale lastActivity.
	orig := `{
  "alias": "al",
  "channel": "ch",
  "sessionId": "sess-xyz",
  "cwd": "/work",
  "listenerPid": null,
  "ownerPid": null,
  "host": "mbp",
  "ts": "2020-01-01T00:00:00Z",
  "lastActivity": "2020-01-01T00:00:00Z"
}`
	if err := os.WriteFile(metaPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	// tri-state BEFORE arm: null listenerPid -> first arm.
	if pm, ok := ReadPeerMeta(metaPath); !ok || pm.ListenerPid != 0 {
		t.Fatalf("pre-arm listenerPid should read 0 (null), got %d ok=%v", pm.ListenerPid, ok)
	}

	armMeta(metaPath, selfStart(t))

	pm, ok := ReadPeerMeta(metaPath)
	if !ok || pm.ListenerPid != os.Getpid() {
		t.Fatalf("armMeta must record listenerPid=own pid %d, got %d", os.Getpid(), pm.ListenerPid)
	}
	// preserved fields + refreshed lastActivity.
	var m map[string]any
	b, _ := os.ReadFile(metaPath)
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"alias": "al", "channel": "ch", "sessionId": "sess-xyz", "cwd": "/work", "host": "mbp", "ts": "2020-01-01T00:00:00Z"} {
		if got, _ := m[k].(string); got != want {
			t.Errorf("field %q = %q, want preserved %q", k, got, want)
		}
	}
	if la, _ := m["lastActivity"].(string); la == "2020-01-01T00:00:00Z" || la == "" {
		t.Errorf("lastActivity must be refreshed at arm (D3), got %q", la)
	}

	// tri-state AFTER arm: a recorded pid -> re-arm.
	if pm2, _ := ReadPeerMeta(metaPath); pm2.ListenerPid == 0 {
		t.Fatal("post-arm listenerPid should be non-zero (re-arm signal)")
	}
}

// TestArmMetaBestEffortMissing: a missing meta.json is a no-op (bash `jset || true`),
// not a create — armMeta must not panic or fabricate a file.
func TestArmMetaBestEffortMissing(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "meta.json")
	armMeta(metaPath, selfStart(t)) // must not panic
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatalf("armMeta fabricated a meta for an absent file: err=%v", err)
	}
}

// selfStart is this process's structural witness, the token armMeta records.
func selfStart(t *testing.T) string {
	t.Helper()
	s, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self): %v", err)
	}
	return s
}
