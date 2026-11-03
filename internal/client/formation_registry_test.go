package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const regSid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func writeRegistry(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadRegistrySids(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeRegistry(t, a, "100.json", `{"pid":100,"sessionId":"`+regSid+`"}`)
	writeRegistry(t, a, "100.abc.key", `secret`)
	writeRegistry(t, a, "200.json", `{"pid":200,"sessionId":"11111111-2222-3333-4444-555555555555"}`) // dead
	writeRegistry(t, b, "300.json", `{"pid":301,"sessionId":"22222222-2222-3333-4444-555555555555"}`) // pid mismatch
	writeRegistry(t, b, "400.json", `{"pid":400,"sessionId":"not-a-uuid"}`)
	writeRegistry(t, b, "500.json", `{torn`)
	if err := os.Symlink(filepath.Join(a, "sessions", "100.json"), filepath.Join(b, "sessions", "600.json")); err != nil {
		t.Fatal(err)
	}
	alive := func(pid int) bool { return pid != 200 }
	held := readRegistrySids([]string{a, b, filepath.Join(a, "missing")}, alive)
	if len(held) != 1 || held[regSid] != 100 {
		t.Errorf("held = %v, want only %s at pid 100", held, regSid)
	}
}

func stubRegistrySids(t *testing.T, held map[string]int) {
	orig := registrySids
	registrySids = func() map[string]int { return held }
	t.Cleanup(func() { registrySids = orig })
}

// a fresh-started session that left the bus has no sid in its argv; the registry still names it
func TestApplyRefusesResumeOfARegistryHeldSession(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-applier")
	if _, _, err := Join("ch", "orchestrator"); err != nil {
		t.Fatal(err)
	}
	stubRegistrySids(t, map[string]int{"sid-coder": 4545})
	f := applyFixture(peer("orchestrator"), peer("coder", func(p *FormationPeer) {
		p.Machine, p.Mode, p.Origin, p.SessionID = thisHost(), ModeResume, OriginFresh, "sid-coder"
	}))
	fk := &recForker{}
	rep, _ := applyWith(t, f, ApplyOptions{}, fk, func(_, sid string) bool { return sid == "sid-coder" })
	if len(fk.specs) != 0 {
		t.Fatalf("forked %d times onto a running session", len(fk.specs))
	}
	r := resultFor(t, rep, "coder")
	if r.Outcome != OutcomeRefused || !strings.Contains(r.Detail, "pid 4545") {
		t.Errorf("coder = %s (%s), want refused naming pid 4545", r.Outcome, r.Detail)
	}
}

// a second apply before the first one's child joins must not resume the same session again
func TestApplyResumeClaimsLaunchIntent(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-applier")
	if _, _, err := Join("ch", "orchestrator"); err != nil {
		t.Fatal(err)
	}
	mk := func() *Formation {
		return applyFixture(peer("orchestrator"), peer("coder", func(p *FormationPeer) {
			p.Machine, p.Mode, p.Origin, p.SessionID = thisHost(), ModeResume, OriginFresh, "sid-coder"
		}))
	}
	has := func(_, sid string) bool { return sid == "sid-coder" }
	first := &recForker{}
	if _, err := applyWith(t, mk(), ApplyOptions{}, first, has); err != nil {
		t.Fatal(err)
	}
	if len(first.specs) != 1 {
		t.Fatalf("first apply forked %d, want 1", len(first.specs))
	}
	second := &recForker{}
	rep, _ := applyWith(t, mk(), ApplyOptions{}, second, has)
	if len(second.specs) != 0 {
		t.Fatalf("second apply forked %d onto a session still booting", len(second.specs))
	}
	if r := resultFor(t, rep, "coder"); r.Outcome != OutcomeFailed || !strings.Contains(r.Detail, "has not joined yet") {
		t.Errorf("coder = %s (%s), want failed on the launch intent", r.Outcome, r.Detail)
	}
}

func resultFor(t *testing.T, rep *ApplyReport, alias string) PeerResult {
	t.Helper()
	if rep != nil {
		for _, r := range rep.Results {
			if r.Alias == alias {
				return r
			}
		}
	}
	t.Fatalf("no result for %s in %+v", alias, rep)
	return PeerResult{}
}
