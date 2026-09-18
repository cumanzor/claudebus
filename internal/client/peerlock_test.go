package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPeerLockExcludesSameProcessAndSurvivesAliasReplacement(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	unlock, err := lockPeer("ch", "advisor")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// Replacing an alias directory does not replace the lock's inode.
	dir := filepath.Join(CBUSDir(), "ch", "advisor")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if release, err := lockPeerFor("ch", "advisor", 30*time.Millisecond); err == nil {
		release()
		t.Fatal("second handle acquired a held peer lock")
	} else if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("contention error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("peer lock did not bound contention")
	}
	other, err := lockPeer("ch", "other")
	if err != nil {
		t.Fatalf("unrelated peer blocked: %v", err)
	}
	other()
	unlock()
	again, err := lockPeerFor("ch", "advisor", 0)
	if err != nil {
		t.Fatalf("released lock stayed held: %v", err)
	}
	again()
}

func TestPeerLockProcessProbe(t *testing.T) {
	if os.Getenv("CBUS_TEST_PEER_LOCK_PROBE") != "1" {
		t.Skip("subprocess helper")
	}
	unlock, err := lockPeerFor("ch", "advisor", 50*time.Millisecond)
	if err != nil {
		fmt.Println("peer-lock: blocked")
		return
	}
	unlock()
	fmt.Println("peer-lock: acquired")
}

func TestPeerLockExcludesOtherProcess(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	unlock, err := lockPeer("ch", "advisor")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	probe := func(want string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestPeerLockProcessProbe$")
		cmd.Env = append(os.Environ(), "CBUS_TEST_PEER_LOCK_PROBE=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "peer-lock: "+want) {
			t.Fatalf("lock probe = %s, %v; want %s", out, err, want)
		}
	}
	probe("blocked")
	unlock()
	probe("acquired")
}

func TestPeerLockCanonicalOrderAndEquivalentNames(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	unlock, err := lockPeers("ch", "B", "a", "A", "b.")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	for _, alias := range []string{"a", "B", "A.", "b"} {
		if release, err := lockPeerFor("CH", alias, 0); err == nil {
			release()
			t.Fatalf("equivalent alias %q acquired a second lock", alias)
		}
	}
}

func TestPeerLockGuardsStoreLifecycle(t *testing.T) {
	for _, name := range []string{"join", "reserve", "leave", "unregister", "rename source", "rename target"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CBUS_DIR", t.TempDir())
			clearSessionEnv(t)
			t.Setenv("CBUS_SESSION_ID", "SELF")
			sid := "SELF"
			if name == "join" || name == "reserve" {
				sid = "OTHER"
			}
			seedMeta(t, CBUSDir(), "ch", "advisor", sid)
			lockAlias := "advisor"
			if name == "rename target" {
				lockAlias = "renamed"
			}
			unlock, err := lockPeer("ch", lockAlias)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			done := make(chan error, 1)
			go func() {
				var err error
				switch name {
				case "join":
					_, _, err = Join("ch", "advisor")
				case "reserve":
					_, err = ReserveAlias("ch", "advisor", OriginFresh, "")
				case "leave":
					_, err = Leave("ch")
				case "unregister":
					err = Unregister("ch", "advisor")
				default:
					_, _, _, err = Rename("renamed", "ch")
				}
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("lifecycle mutation bypassed held lock: %v", err)
			case <-time.After(40 * time.Millisecond):
			}
			if got := metaSessionID(filepath.Join(CBUSDir(), "ch", "advisor", "meta.json")); got != sid {
				t.Fatalf("locked registration changed to %q", got)
			}
			unlock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("lifecycle operation did not resume after unlock")
			}
		})
	}
}

func TestPeerLockPruneRechecksReplacement(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	dir := filepath.Join(CBUSDir(), "ch", "advisor")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	m := peerMeta{Alias: "advisor", Channel: "ch", SessionID: "OLD", ListenerPid: []byte("4194304"), OwnerPid: jsonNull}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockPeer("ch", "advisor")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	done := make(chan []string, 1)
	go func() { done <- PruneChannel("ch") }()
	select {
	case msgs := <-done:
		t.Fatalf("prune bypassed lock: %v", msgs)
	case <-time.After(40 * time.Millisecond):
	}
	m.SessionID, m.ConnectionID = "NEW", "managed-new"
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	unlock()
	select {
	case msgs := <-done:
		if len(msgs) != 0 {
			t.Fatalf("new registration was pruned: %v", msgs)
		}
	case <-time.After(time.Second):
		t.Fatal("prune did not resume after unlock")
	}
	if got := metaSessionID(filepath.Join(dir, "meta.json")); got != "NEW" {
		t.Fatalf("replacement lost: session = %q", got)
	}
}
