package client

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadPeerMeta(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, "meta.json")
	_ = os.WriteFile(mp, []byte(`{"listenerPid":1234,"ownerPid":null,"host":"mbp","cwd":"/x","alias":"a"}`), 0o644)
	if m, ok := ReadPeerMeta(mp); !ok || m.ListenerPid != 1234 || m.OwnerPid != 0 || m.Host != "mbp" || m.Cwd != "/x" {
		t.Errorf("meta = %+v ok=%v", m, ok)
	}
	// digit-coerced pid stored as a string is tolerated
	_ = os.WriteFile(mp, []byte(`{"listenerPid":"42"}`), 0o644)
	if m, _ := ReadPeerMeta(mp); m.ListenerPid != 42 {
		t.Errorf("string pid = %d, want 42", m.ListenerPid)
	}
	if _, ok := ReadPeerMeta(filepath.Join(dir, "nope.json")); ok {
		t.Error("missing meta should be not-ok")
	}
}

func TestMetaListenerAlive(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "inbox.jsonl")
	_ = os.WriteFile(inbox, nil, 0o644)
	mp := filepath.Join(dir, "meta.json")
	writeMeta := func(body string) { _ = os.WriteFile(mp, []byte(body), 0o644) }

	// a live process standing in for the follower
	livePid := liveProc(t)
	liveStart := startTokenOf(t, livePid)

	// a SECOND live process: liveProc separates consecutive spawns so the two cannot
	// share a start token, which is what keeps the mismatch case below a mismatch (F2).
	otherPid := liveProc(t)

	deadPid := deadProc(t)

	writeMeta(fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, livePid, liveStart))
	if !MetaListenerAlive(mp) {
		t.Error("a live listener whose recorded witness is its own should be listening")
	}
	writeMeta(`{"listenerPid":null}`)
	if MetaListenerAlive(mp) {
		t.Error("null listenerPid must be off")
	}
	writeMeta(fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, otherPid, liveStart))
	if MetaListenerAlive(mp) {
		t.Error("a live pid wearing another process's witness must be off (recycling guard)")
	}
	writeMeta(fmt.Sprintf(`{"listenerPid":%d}`, deadPid))
	if MetaListenerAlive(mp) {
		t.Error("a dead pid must be off")
	}
	writeMeta(fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q,"ownerPid":%d}`, livePid, liveStart, deadPid))
	if MetaListenerAlive(mp) {
		t.Error("a dead owner must make it off (crash-orphan guard)")
	}
}

func TestPeerDead(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, "meta.json")
	inbox := filepath.Join(dir, "inbox.jsonl")
	_ = os.WriteFile(inbox, nil, 0o644)
	write := func(body string) { _ = os.WriteFile(mp, []byte(body), 0o644) }
	setMtime := func(ago time.Duration) {
		ts := time.Now().Add(-ago)
		_ = os.Chtimes(mp, ts, ts)
	}
	utc := func(ago time.Duration) string { return time.Now().Add(-ago).UTC().Format("2006-01-02T15:04:05Z") }

	livePid := liveProc(t)
	deadPid := deadProc(t)

	write(fmt.Sprintf(`{"listenerPid":%d,"listenerStart":%q}`, livePid, startTokenOf(t, livePid)))
	if PeerDead(mp) {
		t.Error("armed + live listener must NOT be dead")
	}
	write(fmt.Sprintf(`{"listenerPid":%d}`, deadPid))
	if !PeerDead(mp) {
		t.Error("armed + dead listener must be dead")
	}
	write(`{"listenerPid":null}`)
	setMtime(1 * time.Minute)
	if !PeerDead(mp) {
		t.Error("never-armed with no lastActivity stamp must be dead (pre-port relic)")
	}
	write(``)
	if !PeerDead(mp) {
		t.Error("an empty meta must be dead")
	}
	write(`{not json`)
	if !PeerDead(mp) {
		t.Error("an unparseable meta must be dead")
	}
	_ = os.Remove(mp)
	if PeerDead(mp) {
		t.Error("a missing meta must NOT be dead")
	}
	// any RFC3339 form counts as stamped, not just the store's frozen layout
	write(fmt.Sprintf(`{"listenerPid":null,"lastActivity":%q}`, time.Now().UTC().Format("2006-01-02T15:04:05+00:00")))
	if PeerDead(mp) {
		t.Error("a fresh RFC3339-offset lastActivity must NOT be dead")
	}
	write(fmt.Sprintf(`{"listenerPid":null,"lastActivity":%q}`, utc(0)))
	if PeerDead(mp) {
		t.Error("never-armed within the grace window must NOT be dead")
	}
	write(fmt.Sprintf(`{"listenerPid":null,"lastActivity":%q}`, utc(11*time.Minute)))
	if !PeerDead(mp) {
		t.Error("never-armed past the grace window must be dead")
	}
	// mtime must have no influence in either direction (P3: fallback deleted)
	write(fmt.Sprintf(`{"listenerPid":null,"lastActivity":%q}`, utc(0)))
	setMtime(30 * time.Minute)
	if PeerDead(mp) {
		t.Error("a fresh lastActivity must win despite an old mtime")
	}
	write(fmt.Sprintf(`{"listenerPid":null,"lastActivity":%q}`, utc(20*time.Minute)))
	setMtime(1 * time.Minute)
	if !PeerDead(mp) {
		t.Error("an old lastActivity must be dead despite a fresh mtime")
	}
}

// startTokenOf is the structural witness armMeta records for a pid. Every "this peer
// is armed" fixture needs one now: with the transition branch gone, a listenerPid
// with no witness can never read alive, so a fixture without it would be asserting
// the stampless rule by accident instead of whatever it means to assert.
func startTokenOf(t *testing.T, pid int) string {
	t.Helper()
	start, err := procStartTime(pid)
	if err != nil {
		t.Fatalf("procStartTime(%d): %v", pid, err)
	}
	return start
}
