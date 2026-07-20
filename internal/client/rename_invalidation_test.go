package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// readRawMeta returns meta.json's decoded key/value map, so a test can assert on what
// is actually ON DISK rather than on what a struct read chose to surface.
//
// UseNumber is load-bearing (F4). Decoding into map[string]any turns every JSON number
// into a float64, and fmt's %v for float64 switches to scientific notation at exactly
// 1e6 — so a 7-digit pid renders "1.548122e+06" and any string comparison against it
// fails. That is invisible on darwin (pid_max 99999) and in a small container, and
// routine on the NUC (pid_max 4194304). json.Number keeps the literal text, so these
// assertions are pid-size-independent by construction rather than by luck.
func readRawMeta(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		t.Fatalf("parse meta: %v", err)
	}
	return m
}

// TestRenameInvalidatesListenerIdentity is the port-map D1 contract, and it is NEW
// behavior rather than a port of old behavior. argv-grep invalidated on rename by
// accident: after the dir moved, the needle used the new alias while the live
// follower's argv still carried the old path, so the peer read dead for free.
// (pid,starttime) survives a directory rename untouched, so a stale follower would
// start reading ALIVE. Rename has to clear the identity on purpose now.
//
// Both halves matter and the second is the one a naive fix breaks:
//  1. the predicate reads dead        -> "old tail is stale, re-arm" holds
//  2. listenerPid stays a non-zero int -> D4 tri-state still says ever-armed, so the
//     re-arm seeks EOF instead of replaying the whole inbox
//
// Clearing listenerPid instead of listenerStart satisfies (1) and silently breaks (2):
// rename does not truncate the inbox, so the re-arm would re-deliver every message the
// peer ever received.
func TestRenameInvalidatesListenerIdentity(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatal(err)
	}
	oldMeta := filepath.Join(root, "dev", "main", "meta.json")
	armMeta(oldMeta, selfStart(t)) // this process becomes the listener, with a structural witness

	if before := readRawMeta(t, oldMeta); before["listenerStart"] == nil || before["listenerStart"] == "" {
		t.Fatalf("armMeta recorded no listenerStart; nothing to invalidate: %v", before)
	}
	if !MetaListenerAlive(oldMeta) {
		t.Fatal("armed peer should read alive before the rename")
	}

	if _, _, _, err := Rename("newname", ""); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	newMeta := filepath.Join(root, "dev", "newname", "meta.json")
	after := readRawMeta(t, newMeta)

	if v, present := after["listenerStart"]; present && v != "" {
		t.Errorf("rename left listenerStart = %v — the stale tail would read ALIVE", v)
	}
	if v, present := after["listenerPid"]; !present || v == nil {
		t.Error("rename nulled listenerPid — the re-arm would replay the whole inbox (D4 tri-state lost)")
	}
	if MetaListenerAlive(newMeta) {
		t.Error("renamed peer must read dead until it re-arms")
	}
	// ever-armed-ness asserted directly, not inferred from the raw field. Its REASON
	// changed with the cursor: listenerPid no longer decides replay (the cursor does),
	// but it still drives the send gate and the cursor-less migration rule, so a rename
	// must not silently demote a renamed peer to never-armed.
	if pm, ok := ReadPeerMeta(newMeta); !ok || pm.ListenerPid == 0 {
		t.Error("renamed peer must still read as ever-armed (send gate + migration rule)")
	}
}

// TestRenameKeepsEverythingElse: the invalidation must be surgical. renameMeta
// round-trips through peerMeta, so a field missing from that struct is silently
// dropped here (B2's hazard, seen from the rename side).
func TestRenameKeepsEverythingElse(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatal(err)
	}
	oldMeta := filepath.Join(root, "dev", "main", "meta.json")
	armMeta(oldMeta, selfStart(t))
	before := readRawMeta(t, oldMeta)

	if _, _, _, err := Rename("newname", ""); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	after := readRawMeta(t, filepath.Join(root, "dev", "newname", "meta.json"))

	for k, want := range before {
		switch k {
		case "alias", "listenerStart": // the two the rename is allowed to change
			continue
		}
		if got, present := after[k]; !present {
			t.Errorf("rename DROPPED field %q (was %v)", k, want)
		} else if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("rename changed %q: %v -> %v", k, want, got)
		}
	}
}

// TestReapExposureUnchangedAcrossRename is B3. A renamed-not-yet-rearmed peer was
// ALREADY reapable before P3 (the argv needle followed the new alias while the
// follower's argv kept the old path), so the structural rewrite must not widen that
// window.
//
// The structural subtest carries B3 alone now. Its transition-branch twin asserted
// that a pre-P3 armed peer with a matching argv was NOT dead before its rename, a
// premise the deleted argv clause supplied; with one branch left there is no second
// binary's arming to agree with.
func TestReapExposureUnchangedAcrossRename(t *testing.T) {
	t.Run("structural branch", func(t *testing.T) {
		root := setupStore(t)
		if _, _, err := Join("dev", "main"); err != nil {
			t.Fatal(err)
		}
		armMeta(filepath.Join(root, "dev", "main", "meta.json"), selfStart(t))
		if _, _, _, err := Rename("newname", ""); err != nil {
			t.Fatal(err)
		}
		if !PeerDead(filepath.Join(root, "dev", "newname", "meta.json")) {
			t.Error("renamed peer must be PeerDead (same exposure as before P3)")
		}
	})
}
