package client

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func seedMeta(t *testing.T, root, ch, al, sid string) {
	t.Helper()
	dir := filepath.Join(root, ch, al)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"sessionId":%q}`, sid)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSelfAndFindPeerChannel(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID")
	seedMeta(t, root, "alpha", "me", "SID")
	seedMeta(t, root, "beta", "me", "SID")
	seedMeta(t, root, "alpha", "peer", "OTHER")
	seedMeta(t, root, "beta", "other", "OTHER")
	// a dot-dir must be invisible (the client's */ glob blindness)
	if err := os.MkdirAll(filepath.Join(root, ".remote", "nuc"), 0o755); err != nil {
		t.Fatal(err)
	}

	self := ResolveSelf()
	want := []LocalReg{{"alpha", "me"}, {"beta", "me"}} // channel-major glob order
	if fmt.Sprint(self) != fmt.Sprint(want) {
		t.Fatalf("ResolveSelf() = %+v, want %+v", self, want)
	}

	// bare "peer" exists in alpha (my channel) -> resolves to alpha
	if ch, ok := FindPeerChannel("peer"); !ok || ch != "alpha" {
		t.Errorf("FindPeerChannel(peer) = %q,%v; want alpha,true", ch, ok)
	}
	// "me" is in both my channels; the first (alpha) wins
	if ch, ok := FindPeerChannel("me"); !ok || ch != "alpha" {
		t.Errorf("FindPeerChannel(me) = %q,%v; want alpha,true", ch, ok)
	}
	// an alias in no channel of mine -> not found
	if ch, ok := FindPeerChannel("ghost"); ok {
		t.Errorf("FindPeerChannel(ghost) = %q,true; want not found", ch)
	}
}

func TestResolveSelfNoSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	seedMeta(t, root, "alpha", "me", "SID")
	if self := ResolveSelf(); self != nil {
		t.Errorf("ResolveSelf() with no session id = %+v, want nil", self)
	}
}
