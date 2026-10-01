package client

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func plainAncestry() ([]procRecord, bool) {
	return []procRecord{{PPid: 20, Comm: "zsh", Argv: "-zsh"}, {PPid: 1, Comm: "login", Argv: "login -pf user"}}, true
}

func harnessAncestry() ([]procRecord, bool) {
	return []procRecord{{PPid: 20, Comm: "zsh", Argv: "zsh"}, {PPid: 1, Comm: "2.1.286", Argv: "/home/u/.local/bin/claude --model x"}}, true
}

func truncatedAncestry() ([]procRecord, bool) {
	return []procRecord{{PPid: 20, Comm: "zsh", Argv: "-zsh"}}, false
}

func withAncestry(t *testing.T, f func() ([]procRecord, bool)) {
	t.Helper()
	old := grantAncestry
	grantAncestry = f
	t.Cleanup(func() { grantAncestry = old })
}

func withClock(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	now := at
	old := grantNow
	grantNow = func() time.Time { return now }
	t.Cleanup(func() { grantNow = old })
	return &now
}

// mint writes a grant the way `cbus grant` does after the operator confirms. The
// grantee is registered first as sid-<alias> unless the test registered it already.
func mint(t *testing.T, target, action, mode string, ttl time.Duration) Grant {
	t.Helper()
	if ch, alias, err := ParseLocal(target); err == nil && ch != "" {
		if !fileExists(filepath.Join(CBUSDir(), ch, alias, "meta.json")) {
			plantPeer(t, ch, alias, "sid-"+alias)
		}
	}
	g, err := NewGrant(target, action, mode, ttl)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	g.GrantedBy = GrantProvenanceNow("dev=0x1")
	if err := WriteGrant(g); err != nil {
		t.Fatalf("WriteGrant: %v", err)
	}
	return g
}

func TestNewGrantRefusesBadRequests(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	cases := []struct {
		name, target, action, mode string
		ttl                        time.Duration
		want                       string
	}{
		{"remote", "ch@server/coder", "push", GrantOnce, 0, "local-only"},
		{"no channel", "coder", "push", GrantOnce, 0, "<channel>/<alias>"},
		{"bad alias", "ch/co der", "push", GrantOnce, 0, "bad alias"},
		{"empty action", "ch/coder", "  ", GrantOnce, 0, "must not be empty"},
		{"multiline action", "ch/coder", "push\nand more", GrantOnce, 0, "one line"},
		{"long action", "ch/coder", strings.Repeat("x", grantMaxActionLen+1), GrantOnce, 0, "the limit is"},
		{"zero ttl", "ch/coder", "push", GrantTTL, 0, "--ttl must be more than 0"},
		{"negative ttl", "ch/coder", "push", GrantTTL, -time.Minute, "--ttl must be more than 0"},
		{"ttl over cap", "ch/coder", "push", GrantTTL, GrantMaxTTL + time.Second, "at most 24h0m0s"},
		{"bad mode", "ch/coder", "push", "forever", 0, "grant mode must be"},
		{"absent target", "ch/nobody", "push", GrantOnce, 0, "no peer ch/nobody is registered"},
	}
	plantPeer(t, "ch", "coder", "sid-coder")
	for _, c := range cases {
		if _, err := NewGrant(c.target, c.action, c.mode, c.ttl); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
	if _, err := os.Stat(grantsRoot()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused request must not create the grants dir: %v", err)
	}
}

func TestGrantOnceIsConsumedByItsFirstUse(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	g := mint(t, "ch/coder", "push the branch", GrantOnce, 0)
	self := []LocalReg{{Channel: "ch", Alias: "coder"}}

	views, err := ListGrants(self, false)
	if err != nil || len(views) != 1 || views[0].State != GrantLive {
		t.Fatalf("a fresh once grant must list live: %+v %v", views, err)
	}
	v, err := UseGrant(g.ID, "sid-coder", self)
	if err != nil || v.State != GrantUsed || v.UsedBy != "sid-coder" {
		t.Fatalf("first use must consume it: %+v %v", v, err)
	}
	if _, err := UseGrant(g.ID, "sid-coder", self); err == nil || !strings.Contains(err.Error(), "used, not live") {
		t.Fatalf("a second use must be refused, got %v", err)
	}
}

func TestGrantOnceConcurrentUsesYieldOneSuccess(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	self := []LocalReg{{Channel: "ch", Alias: "coder"}}
	for round := 0; round < 20; round++ {
		g := mint(t, "ch/coder", "push", GrantOnce, 0)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := UseGrant(g.ID, "sid-coder", self); err == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("round %d: %d concurrent uses succeeded, want exactly 1", round, wins)
		}
	}
}

