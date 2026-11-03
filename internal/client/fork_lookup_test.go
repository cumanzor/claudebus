package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestJoinKeepsForkBornOriginAcrossChannels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T)
		want  string
	}{
		{"fork meta in another channel", func(t *testing.T) {
			writeRawMeta(t, "a", "kid", `{"alias":"kid","channel":"a","sessionId":"sid-kid","origin":"fork","listenerPid":null}`)
		}, OriginFork},
		{"meta pruned, ledger remembers", func(t *testing.T) {
			if err := AppendLedger(LedgerEvent{Event: LedgerJoin, Channel: "a", Alias: "kid", SessionID: "sid-kid", Origin: OriginFork}); err != nil {
				t.Fatal(err)
			}
		}, OriginFork},
		{"another session's fork", func(t *testing.T) {
			writeRawMeta(t, "a", "other", `{"alias":"other","channel":"a","sessionId":"sid-other","origin":"fork","listenerPid":null}`)
		}, OriginJoined},
		{"fresh elsewhere", func(t *testing.T) {
			writeRawMeta(t, "a", "kid", `{"alias":"kid","channel":"a","sessionId":"sid-kid","origin":"fresh","listenerPid":null}`)
		}, OriginJoined},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CBUS_DIR", t.TempDir())
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-kid")
			tc.plant(t)
			alias, _, err := Join("b", "seat")
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := metaAt(t, "b", alias)["origin"].(string); got != tc.want {
				t.Errorf("origin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDaemonConnectKeepsForkBornOrigin(t *testing.T) {
	d, _, req := daemonFixture(t)
	if err := AppendLedger(LedgerEvent{Event: LedgerJoin, Channel: "elsewhere", Alias: "kid", SessionID: req.ThreadID, Origin: OriginFork}); err != nil {
		t.Fatal(err)
	}
	c := mustDaemonConnect(t, d, req)
	b, err := os.ReadFile(filepath.Join(d.peerDir(c), "meta.json"))
	var m peerMeta
	if err != nil || json.Unmarshal(b, &m) != nil {
		t.Fatal("meta unreadable")
	}
	if m.Origin != OriginFork {
		t.Errorf("origin = %q, want fork carried from the ledger", m.Origin)
	}
}

// a branch child that joins a second channel must save there as template, not resume
func TestForkChildSavesAsTemplateInASecondChannel(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-parent")
	_, _, child, err := Branch("tab", "a", "", "kid", &fakeForker{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-kid")
	if _, _, err := Join("a", child); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Join("b", "seat"); err != nil {
		t.Fatal(err)
	}
	f, _, err := SaveFormation("b", "b", SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f.Peers {
		if p.Alias == "seat" && (p.Origin != OriginFork || p.Mode != ModeTemplate) {
			t.Errorf("seat = origin %q mode %q, want fork/template", p.Origin, p.Mode)
		}
	}
}
