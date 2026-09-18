//go:build darwin || linux

package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestManagedPeerCloseRefusesSharedDaemon(t *testing.T) {
	root := setupStore(t)
	path := seedManagedPeer(t, root, "managed", "OTHER")
	pid := liveProc(t) // disposable process; this test never names a real harness pid
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m peerMeta
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m.ListenerPid = json.RawMessage(strconv.Itoa(pid))
	m.ListenerStart = startTokenOf(t, pid)
	m.OwnerPid = json.RawMessage(strconv.Itoa(pid))
	if err := writeMeta(filepath.Dir(path), m); err != nil {
		t.Fatal(err)
	}
	unchanged := managedPeerSnapshot(t, path)
	for _, force := range []bool{false, true} {
		report := ClosePeer("dev", "managed", force)
		if report.Ok || !strings.Contains(report.Detail, "cbus connection disconnect") {
			t.Errorf("managed close must refuse and name disconnect: %+v", report)
		}
		if !pidAlive(pid) {
			t.Fatal("close signalled the shared daemon process")
		}
	}
	unchanged()
}
