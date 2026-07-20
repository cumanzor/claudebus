package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claudebus/internal/core"
)

// TestConcurrentClaimUniqueAliases pins F1: N concurrent auto-pick claims must all
// succeed with unique aliases. The pre-fix µs loop burned its retries in a
// sibling's mkdir→meta window and lost claims; the EEXIST-exclusion converges.
func TestConcurrentClaimUniqueAliases(t *testing.T) {
	setupStore(t)
	const N = 24
	var wg sync.WaitGroup
	aliases := make([]string, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			aliases[i], _, errs[i] = claimAlias("cc")
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := 0; i < N; i++ {
		if errs[i] != nil {
			t.Errorf("claim %d failed: %v", i, errs[i])
			continue
		}
		if seen[aliases[i]] {
			t.Errorf("duplicate alias %q", aliases[i])
		}
		seen[aliases[i]] = true
	}
	if len(seen) != N {
		t.Errorf("got %d unique aliases from %d concurrent claims, want %d", len(seen), N, N)
	}
}

func setupStore(t *testing.T) string {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID")
	return root
}

// seedPeer writes a joined-but-never-armed peer (fresh lastActivity -> not dead)
// for the given session id, plus an empty inbox.
func seedPeer(t *testing.T, root, ch, al, sid string) {
	t.Helper()
	seedPeerPid(t, root, ch, al, sid, "null")
}

// seedPeerPid writes a peer whose listenerPid is the given raw JSON (null or an int).
// seedPeerArmed writes a peer armed the way armMeta arms one: the listener pid AND
// its structural witness. seedPeerPid stays for the cases that want a raw pid with no
// witness (never-armed "null", or an armed-but-dead pid that is dead either way).
func seedPeerArmed(t *testing.T, root, ch, al, sid string, pid int) {
	t.Helper()
	start, err := procStartTime(pid)
	if err != nil {
		t.Fatalf("procStartTime(%d): %v", pid, err)
	}
	dir := filepath.Join(root, ch, al)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"alias":%q,"channel":%q,"sessionId":%q,"cwd":"/w","listenerPid":%d,"listenerStart":%q,"ownerPid":null,"host":"h","ts":"2026-07-13T00:00:00Z","lastActivity":%q}`,
		al, ch, sid, pid, start, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inbox.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedPeerPid(t *testing.T, root, ch, al, sid, pid string) {
	t.Helper()
	dir := filepath.Join(root, ch, al)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"alias":%q,"channel":%q,"sessionId":%q,"cwd":"/w","listenerPid":%s,"ownerPid":null,"host":"h","ts":"2026-07-13T00:00:00Z","lastActivity":%q}`, al, ch, sid, pid, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inbox.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func inbox(t *testing.T, root, ch, al string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, ch, al, "inbox.jsonl"))
	return string(b)
}

func TestJoinCreatesTruncatesAndDualWrites(t *testing.T) {
	root := setupStore(t)
	alias, already, err := Join("dev", "")
	if err != nil || already || alias != "main" {
		t.Fatalf("Join = %q,%v,%v; want main,false,nil", alias, already, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "dev", "main", "meta.json"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["alias"] != "main" || m["channel"] != "dev" || m["sessionId"] != "SID" || m["listenerPid"] != nil {
		t.Errorf("meta = %v", m)
	}
	if _, ok := m["lastActivity"]; !ok {
		t.Error("lastActivity was not dual-written (D3)")
	}
	if in := inbox(t, root, "dev", "main"); in != "" {
		t.Errorf("inbox not truncated: %q", in)
	}
	if a2, already2, _ := Join("dev", ""); !already2 || a2 != "main" {
		t.Errorf("re-join same session = %q,%v; want main,true", a2, already2)
	}
}

func TestJoinAutoPicksForkN(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "main", "OTHER")
	seedPeer(t, root, "dev", "fork-1", "OTHER")
	if alias, _, err := Join("dev", ""); err != nil || alias != "fork-2" {
		t.Errorf("Join auto = %q,%v; want fork-2", alias, err)
	}
}

func TestJoinExplicitTakenByLiveListener(t *testing.T) {
	root := setupStore(t)
	// a LIVE listener on dev/main (this test process, with the inbox in argv... we
	// can't fake that here, so use the dead path): a dead armed peer is reclaimable.
	seedPeerPid(t, root, "dev", "main", "OTHER", "999999") // armed but dead
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatalf("joining over a DEAD listener should succeed (reclaim): %v", err)
	}
	// now it's ours
	b, _ := os.ReadFile(filepath.Join(root, "dev", "main", "meta.json"))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["sessionId"] != "SID" {
		t.Errorf("reclaim did not rewrite the peer: %v", m)
	}
}

