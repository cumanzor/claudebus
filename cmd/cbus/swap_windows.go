package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// displacedSuffix marks an image moved aside by a swap. PID-qualified so a second
// update cannot collide on the name with an image an older process still holds.
const displacedSuffix = ".old."

// swapBinary replaces dst with src on windows, leaving the running binary in place on
// any failure (S3). It cannot do what unix does: os.Rename is MoveFileEx with
// MOVEFILE_REPLACE_EXISTING, and that clause is denied with ERROR_ACCESS_DENIED when
// any handle is open on the target regardless of share mode, which the loader always
// holds on a running image. Renaming the running image ASIDE is permitted and the
// process keeps executing from the moved file, so dst is vacated first and the new
// binary is placed at the freed path.
//
// Share flags are not the seam: share mode does not govern rename-replace.
func swapBinary(src, dst string) error {
	displaced := dst + displacedSuffix + strconv.Itoa(os.Getpid())
	if err := os.Rename(dst, displaced); err != nil {
		return fmt.Errorf("move the running binary aside (%s): %w", dst, err)
	}
	if err := placeBinary(src, dst); err != nil {
		if back := os.Rename(displaced, dst); back != nil {
			return fmt.Errorf("%w (and the running binary is left at %s: %v)", err, displaced, back)
		}
		return err
	}
	return nil
}

// placeBinary writes src to the now-vacant dst. A direct copy is safe here in a way it
// never is on unix: dst was just vacated, so there is nothing live to truncate.
func placeBinary(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// src sits under %TEMP%, routinely on another volume, and rename cannot cross one.
	if err := copyFile(src, dst); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("place new binary at %s: %w", dst, err)
	}
	return nil
}

// cleanDisplaced removes images an earlier swap left beside exePath and returns the
// names it could not remove, so the caller can say so (D27). A leftover that resists
// removal means another cbus is still running from it, which the user can act on.
// Between updates one displaced image persists on disk deliberately, rather than being
// swept at startup where every invocation would pay a directory read for an artifact
// only selfupdate creates.
//
// Matched by prefix rather than filepath.Glob: exePath is not a pattern, and a '[' in
// a user's path would make Glob silently match nothing.
func cleanDisplaced(exePath string) []string {
	dir := filepath.Dir(exePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	prefix := filepath.Base(exePath) + displacedSuffix
	var stuck []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			stuck = append(stuck, e.Name())
		}
	}
	return stuck
}
