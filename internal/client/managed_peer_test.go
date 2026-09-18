package client

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedManagedPeer(t *testing.T, root, alias, sid string) string {
	t.Helper()
	seedPeerPid(t, root, "dev", alias, sid, "999999")
	dir := filepath.Join(root, "dev", alias)
	path := filepath.Join(dir, "meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m peerMeta
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m.ConnectionID = "connection-epoch"
	m.Harness = "codex"
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inbox.jsonl"), []byte("queued-before-restart\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func managedPeerSnapshot(t *testing.T, metaPath string) func() {
	t.Helper()
	files := map[string][]byte{}
	for _, path := range []string{metaPath, filepath.Join(filepath.Dir(metaPath), "inbox.jsonl")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = data
	}
	return func() {
		t.Helper()
		for path, want := range files {
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(want) {
				t.Errorf("managed peer file changed: %s, err=%v", path, err)
			}
		}
	}
}

func TestManagedPeerSurvivesOfflinePrune(t *testing.T) {
	root := setupStore(t)
	path := seedManagedPeer(t, root, "managed", "OTHER")
	unchanged := managedPeerSnapshot(t, path)
	if m, ok := ReadPeerMeta(path); !ok || m.ConnectionID != "connection-epoch" {
		t.Fatalf("managed identity not readable: %+v, ok=%v", m, ok)
	}
	if MetaListenerAlive(path) {
		t.Fatal("fixture requires an offline listener")
	}
	if PeerDead(path) {
		t.Fatal("an offline managed peer must retain its durable inbox")
	}
	if messages := PruneChannel("dev"); len(messages) != 0 {
		t.Fatalf("managed peer must not be pruned: %v", messages)
	}
	unchanged()
	if _, _, _, err := LocalSend("dev/managed", "sender", false, "later"); err == nil {
		t.Error("offline managed listener still requires --force to queue")
	}
	if _, _, warn, err := LocalSend("dev/managed", "sender", true, "later"); err != nil || !warn {
		t.Errorf("forced offline enqueue = warn %v, err %v", warn, err)
	}
	if got := inbox(t, root, "dev", "managed"); !strings.HasPrefix(got, "queued-before-restart\n") || !strings.Contains(got, `"text":"later"`) {
		t.Fatalf("offline enqueue lost queued content: %q", got)
	}
}

func TestManagedPeerJoinPreservesBinding(t *testing.T) {
	for _, sid := range []string{"SID", "OTHER"} {
		t.Run(sid, func(t *testing.T) {
			root := setupStore(t)
			path := seedManagedPeer(t, root, "managed", sid)
			unchanged := managedPeerSnapshot(t, path)
			alias, already, err := Join("dev", "managed")
			if sid == "SID" {
				if err != nil || !already || alias != "managed" {
					t.Errorf("same-session join must be a no-op: %q, %v, %v", alias, already, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "cbus connect") {
				t.Errorf("replacement join must direct the caller to connect: %v", err)
			}
			unchanged()
		})
	}
}

func TestManagedPeerRejectsAliasReplacement(t *testing.T) {
	t.Run("reservation", func(t *testing.T) {
		root := setupStore(t)
		path := seedManagedPeer(t, root, "managed", "OTHER")
		unchanged := managedPeerSnapshot(t, path)
		if _, err := ReserveAlias("dev", "managed", OriginFresh, ""); err == nil || !strings.Contains(err.Error(), "daemon-managed") {
			t.Errorf("reservation must preserve a managed alias: %v", err)
		}
		unchanged()
	})
	t.Run("rename target", func(t *testing.T) {
		root := setupStore(t)
		path := seedManagedPeer(t, root, "managed", "OTHER")
		seedPeer(t, root, "dev", "mine", "SID")
		unchanged := managedPeerSnapshot(t, path)
		if _, _, _, err := Rename("managed", "dev"); err == nil || !strings.Contains(err.Error(), "daemon-managed") {
			t.Errorf("rename must preserve a managed target: %v", err)
		}
		unchanged()
		if !dirExists(filepath.Join(root, "dev", "mine")) {
			t.Error("rejected rename removed its source")
		}
	})
	t.Run("rename source", func(t *testing.T) {
		root := setupStore(t)
		path := seedManagedPeer(t, root, "managed", "SID")
		unchanged := managedPeerSnapshot(t, path)
		if _, _, _, err := Rename("new-name", "dev"); err == nil || !strings.Contains(err.Error(), "disconnect") {
			t.Errorf("managed rename must direct the caller to disconnect/connect: %v", err)
		}
		unchanged()
		if dirExists(filepath.Join(root, "dev", "new-name")) {
			t.Error("rejected rename created its destination")
		}
	})
}

func TestManagedPeerRejectsTail(t *testing.T) {
	for _, steal := range []bool{false, true} {
		name := "normal"
		if steal {
			name = "steal"
		}
		t.Run(name, func(t *testing.T) {
			root := setupStore(t)
			path := seedManagedPeer(t, root, "managed", "SID")
			unchanged := managedPeerSnapshot(t, path)
			done := make(chan error, 1)
			go func() { done <- armLocalTailTo("dev/managed", steal, writerSink{io.Discard}) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "daemon-managed") {
					t.Errorf("managed tail must be refused: %v", err)
				}
			case <-time.After(2 * time.Second):
				_ = os.RemoveAll(filepath.Dir(path)) // release an incorrectly started follower
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				t.Fatal("tail entered a follower loop for a managed peer")
			}
			unchanged()
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), cursorFile)); !os.IsNotExist(err) {
				t.Errorf("refused tail must not create a cursor: %v", err)
			}
		})
	}
}

