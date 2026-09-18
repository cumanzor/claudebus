package client

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaemonStartupRearmsBeforeNativeWork(t *testing.T) {
	for _, state := range []string{"queue-ready", "uncertain", "disconnected", "binding-required", "detached"} {
		t.Run(state, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			c := mustDaemonConnect(t, d, req)
			c.State = state
			if state == "uncertain" {
				c.Pending = &queueAttempt{ClientID: "ambiguous", End: 10, Hash: "unchanged"}
			}
			if err := d.save(c); err != nil {
				t.Fatal(err)
			}
			m, err := readStartupMeta(filepath.Join(d.peerDir(c), "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			m.ListenerPid, m.ListenerStart = json.RawMessage("999999"), "previous-daemon"
			if err := writeDaemonMeta(d.peerDir(c), m); err != nil {
				t.Fatal(err)
			}
			d = reloadDaemonFixture(t, q)
			opens, inspects, finds := q.opens, q.inspects, q.finds
			d.rearmLoaded(context.Background())
			m, err = readStartupMeta(filepath.Join(d.peerDir(c), "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			active := state == "queue-ready" || state == "uncertain"
			if (m.ListenerStart == d.start) != active {
				t.Fatalf("listener state %s: %+v", state, m)
			}
			if q.opens != opens || q.inspects != inspects || q.finds != finds || len(q.calls) != 0 {
				t.Fatal("startup made native queue calls")
			}
			got := d.snapshot(c.ID)
			if got.State != state || (state == "uncertain" && got.Pending.ClientID != "ambiguous") {
				t.Fatalf("startup changed durable delivery state: %+v", got)
			}
		})
	}
}

func TestDaemonStartupRefusesChangedEpochAndBoundsBusyPeer(t *testing.T) {
	for _, mode := range []string{"replaced-registration", "removed-registration", "replaced-inbox", "busy"} {
		t.Run(mode, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			c := mustDaemonConnect(t, d, req)
			m, err := readStartupMeta(filepath.Join(d.peerDir(c), "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			m.ListenerStart = "previous-daemon"
			if mode == "replaced-registration" {
				m.ConnectionID = "new-owner"
			}
			if err := writeDaemonMeta(d.peerDir(c), m); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "removed-registration":
				if err := os.Remove(filepath.Join(d.peerDir(c), "meta.json")); err != nil {
					t.Fatal(err)
				}
			case "replaced-inbox":
				path := filepath.Join(d.peerDir(c), "inbox.jsonl")
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "busy":
				unlock, err := lockPeer(c.Channel, c.Alias)
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
			}
			d = reloadDaemonFixture(t, q)
			started := time.Now()
			d.rearmLoaded(context.Background())
			if time.Since(started) > time.Second {
				t.Fatal("busy peer blocked daemon startup")
			}
			got := d.snapshot(c.ID)
			if strings.Contains(mode, "registration") {
				if got.State != "detached" {
					t.Fatalf("changed registration rearmed: %+v", got)
				}
			} else if got.ListenerError == "" {
				t.Fatalf("blocked listener not visible: %+v", got)
			}
			if mode != "removed-registration" {
				m, err = readStartupMeta(filepath.Join(d.peerDir(c), "meta.json"))
				if err != nil || m.ListenerStart != "previous-daemon" {
					t.Fatalf("unknown/busy epoch rearmed: %+v %v", m, err)
				}
			}
		})
	}
}

func readStartupMeta(path string) (peerMeta, error) {
	var m peerMeta
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	return m, err
}
