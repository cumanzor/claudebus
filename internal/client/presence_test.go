package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendInboxPreservesLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	for _, line := range []string{`{"text":"first"}`, `{"text":"second"}`} {
		if err := appendInbox(path, []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "{\"text\":\"first\"}\n{\"text\":\"second\"}\n"
	if string(data) != want {
		t.Errorf("inbox = %q, want %q", data, want)
	}
}

func TestBroadcastPresenceContinuesAfterInboxFailure(t *testing.T) {
	root := setupStore(t)
	seedPeer(t, root, "dev", "a-broken", "OTHER")
	seedPeer(t, root, "dev", "z-healthy", "ANOTHER")
	path := filepath.Join(root, "dev", "a-broken", "inbox.jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	BroadcastPresence("dev", "sender", "join", "joined", "sender")
	if got := inbox(t, root, "dev", "z-healthy"); !strings.Contains(got, `"event":"join"`) {
		t.Errorf("healthy recipient did not receive presence after earlier inbox failure: %q", got)
	}
}