func TestManagedPeerMetaRewriters(t *testing.T) {
	root := setupStore(t)
	path := seedManagedPeer(t, root, "managed", "SID")
	unchanged := managedPeerSnapshot(t, path)
	armMeta(path, selfStart(t))
	unchanged()
	// The typed rewriter must carry the new field even though the public rename
	// operation is refused for managed peers.
	if err := renameMeta(filepath.Dir(path), "renamed"); err != nil {
		t.Fatal(err)
	}
	if m, ok := ReadPeerMeta(path); !ok || m.ConnectionID != "connection-epoch" {
		t.Fatalf("typed meta rewrite lost managed identity: %+v, ok=%v", m, ok)
	}
}

func TestManagedPeerWrapperClaimPreservesCursor(t *testing.T) {
	root := setupStore(t)
	path := seedManagedPeer(t, root, "managed", "SID")
	unchanged := managedPeerSnapshot(t, path)
	dir := filepath.Dir(path)
	dev, ino, _, ok := fileIdentity(filepath.Join(dir, "inbox.jsonl"))
	if !ok {
		t.Fatal("cannot establish inbox identity")
	}
	writeCursor(dir, dev, ino, 7)
	claimListenerAtJoin("dev", "managed")
	unchanged()
	if gotDev, gotIno, offset, state := readCursor(dir); state != cursorValid || gotDev != dev || gotIno != ino || offset != 7 {
		t.Fatalf("legacy wrapper altered managed cursor: %d, %d, %d, %v", gotDev, gotIno, offset, state)
	}
}

func TestManagedPeerExplicitRemoval(t *testing.T) {
	for _, self := range []bool{false, true} {
		name, sid := "unregister", "OTHER"
		if self {
			name, sid = "leave", "SID"
		}
		t.Run(name, func(t *testing.T) {
			root := setupStore(t)
			path := seedManagedPeer(t, root, "managed", sid)
			if self {
				if left, err := Leave("dev"); err != nil || len(left) != 1 {
					t.Fatalf("managed leave = %v, err=%v", left, err)
				}
			} else if err := Unregister("dev", "managed"); err != nil {
				t.Fatal(err)
			}
			if dirExists(filepath.Dir(path)) {
				t.Error("explicit removal must remove the managed registration")
			}
		})
	}
}
