package client

import (
	"errors"
	"os"
	"path/filepath"
)

// DaemonLockAvailable is only a readiness observation, never ownership. The
// replacement must acquire the same persistent lock itself before listening.
// A missing socket alone is insufficient: shutdown may still hold this lock.
func DaemonLockAvailable() (bool, error) {
	f, err := os.OpenFile(filepath.Join(DaemonDir(), "lock"), os.O_RDWR, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := tryLockExclusive(f); err != nil {
		if errors.Is(err, errLockContended) {
			return false, nil
		}
		return false, err
	}
	defer unlockFile(f)
	return true, nil
}
