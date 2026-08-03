//go:build darwin || linux

package main

import (
	"fmt"
	"os"
	"strconv"
)

// swapBinary replaces dst with src on unix, leaving the running binary untouched on
// any failure (S3). os.Rename works in place while the binary runs (the kernel keeps
// the open inode alive). A cross-filesystem src (/tmp on tmpfs) is staged into a
// SIBLING of dst on the same filesystem, then atomically renamed — a direct copy onto
// dst would O_TRUNC the running binary and trip ETXTBSY.
func swapBinary(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	sibling := dst + ".new." + strconv.Itoa(os.Getpid())
	if err := copyFile(src, sibling); err != nil {
		_ = os.Remove(sibling)
		return fmt.Errorf("stage new binary beside %s: %w", dst, err)
	}
	if err := os.Rename(sibling, dst); err != nil {
		_ = os.Remove(sibling)
		return fmt.Errorf("swap new binary into %s: %w", dst, err)
	}
	return nil
}

// cleanDisplaced is a no-op: unix replaces a running binary in place, so nothing is
// ever displaced and nothing can be stuck.
func cleanDisplaced(string) []string { return nil }
