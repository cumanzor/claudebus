package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// ---- large pids (F4) --------------------------------------------------------------
//
// The NUC runs pid_max 4194304, so 7-digit pids are ordinary there. darwin caps at
// 99999 and the CI container stays small, so nothing on a developer machine ever
// produces one. These cases carry that platform difference into a synthetic fixture so
// the class is caught here instead of at a deploy gate.

// largePid is a realistic NUC pid: past the 1e6 point where fmt's %v for float64
// switches to scientific notation.
const largePid = 1548122

// TestReadRawMetaRendersLargePidsExactly is F4 itself, reproduced without needing a
// machine that can allocate such a pid. Before the UseNumber fix this fails everywhere,
// because the pid is in the FIXTURE rather than in the process table.
func TestReadRawMetaRendersLargePidsExactly(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(metaPath, []byte(fmt.Sprintf(
		`{"listenerPid":%d,"ownerPid":4194304,"listenerStart":"1.2"}`, largePid)), 0o644); err != nil {
		t.Fatal(err)
	}
	m := readRawMeta(t, metaPath)
	if got, want := fmt.Sprint(m["listenerPid"]), fmt.Sprint(largePid); got != want {
		t.Errorf("listenerPid rendered %q, want %q — a decode path is going through float64", got, want)
	}
	if got := fmt.Sprint(m["ownerPid"]); got != "4194304" {
		t.Errorf("ownerPid rendered %q, want 4194304", got)
	}
}

// TestFloatDecodeThresholdIsBelowRealPids documents WHY this only ever showed on the
// NUC, and pins the boundary so the next person does not have to rediscover it: the
// float64 path is exact for every pid darwin can allocate and lossy from 1e6 up, which
// is squarely inside the NUC's range.
func TestFloatDecodeThresholdIsBelowRealPids(t *testing.T) {
	for _, tc := range []struct {
		pid       int
		wantExact bool
	}{
		{62137, true},    // typical darwin pid
		{99999, true},    // darwin pid_max
		{999999, true},   // last exact value
		{1000000, false}, // %v flips to scientific notation here
		{largePid, false},
		{4194304, false}, // NUC pid_max
	} {
		var m map[string]any
		body := fmt.Sprintf(`{"pid":%d}`, tc.pid)
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatal(err)
		}
		exact := fmt.Sprint(m["pid"]) == fmt.Sprint(tc.pid)
		if exact != tc.wantExact {
			t.Errorf("pid %d: plain-float64 decode exact=%v, want %v (rendered %q)",
				tc.pid, exact, tc.wantExact, fmt.Sprint(m["pid"]))
		}
	}
}

// TestReadPeerMetaHandlesLargePids pins that PRODUCTION is size-independent. F4 was
// test-only, but that had been demonstrated once by hand on the NUC and asserted
// everywhere else; this makes it a standing gate that runs on any platform.
func TestReadPeerMetaHandlesLargePids(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(metaPath, []byte(fmt.Sprintf(
		`{"listenerPid":%d,"ownerPid":4194304}`, largePid)), 0o644); err != nil {
		t.Fatal(err)
	}
	m, ok := ReadPeerMeta(metaPath)
	if !ok {
		t.Fatal("ReadPeerMeta failed")
	}
	if m.ListenerPid != largePid {
		t.Errorf("ListenerPid = %d, want %d", m.ListenerPid, largePid)
	}
	if m.OwnerPid != 4194304 {
		t.Errorf("OwnerPid = %d, want 4194304", m.OwnerPid)
	}
}

// TestWriteMetaPreservesLargePids covers the write half: a rewrite must put the pid
// back as an integer literal, never as a float rendering. peerMeta carries pids as
// json.RawMessage precisely so a rewrite cannot reformat them, and this asserts that
// property survives rather than trusting the field type.
func TestWriteMetaPreservesLargePids(t *testing.T) {
	dir := t.TempDir()
	m := peerMeta{
		Alias: "a", Channel: "c", SessionID: "sid", Cwd: "/w",
		ListenerPid: json.RawMessage(fmt.Sprint(largePid)),
		OwnerPid:    json.RawMessage("4194304"),
		Host:        "h", TS: "2026-07-19T00:00:00Z",
	}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, fmt.Sprint(largePid)) || strings.Contains(s, "e+06") {
		t.Errorf("meta.json did not keep the pid as an integer literal:\n%s", s)
	}
	if got := readRawMeta(t, filepath.Join(dir, "meta.json")); fmt.Sprint(got["listenerPid"]) != fmt.Sprint(largePid) {
		t.Errorf("round-tripped listenerPid = %v", got["listenerPid"])
	}
}
