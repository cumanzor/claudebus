package client

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seedMeta(t *testing.T, root, ch, al, sid string) {
	t.Helper()
	dir := filepath.Join(root, ch, al)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"sessionId":%q,"lastActivity":%q}`, sid, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
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
	t.Setenv("CBUS_SESSION_ID", "")
	t.Setenv("GROK_SESSION_ID", "")
	seedMeta(t, root, "alpha", "me", "SID")
	if self := ResolveSelf(); self != nil {
		t.Errorf("ResolveSelf() with no session id = %+v, want nil", self)
	}
}

const (
	envCbus   = "CBUS_SESSION_ID"
	envClaude = "CLAUDE_CODE_SESSION_ID"
	envGrok   = "GROK_SESSION_ID"
)

// clearSessionEnv blanks the whole $*_SESSION_ID chain so a test drives SessionID()
// through exactly the vars it sets (no ambient session leaks in from the dev/CI shell).
func clearSessionEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{envCbus, envClaude, envGrok} {
		t.Setenv(k, "")
	}
}

// TestSessionIDLookup pins the ordered env lookup: CBUS_SESSION_ID > CLAUDE_CODE_SESSION_ID
// > GROK_SESSION_ID, each alone and in every precedence pair, all-empty yielding "".
func TestSessionIDLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"cbus alone", map[string]string{envCbus: "C"}, "C"},
		{"claude alone", map[string]string{envClaude: "K"}, "K"},
		{"grok alone", map[string]string{envGrok: "G"}, "G"},
		{"cbus beats claude", map[string]string{envCbus: "C", envClaude: "K"}, "C"},
		{"cbus beats grok", map[string]string{envCbus: "C", envGrok: "G"}, "C"},
		{"claude beats grok", map[string]string{envClaude: "K", envGrok: "G"}, "K"},
		{"all three -> cbus", map[string]string{envCbus: "C", envClaude: "K", envGrok: "G"}, "C"},
		{"all empty", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearSessionEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := SessionID(); got != tc.want {
				t.Errorf("SessionID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestOverrideSessionIDBeatsEnv: the in-process override outranks every env var, and the
// returned restore func puts the env chain back in charge.
func TestOverrideSessionIDBeatsEnv(t *testing.T) {
	clearSessionEnv(t)
	t.Setenv(envCbus, "C")
	t.Setenv(envClaude, "K")
	t.Setenv(envGrok, "G")
	restore := OverrideSessionID("OVERRIDE")
	if got := SessionID(); got != "OVERRIDE" {
		t.Errorf("SessionID() under override = %q, want OVERRIDE", got)
	}
	restore()
	if got := SessionID(); got != "C" {
		t.Errorf("SessionID() after restore = %q, want C (env chain back)", got)
	}
}

// TestOverrideSessionIDNested: restore funcs unwind LIFO to the exact prior value.
func TestOverrideSessionIDNested(t *testing.T) {
	clearSessionEnv(t)
	r1 := OverrideSessionID("A")
	r2 := OverrideSessionID("B")
	if got := SessionID(); got != "B" {
		t.Errorf("innermost = %q, want B", got)
	}
	r2()
	if got := SessionID(); got != "A" {
		t.Errorf("after inner restore = %q, want A", got)
	}
	r1()
	if got := SessionID(); got != "" {
		t.Errorf("after outer restore = %q, want empty", got)
	}
}

// TestOverrideSessionIDEmptyIsNoop: an empty override does NOT force sessionless — it
// falls through to the env chain, so a caller with nothing to pin can call it blind.
func TestOverrideSessionIDEmptyIsNoop(t *testing.T) {
	clearSessionEnv(t)
	t.Setenv(envClaude, "K")
	restore := OverrideSessionID("")
	defer restore()
	if got := SessionID(); got != "K" {
		t.Errorf("empty override = %q, want K (env chain)", got)
	}
}
