package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestPeerMetaRoundTripsLossless is the B2 guard at the struct level. Every rewriter
// (armMeta, renameMeta) unmarshals meta.json into peerMeta and marshals it back, and
// encoding/json silently drops anything the struct does not declare. A field missing
// from peerMeta is therefore not a cosmetic omission: it is deleted from a live peer's
// meta by the next rewrite that touches it.
//
// For listenerStart specifically that would strip an armed peer down to the TRANSITION
// branch, where a follower armed by THIS binary has no inbox in its argv and reads
// DEAD. A peer would go quietly off the bus mid-session.
func TestPeerMetaRoundTripsLossless(t *testing.T) {
	full := peerMeta{
		Alias: "a", Channel: "c", SessionID: "sid", Cwd: "/w",
		ListenerPid: json.RawMessage("4242"), OwnerPid: json.RawMessage("99"),
		ListenerStart: "1784436015.326553",
		Host:          "h", TS: "2026-07-18T00:00:00Z",
		LastActivity: "2026-07-18T00:00:01Z",
		Origin:       "spawned", Model: "opus",
	}
	first, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	var back peerMeta
	if err := json.Unmarshal(first, &back); err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("peerMeta lost data on round trip:\n first  %s\n second %s", first, second)
	}
	if back.ListenerStart != full.ListenerStart {
		t.Errorf("listenerStart did not survive: %q", back.ListenerStart)
	}
}

// TestArmMetaRecordsIdentityAndKeepsTheRest: arming writes the structural witness and
// leaves every field it does not own alone. The birth record is the one that has been
// silently dropped before (cbus-m9l), so it is asserted by name rather than by count.
func TestArmMetaRecordsIdentityAndKeepsTheRest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	dir := filepath.Join(root, "ch", "peer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dir, "meta.json")
	body := `{"alias":"peer","channel":"ch","sessionId":"sid","cwd":"/w",` +
		`"listenerPid":null,"ownerPid":null,"host":"h","ts":"2026-07-18T00:00:00Z",` +
		`"lastActivity":"2026-07-18T00:00:00Z","origin":"spawned","model":"opus"}`
	if err := os.WriteFile(metaPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	armMeta(metaPath)
	after := readRawMeta(t, metaPath)

	want, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(self): %v", err)
	}
	if got := fmt.Sprint(after["listenerStart"]); got != want {
		t.Errorf("listenerStart = %q, want this process's token %q", got, want)
	}
	if fmt.Sprint(after["listenerPid"]) != fmt.Sprint(os.Getpid()) {
		t.Errorf("listenerPid = %v, want %d", after["listenerPid"], os.Getpid())
	}
	for k, want := range map[string]string{
		"alias": "peer", "channel": "ch", "sessionId": "sid", "cwd": "/w",
		"host": "h", "ts": "2026-07-18T00:00:00Z", "origin": "spawned", "model": "opus",
	} {
		if got, present := after[k]; !present {
			t.Errorf("armMeta DROPPED %q", k)
		} else if fmt.Sprint(got) != want {
			t.Errorf("armMeta changed %q: %v, want %q", k, got, want)
		}
	}
}

// TestArmedPeerReadsAliveEndToEnd closes the loop the unit matrix cannot: it arms a
// real meta as THIS process and asks the real predicate. If armMeta and
// listenerIdentityHolds ever disagree about the token's spelling, every unit test
// still passes (each is self-consistent) and every armed peer reads dead in the field.
// The single composer exists to make that impossible; this asserts it.
func TestArmedPeerReadsAliveEndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	dir := filepath.Join(root, "ch", "peer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(metaPath, []byte(
		`{"alias":"peer","channel":"ch","sessionId":"sid","cwd":"/w",`+
			`"listenerPid":null,"ownerPid":null,"host":"h","ts":"2026-07-18T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	armMeta(metaPath)
	if !MetaListenerAlive(metaPath) {
		t.Fatal("a peer armed by this process must read alive through the structural branch")
	}
	if PeerDead(metaPath) {
		t.Error("a freshly armed peer must not be PeerDead")
	}
}
