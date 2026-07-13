package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitVerbArgsTerminator(t *testing.T) {
	valued := map[string]bool{"--from": true}
	bare := map[string]bool{"--force": true}

	// leading options, then the `--` terminator makes the rest positional even when
	// it starts with '-' (the ruled delta).
	p, err := splitVerbArgs([]string{"--from", "c/o", "--force", "--", "--force", "-x"}, valued, bare, false)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := p.has("--from"); !ok || v != "c/o" {
		t.Errorf("--from = %q,%v", v, ok)
	}
	if !p.flags["--force"] {
		t.Error("--force flag not set")
	}
	if strings.Join(p.pos, " ") != "--force -x" {
		t.Errorf("positionals after -- = %v", p.pos)
	}
}

func TestSplitVerbArgsNonStrictUnknownIsPositional(t *testing.T) {
	// send is non-strict: an unknown --flag begins the message body, not an error.
	p, err := splitVerbArgs([]string{"--unknown", "text"}, map[string]bool{"--from": true}, map[string]bool{"--force": true}, false)
	if err != nil {
		t.Fatalf("non-strict must not error on unknown flag: %v", err)
	}
	if strings.Join(p.pos, " ") != "--unknown text" {
		t.Errorf("pos = %v", p.pos)
	}
}

func TestSplitVerbArgsStrictUnknown(t *testing.T) {
	_, err := splitVerbArgs([]string{"--bogus"}, map[string]bool{"--token": true}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "unknown flag --bogus") {
		t.Fatalf("strict unknown flag should error, got %v", err)
	}
}

func TestSplitVerbArgsMissingValue(t *testing.T) {
	_, err := splitVerbArgs([]string{"--from"}, map[string]bool{"--from": true}, nil, false)
	if err == nil || !strings.Contains(err.Error(), "missing value for --from") {
		t.Fatalf("missing value should error, got %v", err)
	}
}

func TestNoExtraTrailingJunk(t *testing.T) {
	if err := noExtra([]string{"a", "b"}, 2, "usage"); err != nil {
		t.Errorf("2 within max 2 must pass: %v", err)
	}
	if err := noExtra([]string{"a", "b", "c"}, 2, "usage: cbus foo"); err == nil {
		t.Fatal("3 beyond max 2 must error")
	}
	if err := noExtra(nil, 0, "usage"); err != nil {
		t.Errorf("nil within max 0 must pass: %v", err)
	}
}

// TestTrailingJunkRejectedByVerbs drives run() end-to-end: a fixed-arity verb rejects
// an extra positional (the ruled delta) rather than silently discarding it.
func TestTrailingJunkRejectedByVerbs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	cases := [][]string{
		{"whoami", "junk"},
		{"channels", "junk"},
		{"inbox", "ch/al", "junk"},
		{"join", "ch", "al", "junk"},
		{"prune", "ch", "junk"},
		{"bootstrap", "ch", "parent", "junk"},
	}
	for _, args := range cases {
		if rc := run(args); rc == 0 {
			t.Errorf("%v: trailing junk must be rejected (rc!=0)", args)
		}
	}
}

// TestSendDashDashBody drives run(): `send ch/al -- -literal` sends the literal body.
func TestSendDashDashBody(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "S")
	// a target peer to receive it.
	seedPeer(t, root, "ch", "al", `{"alias":"al","channel":"ch","sessionId":"other","listenerPid":null,"host":"h","cwd":"/w","ts":"t","lastActivity":"2999-01-01T00:00:00Z"}`)
	if rc := run([]string{"send", "ch/al", "--", "-x --not-a-flag"}); rc != 0 {
		t.Fatalf("send with -- body rc=%d", rc)
	}
	b, err := os.ReadFile(filepath.Join(root, "ch", "al", "inbox.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "-x --not-a-flag") {
		t.Errorf("body not delivered: %q", b)
	}
}
