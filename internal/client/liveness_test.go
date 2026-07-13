package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReadPeerMeta(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, "meta.json")
	_ = os.WriteFile(mp, []byte(`{"listenerPid":1234,"ownerPid":null,"host":"mbp","cwd":"/x","alias":"a"}`), 0o644)
	if m, ok := ReadPeerMeta(mp); !ok || m.ListenerPid != 1234 || m.OwnerPid != 0 || m.Host != "mbp" || m.Cwd != "/x" {
		t.Errorf("meta = %+v ok=%v", m, ok)
	}
	// digit-coerced pid stored as a string is tolerated
	_ = os.WriteFile(mp, []byte(`{"listenerPid":"42"}`), 0o644)
	if m, _ := ReadPeerMeta(mp); m.ListenerPid != 42 {
		t.Errorf("string pid = %d, want 42", m.ListenerPid)
	}
	if _, ok := ReadPeerMeta(filepath.Join(dir, "nope.json")); ok {
		t.Error("missing meta should be not-ok")
	}
}

func TestMetaListenerAlive(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "inbox.jsonl")
	_ = os.WriteFile(inbox, nil, 0o644)
	mp := filepath.Join(dir, "meta.json")
	writeMeta := func(body string) { _ = os.WriteFile(mp, []byte(body), 0o644) }

	// a live process whose argv references the inbox path (tail -f blocks and
	// keeps the path in argv, like the real follower)
	live := exec.Command("tail", "-f", inbox)
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Process.Kill() }()

	// a live process whose argv does NOT reference the inbox
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill() }()

	// a reaped (dead) pid
	dead := exec.Command("/bin/sh", "-c", "true")
	_ = dead.Run()
	deadPid := dead.Process.Pid

	writeMeta(fmt.Sprintf(`{"listenerPid":%d}`, live.Process.Pid))
	if !MetaListenerAlive(mp) {
		t.Error("live listener with the inbox in argv should be listening")
	}
	writeMeta(`{"listenerPid":null}`)
	if MetaListenerAlive(mp) {
		t.Error("null listenerPid must be off")
	}
	writeMeta(fmt.Sprintf(`{"listenerPid":%d}`, other.Process.Pid))
	if MetaListenerAlive(mp) {
		t.Error("a pid whose argv lacks the inbox must be off (recycling guard)")
	}
	writeMeta(fmt.Sprintf(`{"listenerPid":%d}`, deadPid))
	if MetaListenerAlive(mp) {
		t.Error("a dead pid must be off")
	}
	writeMeta(fmt.Sprintf(`{"listenerPid":%d,"ownerPid":%d}`, live.Process.Pid, deadPid))
	if MetaListenerAlive(mp) {
		t.Error("a dead owner must make it off (crash-orphan guard)")
	}
}
