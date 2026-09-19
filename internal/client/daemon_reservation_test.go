package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonConnectClaimsLaunchReservation(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "queued"}[queued], func(t *testing.T) {
			d, q, req := daemonFixture(t)
			if _, err := ReserveAlias(req.Channel, req.Alias, OriginFork, "test-model"); err != nil {
				t.Fatal(err)
			}
			var end int64
			var oldDev, oldIno uint64
			if queued {
				end = appendDaemonMessage(t, &ConnectionState{Channel: req.Channel, Alias: req.Alias}, "", "queued before launch")
				oldDev, oldIno, _, _ = fileIdentity(InboxPath(req.Channel, req.Alias))
			}
			c := mustDaemonConnect(t, d, req)
			if !d.owns(c) || c.Offset != 0 || queued && (c.Dev != oldDev || c.Ino != oldIno) {
				t.Fatal("claim replaced reserved inbox or skipped its mail")
			}
			b, err := os.ReadFile(filepath.Join(d.peerDir(c), "meta.json"))
			var m peerMeta
			if err != nil || json.Unmarshal(b, &m) != nil || m.Origin != OriginFork || m.Model != "test-model" || m.SessionID != req.ThreadID || m.ConnectionID != c.ID {
				t.Fatal("claim lost launch provenance or did not bind the new session")
			}
			if err := d.deliver(c); err != nil || c.Offset != end || len(q.calls) != map[bool]int{false: 0, true: 1}[queued] {
				t.Fatalf("reserved mail did not deliver exactly once: %v", err)
			}
		})
	}
}

func TestDaemonConnectRefusesNonReservationWithoutChangingMail(t *testing.T) {
	for _, kind := range []string{"real session", "managed", "owner", "listener", "wrong alias", "malformed", "oversized", "symlink directory", "symlink metadata", "symlink inbox"} {
		t.Run(kind, func(t *testing.T) {
			d, _, req := daemonFixture(t)
			if _, err := ReserveAlias(req.Channel, req.Alias, OriginFresh, "test-model"); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(CBUSDir(), req.Channel, req.Alias)
			metaPath := filepath.Join(dir, "meta.json")
			b, _ := os.ReadFile(metaPath)
			var m peerMeta
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "real session":
				m.SessionID = daemonTestThread
			case "managed":
				m.ConnectionID = "existing-epoch"
			case "owner":
				m.OwnerPid = json.RawMessage("42")
			case "listener":
				m.ListenerPid = json.RawMessage("42")
			case "wrong alias":
				m.Alias = "other"
			}
			if err := writeMeta(dir, m); err != nil {
				t.Fatal(err)
			}
			if kind == "malformed" {
				if err := os.WriteFile(metaPath, []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "oversized" {
				valid, _ := json.Marshal(m)
				body := string(valid) + strings.Repeat(" ", (1<<20)-len(valid)) + "invalid suffix"
				if err := os.WriteFile(metaPath, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			inbox := filepath.Join(dir, "inbox.jsonl")
			if err := os.WriteFile(inbox, []byte("preserved\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var from string
			switch kind {
			case "symlink directory":
				from = dir
			case "symlink metadata":
				from = metaPath
			case "symlink inbox":
				from = inbox
			}
			if from != "" {
				to := filepath.Join(t.TempDir(), "original")
				if err := os.Rename(from, to); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(to, from); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(metaPath)
			if _, err := d.connect(req); err == nil {
				t.Fatal("non-reservation was claimed")
			}
			after, _ := os.ReadFile(metaPath)
			mail, _ := os.ReadFile(inbox)
			if string(before) != string(after) || string(mail) != "preserved\n" || len(d.statusSnapshots()) != 0 {
				t.Fatal("refusal modified existing registration or mail")
			}
		})
	}
}
