package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDaemonLockAvailableDoesNotTreatMissingSocketAsExit(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	if available, err := DaemonLockAvailable(); err != nil || !available {
		t.Fatalf("empty state: %v %v", available, err)
	}
	if err := os.MkdirAll(DaemonDir(), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(DaemonDir(), "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := tryLockExclusive(f); err != nil {
		t.Fatal(err)
	}
	if available, err := DaemonLockAvailable(); err != nil || available {
		t.Fatalf("held lock with absent socket: %v %v", available, err)
	}
	if err := unlockFile(f); err != nil {
		t.Fatal(err)
	}
	if available, err := DaemonLockAvailable(); err != nil || !available {
		t.Fatalf("released lock: %v %v", available, err)
	}
}