func TestPresenceJoinBroadcast(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "watcher", "OTHER") // never-armed, fresh -> not dead
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatal(err)
	}
	w := inbox(t, root, "dev", "watcher")
	for _, want := range []string{`"kind":"presence"`, `"event":"join"`, `"from":"dev/main"`, `"to":"dev/watcher"`, `"text":"joined dev as main"`} {
		if !strings.Contains(w, want) {
			t.Errorf("watcher inbox missing %q in %q", want, w)
		}
	}
	if s := inbox(t, root, "dev", "main"); s != "" {
		t.Errorf("subject must not receive its own join (skip=self): %q", s)
	}
}

func TestPruneReapsDeadAndDeparts(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "watcher", "OTHER")            // live (never-armed, fresh)
	seedPeerPid(t, root, "dev", "ghost", "OTHER", "999999") // armed + dead
	msgs := PruneChannel("dev")
	if dirExists(filepath.Join(root, "dev", "ghost")) {
		t.Error("dead ghost was not reaped")
	}
	if !dirExists(filepath.Join(root, "dev", "watcher")) {
		t.Error("live watcher was wrongly reaped")
	}
	if !strings.Contains(strings.Join(msgs, " "), "pruned dev/ghost") {
		t.Errorf("prune messages = %v", msgs)
	}
	w := inbox(t, root, "dev", "watcher")
	if !strings.Contains(w, `"event":"departed"`) || !strings.Contains(w, `"from":"dev/ghost"`) || !strings.Contains(w, `"text":"departed (listener gone)"`) {
		t.Errorf("watcher missing departed presence: %q", w)
	}
}

func TestRename(t *testing.T) {
	root := setupStore(t)
	if _, _, err := Join("dev", "main"); err != nil {
		t.Fatal(err)
	}
	seedPeer(t, root, "dev", "watcher", "OTHER")
	ch, old, already, err := Rename("newname", "")
	if err != nil || already || ch != "dev" || old != "main" {
		t.Fatalf("Rename = %q,%q,%v,%v", ch, old, already, err)
	}
	if dirExists(filepath.Join(root, "dev", "main")) || !dirExists(filepath.Join(root, "dev", "newname")) {
		t.Error("rename did not move the dir")
	}
	b, _ := os.ReadFile(filepath.Join(root, "dev", "newname", "meta.json"))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["alias"] != "newname" {
		t.Errorf("meta.alias not rewritten: %v", m)
	}
	// the presence line is canonical-Go (compact, HTML-escapes '>' to > — D8);
	// it frames identically to bash's, so assert on the DECODED fields.
	w := inbox(t, root, "dev", "watcher")
	pm, derr := core.DecodeMessage([]byte(strings.TrimSpace(w)))
	if derr != nil || pm.Kind != "presence" || pm.Event != "rename" || pm.Text != "renamed main -> newname" {
		t.Errorf("watcher rename presence = %+v (%v); raw %q", pm, derr, w)
	}
}

func TestUnregisterAndLeave(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "victim", "OTHER")
	seedPeer(t, root, "dev", "watcher", "OTHER")
	if err := Unregister("dev", "victim"); err != nil {
		t.Fatal(err)
	}
	if dirExists(filepath.Join(root, "dev", "victim")) {
		t.Error("victim not removed")
	}
	if w := inbox(t, root, "dev", "watcher"); !strings.Contains(w, `"text":"unregistered"`) {
		t.Errorf("watcher missing unregistered presence: %q", w)
	}

	// leave: this session joins then leaves, watcher hears "left"
	if _, _, err := Join("team", "me"); err != nil {
		t.Fatal(err)
	}
	seedPeer(t, root, "team", "peer", "OTHER")
	left, err := Leave("team")
	if err != nil || len(left) != 1 || left[0] != "team/me" {
		t.Fatalf("Leave = %v,%v", left, err)
	}
	if dirExists(filepath.Join(root, "team", "me")) {
		t.Error("left peer not removed")
	}
	if p := inbox(t, root, "team", "peer"); !strings.Contains(p, `"event":"leave"`) || !strings.Contains(p, `"text":"left team"`) {
		t.Errorf("peer missing leave presence: %q", p)
	}
}
