package client

import (
	"os"
	"testing"
	"time"
)

// The cost of the identity check and the cursor write, measured rather than described.
// The proposal claimed "one small read per second" and "a quiet follower writes
// nothing"; these are the numbers behind those claims, and they are the ones reported
// upstream.
//
// Run: go test ./internal/client/ -run XXX -bench 'BenchmarkIdentity|BenchmarkCursor' -benchtime 2000x

func BenchmarkIdentityCheck(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("CBUS_DIR", dir)
	peer, _, meta, id := armedPeerB(b, dir)
	_ = peer
	_ = meta
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if c := id.check(); c.cause != stillListener {
			b.Fatalf("unexpected cause %d", c.cause)
		}
	}
}

func BenchmarkCursorWrite(b *testing.B) {
	dir := b.TempDir()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writeCursor(dir, 1, 2, int64(i))
	}
}

// armedPeerB is armedPeer for a benchmark (testing.TB has no t.Helper equivalence in
// the shared helper's signature).
func armedPeerB(b *testing.B, root string) (peer, inbox, meta string, id *listenerIdentity) {
	b.Helper()
	peer = root + "/ch/al"
	if err := os.MkdirAll(peer, 0o755); err != nil {
		b.Fatal(err)
	}
	inbox = peer + "/inbox.jsonl"
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		b.Fatal(err)
	}
	meta = peer + "/meta.json"
	if err := os.WriteFile(meta, []byte(
		`{"alias":"al","channel":"ch","sessionId":"sid","cwd":"/w","listenerPid":null,"ownerPid":null,"host":"h","ts":"2026-07-19T00:00:00Z"}`), 0o644); err != nil {
		b.Fatal(err)
	}
	start, err := procStartTime(os.Getpid())
	if err != nil {
		b.Fatal(err)
	}
	armMeta(meta, start)
	return peer, inbox, meta, &listenerIdentity{pid: os.Getpid(), start: start, metaPath: meta}
}

// TestQuietFollowerWritesNothing pins the claim that idling is free. The cursor write
// is gated on the offset having MOVED, so a follower on a silent inbox must leave the
// sidecar's mtime untouched no matter how many poll ticks elapse. If this ever fails,
// every armed session is rewriting a file once a second forever.
func TestQuietFollowerWritesNothing(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	peer, inbox, _, id := armedPeer(t, "ch", "al")

	_, running, stopJoin := startFollowAs(t, inbox, resumePoint{}, id)
	defer stopJoin()
	appendLine(t, inbox, "p", "q", "one")
	waitFor(t, func() bool {
		_, _, off, st := readCursor(peer)
		return st == cursorValid && off > 0
	}, "the initial cursor")

	first, err := os.Stat(cursorPath(peer))
	if err != nil {
		t.Fatal(err)
	}
	// many poll ticks with nothing arriving (test poll is 3ms, so this is ~200 ticks)
	time.Sleep(600 * time.Millisecond)
	if !running() {
		t.Fatal("the follower stopped; this measures an idle LIVE follower")
	}
	second, err := os.Stat(cursorPath(peer))
	if err != nil {
		t.Fatal(err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Errorf("an idle follower rewrote the cursor (%v -> %v); the write must be gated on the offset moving",
			first.ModTime(), second.ModTime())
	}
}
