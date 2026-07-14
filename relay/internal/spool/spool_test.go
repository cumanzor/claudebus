package spool

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, s Store, ch, al, text string) {
	t.Helper()
	if _, err := s.Write(ch, al, []byte(text+"\n")); err != nil {
		t.Fatalf("write %s/%s: %v", ch, al, err)
	}
}

// Remove drops a delivered/idle peer, keeps one with queued mail, and rmdir's the
// channel once its last peer is gone.
func TestRemove(t *testing.T) {
	s := Store{Root: t.TempDir()}

	// idle: one message written then delivered -> new/ empty, dir lingers.
	write(t, s, "c", "idle", "hi")
	names, _ := s.ListNew("c", "idle")
	if err := s.MarkDelivered("c", "idle", names[0]); err != nil {
		t.Fatal(err)
	}
	// pending: undelivered mail in new/.
	write(t, s, "c", "pending", "waiting")

	if ok, err := s.Remove("c", "pending"); err != nil || ok {
		t.Fatalf("Remove pending = (%v,%v), want (false,nil) — queued mail must survive", ok, err)
	}
	if _, err := os.Stat(s.NewDir("c", "pending")); err != nil {
		t.Fatalf("pending peer dir gone after keep: %v", err)
	}

	if ok, err := s.Remove("c", "idle"); err != nil || !ok {
		t.Fatalf("Remove idle = (%v,%v), want (true,nil)", ok, err)
	}
	if _, err := os.Stat(s.peerDir("c", "idle")); !os.IsNotExist(err) {
		t.Fatalf("idle peer dir still present after Remove: %v", err)
	}

	// removing the last peer rmdir's the channel.
	if ok, _ := s.Remove("c", "pending"); ok {
		t.Fatal("pending removed despite mail — bug")
	}
}

// Remove of an absent peer is a no-op, not an error.
func TestRemoveMissing(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if ok, err := s.Remove("c", "ghost"); err != nil || ok {
		t.Fatalf("Remove missing = (%v,%v), want (false,nil)", ok, err)
	}
}

// Peers ignores dot-prefixed dirs (transient claim dirs, invisible peer names).
func TestPeersSkipsDotDirs(t *testing.T) {
	s := Store{Root: t.TempDir()}
	write(t, s, "c", "real", "hi")
	if err := os.MkdirAll(filepath.Join(s.Root, "c", ".prune.1.1", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, ".hidden", "x", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	peers, err := s.Peers()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := peers["c/real"]; !ok {
		t.Fatalf("real peer missing: %v", peers)
	}
	if len(peers) != 1 {
		t.Fatalf("dot dirs leaked into Peers: %v", peers)
	}
}