func TestGrantTTLStaysUsableUntilItExpires(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	now := withClock(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	g := mint(t, "ch/coder", "edit the other repo", GrantTTL, time.Hour)
	self := []LocalReg{{Channel: "ch", Alias: "coder"}}
	for i := 1; i <= 2; i++ {
		if v, err := UseGrant(g.ID, "sid-coder", self); err != nil || v.State != GrantLive || v.Uses != i {
			t.Fatalf("use %d inside the ttl must succeed, stay live and be logged: %+v %v", i, v, err)
		}
	}
	*now = now.Add(time.Hour)
	views, _ := ListGrants(self, false)
	if len(views) != 1 || views[0].State != GrantExpired {
		t.Fatalf("at the expiry instant the grant must list expired: %+v", views)
	}
	if _, err := UseGrant(g.ID, "sid-coder", self); err == nil || !strings.Contains(err.Error(), "expired, not live") {
		t.Fatalf("an expired grant must be refused, got %v", err)
	}
}

func TestGrantRevokedIsRefused(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	g := mint(t, "ch/coder", "push", GrantTTL, time.Hour)
	if v, err := RevokeGrant(g.ID, "operator"); err != nil || v.State != GrantRevoked {
		t.Fatalf("revoke: %+v %v", v, err)
	}
	if v, err := RevokeGrant(g.ID, "operator"); err != nil || v.State != GrantRevoked {
		t.Fatalf("revoking twice reports the revocation: %+v %v", v, err)
	}
	if _, err := UseGrant(g.ID, "sid-coder", []LocalReg{{Channel: "ch", Alias: "coder"}}); err == nil || !strings.Contains(err.Error(), "revoked, not live") {
		t.Fatalf("a revoked grant must be refused, got %v", err)
	}
	if _, err := RevokeGrant("g-0000000000", "operator"); err == nil {
		t.Fatal("revoking an unknown grant must fail")
	}
}

func TestGrantIsVisibleAndUsableOnlyByItsGrantee(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	g := mint(t, "ch/coder", "push", GrantOnce, 0)
	other := []LocalReg{{Channel: "ch", Alias: "reviewer"}}

	if views, _ := ListGrants(other, false); len(views) != 0 {
		t.Errorf("another alias must not see the grant by default: %+v", views)
	}
	if views, _ := ListGrants(other, true); len(views) != 1 || views[0].ID != g.ID {
		t.Errorf("--all lists every grant in the store: %+v", views)
	}
	if _, err := UseGrant(g.ID, "sid-reviewer", other); err == nil || !strings.Contains(err.Error(), "not for this session") {
		t.Fatalf("a non-grantee must not use the grant, got %v", err)
	}
	if v, _ := ListGrants([]LocalReg{{Channel: "ch", Alias: "coder"}}, false); len(v) != 1 || v[0].State != GrantLive {
		t.Fatalf("the refused use must leave the grant live for its grantee: %+v", v)
	}
}

func TestGrantFromAHarnessTreeIsSuspect(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	self := []LocalReg{{Channel: "ch", Alias: "coder"}}

	withAncestry(t, harnessAncestry)
	g := mint(t, "ch/coder", "push", GrantOnce, 0)
	if !g.GrantedBy.HarnessAncestor || g.GrantedBy.Harness != "claude" {
		t.Fatalf("a claude ancestor must be recorded: %+v", g.GrantedBy)
	}
	if _, err := UseGrant(g.ID, "sid-coder", self); err == nil || !strings.Contains(err.Error(), "suspect, not live") {
		t.Fatalf("a suspect grant must be refused, got %v", err)
	}

	withAncestry(t, truncatedAncestry)
	g2 := mint(t, "ch/coder", "push", GrantOnce, 0)
	if !g2.GrantedBy.AncestryTruncated || g2.GrantedBy.HarnessAncestor {
		t.Fatalf("a truncated walk must be recorded as truncated: %+v", g2.GrantedBy)
	}
	if _, err := UseGrant(g2.ID, "sid-coder", self); err == nil || !strings.Contains(err.Error(), "suspect, not live") {
		t.Fatalf("a grant whose ancestry was truncated must be refused, got %v", err)
	}

	withAncestry(t, plainAncestry)
	g3 := mint(t, "ch/coder", "push", GrantOnce, 0)
	if g3.GrantedBy.HarnessAncestor || g3.GrantedBy.AncestryTruncated {
		t.Fatalf("a plain terminal ancestry must not be flagged: %+v", g3.GrantedBy)
	}
	if want := []string{"zsh", "login"}; strings.Join(g3.GrantedBy.Ancestors, ",") != strings.Join(want, ",") {
		t.Errorf("ancestors = %v, want %v", g3.GrantedBy.Ancestors, want)
	}
	if _, err := UseGrant(g3.ID, "sid-coder", self); err != nil {
		t.Fatalf("a plain-terminal grant must be usable: %v", err)
	}
}

func TestChainHarnessMatchesHarnessArgvOnly(t *testing.T) {
	cases := []struct {
		name string
		rec  procRecord
		want string
	}{
		{"claude native", procRecord{Comm: "2.1.286", Argv: "/u/.local/bin/claude --model m"}, "claude"},
		{"codex native", procRecord{Comm: "codex", Argv: "codex exec"}, "codex"},
		{"claude npm cli", procRecord{Comm: "node", Argv: "node /u/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, "claude"},
		{"codex npm shim", procRecord{Comm: "node", Argv: "node /u/lib/node_modules/@openai/codex/bin/codex.js"}, "codex"},
		{"opencode under bun", procRecord{Comm: "bun", Argv: "bun /u/.opencode/bin/opencode"}, "opencode"},
		{"plain node app", procRecord{Comm: "node", Argv: "node /srv/app/server.js --claude-mode"}, ""},
		{"node wrapper of something else", procRecord{Comm: "node", Argv: "node /u/.nvm/bin/ccs personal"}, ""},
		{"electron app", procRecord{Comm: "Code Helper", Argv: "/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper"}, ""},
		{"desktop app", procRecord{Comm: "Claude", Argv: "/Applications/Claude.app/Contents/MacOS/Claude"}, ""},
		{"prompt text naming a harness", procRecord{Comm: "zsh", Argv: "zsh -c echo claude codex opencode"}, ""},
	}
	for _, c := range cases {
		if got := chainHarness([]procRecord{c.rec}); got != c.want {
			t.Errorf("%s: chainHarness = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAncestorChainReportsTruncation(t *testing.T) {
	recs := map[int]procRecord{
		10: {PPid: 9, Comm: "zsh"},
		9:  {PPid: 1, Comm: "login"},
		30: {PPid: 29, Comm: "zsh"},
		40: {PPid: 41, Comm: "a"},
		41: {PPid: 40, Comm: "b"},
	}
	lookup := func(p int) (procRecord, bool) { r, ok := recs[p]; return r, ok }
	if chain, complete := ancestorChain(10, 1, lookup); !complete || len(chain) != 2 {
		t.Errorf("a walk that reaches init is complete: %v %v", chain, complete)
	}
	if _, complete := ancestorChain(30, 1, lookup); complete {
		t.Error("a failed lookup is a truncation")
	}
	if _, complete := ancestorChain(40, 1, lookup); complete {
		t.Error("hitting the depth cap is a truncation")
	}
	young := map[int]procRecord{50: {PPid: 51, Created: 5}, 51: {PPid: 1, Created: 9}}
	if _, complete := ancestorChain(50, 1, func(p int) (procRecord, bool) { r, ok := young[p]; return r, ok }); complete {
		t.Error("a parent younger than its child is a truncation")
	}
}

func TestListGrantsIgnoresARecordAtTheWrongPath(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	g := mint(t, "ch/coder", "push", GrantOnce, 0)
	// a record copied under another alias's dir is not a grant for that alias
	src := filepath.Join(grantDir("ch", "coder"), g.ID+".json")
	b, _ := os.ReadFile(src)
	_ = os.MkdirAll(grantDir("ch", "reviewer"), 0o700)
	_ = os.WriteFile(filepath.Join(grantDir("ch", "reviewer"), g.ID+".json"), b, 0o600)
	if v, _ := ListGrants([]LocalReg{{Channel: "ch", Alias: "reviewer"}}, false); len(v) != 0 {
		t.Errorf("a record whose content names another peer must not list for this one: %+v", v)
	}
}

// TestGrantBindsTheExactSession: a later session holding the alias, through the real
// reclaim path (reserve over a dead peer, then join), is someone else.
func TestGrantBindsTheExactSession(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	plantPeer(t, "ch", "coder", "sid-original")
	g := mint(t, "ch/coder", "push", GrantOnce, 0)
	if g.SessionID != "sid-original" {
		t.Fatalf("the grant must record the grantee's session: %+v", g)
	}

	if _, err := ReserveAlias("ch", "coder", OriginFresh, ""); err != nil {
		t.Fatalf("reserve over the dead peer: %v", err)
	}
	if _, err := NewGrant("ch/coder", "push", GrantOnce, 0); err == nil || !strings.Contains(err.Error(), "reserved but has not joined") {
		t.Fatalf("a reserved alias has no session to bind, got %v", err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-later")
	if _, _, err := Join("ch", "coder"); err != nil {
		t.Fatalf("join as the later session: %v", err)
	}
	if m, _ := ReadPeerMeta(filepath.Join(CBUSDir(), "ch", "coder", "meta.json")); m.SessionID != "sid-later" {
		t.Fatalf("precondition: the alias must now belong to the later session, meta has %q", m.SessionID)
	}

	self := []LocalReg{{Channel: "ch", Alias: "coder"}}
	if _, err := UseGrant(g.ID, "sid-later", self); err == nil || !strings.Contains(err.Error(), "bound to session sid-original") {
		t.Fatalf("a later holder of the alias must not use the grant, got %v", err)
	}
	if v, _ := ListGrants(self, false); len(v) != 1 || v[0].State != GrantLive {
		t.Fatalf("the refused use must not consume it: %+v", v)
	}
	if _, err := UseGrant(g.ID, "sid-original", self); err != nil {
		t.Fatalf("the bound session can still use it: %v", err)
	}
}

// TestGrantSurvivesTheSameSessionReconnecting: the connection id changes on a
// reconnect, so use binds the session id only.
func TestGrantSurvivesTheSameSessionReconnecting(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	dir := filepath.Join(CBUSDir(), "ch", "coder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := func(conn string) {
		// the shape the daemon's connect writes: the session's own id plus its connection
		if err := writeMeta(dir, peerMeta{Alias: "coder", Channel: "ch", SessionID: "sid-coder", ConnectionID: conn,
			ListenerPid: jsonNull, OwnerPid: jsonNull, Host: thisHost(), TS: Now(), LastActivity: Now()}); err != nil {
			t.Fatal(err)
		}
	}
	meta("conn-1")
	g := mint(t, "ch/coder", "push", GrantOnce, 0)
	if g.ConnectionID != "conn-1" {
		t.Fatalf("the connection id is recorded for audit: %+v", g)
	}
	meta("conn-2")
	if _, err := UseGrant(g.ID, "sid-coder", []LocalReg{{Channel: "ch", Alias: "coder"}}); err != nil {
		t.Fatalf("the same session after a reconnect must still use its grant: %v", err)
	}
}

func TestGrantRefusesADeadListener(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	dir := filepath.Join(CBUSDir(), "ch", "coder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// an armed-ever peer whose listener is gone: armMeta wrote a pid that no longer runs
	if err := writeMeta(dir, peerMeta{Alias: "coder", Channel: "ch", SessionID: "sid-coder", ListenerPid: json.RawMessage("2147483646"),
		ListenerStart: "1", OwnerPid: jsonNull, Host: thisHost(), TS: Now(), LastActivity: Now()}); err != nil {
		t.Fatal(err)
	}
	if !PeerDead(filepath.Join(dir, "meta.json")) {
		t.Fatal("precondition: the fixture must read as a dead peer")
	}
	if _, err := NewGrant("ch/coder", "push", GrantOnce, 0); err == nil || !strings.Contains(err.Error(), "listener is dead") {
		t.Fatalf("a dead listener must be refused, got %v", err)
	}
}

// TestGrantRecordWithoutASessionIsSuspect: no code path writes an unbound record, so
// one in the store was written by hand (the direct-write route) and is never live.
func TestGrantRecordWithoutASessionIsSuspect(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	withAncestry(t, plainAncestry)
	g := mint(t, "ch/coder", "push", GrantOnce, 0)
	g.ID, g.SessionID = "g-00000000aa", ""
	if err := WriteGrant(g); err != nil {
		t.Fatal(err)
	}
	views, _ := ListGrants([]LocalReg{{Channel: "ch", Alias: "coder"}}, false)
	for _, v := range views {
		if v.ID == g.ID && v.State != GrantSuspect {
			t.Fatalf("an unbound record must list suspect, got %s", v.State)
		}
	}
}
