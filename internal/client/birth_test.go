package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeRawMeta plants an arbitrary meta.json for a peer (birth-record fields included).
func writeRawMeta(t *testing.T, ch, alias, body string) {
	t.Helper()
	dir := filepath.Join(CBUSDir(), ch, alias)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func metaAt(t *testing.T, ch, alias string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(CBUSDir(), ch, alias, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestReserveAliasStampsBirth: the launcher stamps origin+model into the placeholder.
func TestReserveAliasStampsBirth(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-parent")
	if _, err := ReserveAlias("ch", "kid", OriginFresh, "opus"); err != nil {
		t.Fatal(err)
	}
	m := metaAt(t, "ch", "kid")
	if m["sessionId"] != "reserved" || m["origin"] != "fresh" || m["model"] != "opus" {
		t.Errorf("reservation birth-record = %v", m)
	}
	// a blank birth-record omits the keys entirely (omitempty)
	if _, err := ReserveAlias("ch", "kid2", "", ""); err != nil {
		t.Fatal(err)
	}
	m2 := metaAt(t, "ch", "kid2")
	if _, ok := m2["origin"]; ok {
		t.Error("blank origin must be omitted, not written as empty")
	}
	if _, ok := m2["model"]; ok {
		t.Error("blank model must be omitted")
	}
}

// TestJoinBirthThreeWay is D18: a reservation-less join resolves birth by identity.
func TestJoinBirthThreeWay(t *testing.T) {
	for _, tc := range []struct {
		name       string
		selfSid    string
		plant      func(t *testing.T) // writes the meta at ch/kid, or nothing
		alias      string
		wantOrigin string
		wantModel  string
	}{
		{
			name:    "reservation reclaim -> inherit",
			selfSid: "sid-child",
			plant: func(t *testing.T) {
				writeRawMeta(t, "ch", "kid", `{"alias":"kid","channel":"ch","sessionId":"reserved","origin":"fresh","model":"opus","listenerPid":null}`)
			},
			alias: "kid", wantOrigin: "fresh", wantModel: "opus",
		},
		{
			name:    "torn reservation -> blank, NEVER joined",
			selfSid: "sid-child",
			plant: func(t *testing.T) {
				writeRawMeta(t, "ch", "kid", `{"alias":"kid","channel":"ch","sessionId":"reserved","listenerPid":null}`)
			},
			alias: "kid", wantOrigin: "", wantModel: "",
		},
		{
			name:    "resume-rejoin (own sid, armed-dead) -> preserve",
			selfSid: "sid-mine",
			plant: func(t *testing.T) {
				dp := deadProc(t)
				writeRawMeta(t, "ch", "kid", `{"alias":"kid","channel":"ch","sessionId":"sid-mine","origin":"fresh","model":"sonnet","listenerPid":`+strconv.Itoa(dp)+`}`)
			},
			alias: "kid", wantOrigin: "fresh", wantModel: "sonnet",
		},
		{
			name:    "takeover (different sid) -> joined, NOT the stranger's fork",
			selfSid: "sid-mine",
			plant: func(t *testing.T) {
				dp := deadProc(t)
				writeRawMeta(t, "ch", "kid", `{"alias":"kid","channel":"ch","sessionId":"sid-other","origin":"fork","model":"opus","listenerPid":`+strconv.Itoa(dp)+`}`)
			},
			alias: "kid", wantOrigin: OriginJoined, wantModel: "",
		},
		{
			name:       "explicit alias, no prior meta -> joined",
			selfSid:    "sid-mine",
			plant:      func(t *testing.T) {},
			alias:      "solo",
			wantOrigin: OriginJoined, wantModel: "",
		},
		{
			name:       "auto-pick join -> joined",
			selfSid:    "sid-mine",
			plant:      func(t *testing.T) {},
			alias:      "", // auto
			wantOrigin: OriginJoined, wantModel: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CBUS_DIR", t.TempDir())
			t.Setenv("CLAUDE_CODE_SESSION_ID", tc.selfSid)
			tc.plant(t)
			chosen, already, err := Join("ch", tc.alias)
			if err != nil || already {
				t.Fatalf("join = %q already=%v err=%v", chosen, already, err)
			}
			m := metaAt(t, "ch", chosen)
			gotO, _ := m["origin"].(string)
			gotM, _ := m["model"].(string)
			if gotO != tc.wantOrigin || gotM != tc.wantModel {
				t.Errorf("birth = (origin %q, model %q), want (%q, %q)\nmeta=%v", gotO, gotM, tc.wantOrigin, tc.wantModel, m)
			}
		})
	}
}

// TestSpawnStampsFresh / branch stamps fork: the launcher's origin reaches the
// reservation, through the real Spawn/Branch via a fake terminal.
func TestSpawnBranchStampBirth(t *testing.T) {
	t.Run("spawn=fresh", func(t *testing.T) {
		t.Setenv("CBUS_DIR", t.TempDir())
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-parent")
		f := &fakeForker{}
		if _, _, err := Spawn("tab", "ch", "opus", "kid", "", f); err != nil {
			t.Fatal(err)
		}
		m := metaAt(t, "ch", "kid")
		if m["origin"] != "fresh" || m["model"] != "opus" {
			t.Errorf("spawn reservation = %v, want fresh+opus", m)
		}
	})
	t.Run("branch=fork", func(t *testing.T) {
		t.Setenv("CBUS_DIR", t.TempDir())
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-parent")
		f := &fakeForker{}
		_, _, child, err := Branch("tab", "ch", "sonnet", "kid", f)
		if err != nil {
			t.Fatal(err)
		}
		m := metaAt(t, "ch", child)
		if m["origin"] != "fork" || m["model"] != "sonnet" {
			t.Errorf("branch reservation = %v, want fork+sonnet", m)
		}
	})
}

// TestMetaOmitemptyByteIdentity: a rewrite of a meta that lacks origin/model stays
// byte-identical — the compat guarantee (bash-era / pre-m9l metas do not grow keys).
func TestMetaOmitemptyByteIdentity(t *testing.T) {
	dir := t.TempDir()
	m := peerMeta{Alias: "a", Channel: "c", SessionID: "s", Cwd: "/x",
		ListenerPid: jsonNull, OwnerPid: jsonNull, Host: "h", TS: "t"}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	if strings.Contains(string(b), "origin") || strings.Contains(string(b), "model") {
		t.Errorf("blank birth-record must not appear in the file:\n%s", b)
	}
	// and with a birth-record, the keys are present
	m.Origin, m.Model = OriginFork, "opus"
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(dir, "meta.json"))
	if !strings.Contains(string(b2), `"origin": "fork"`) || !strings.Contains(string(b2), `"model": "opus"`) {
		t.Errorf("birth-record not written:\n%s", b2)
	}
}
