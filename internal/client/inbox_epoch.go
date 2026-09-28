package client

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// A journaled inbox identity is (dev, ino). Some volumes renumber st_dev on
// reboot while the inode and bytes survive, so where devMayRenumber is set a
// dev-only change is accepted when the last consumed record and the pending
// record are byte-identical at their journaled offsets. That is not a proof of
// the whole consumed prefix: a reused inode reproducing both records would pass.
// With an unchanged device the check is the caller's own size bound, as before.
func inboxEpochDecision(c *ConnectionState, f *os.File, end int64) (dev uint64, restamp bool, ok bool) {
	dev, ino, size, ok := fileIdentityOf(f)
	if !ok || ino != c.Ino || size < end {
		return 0, false, false
	}
	if dev == c.Dev {
		return dev, false, true
	}
	// Each record read below must reach its boundary, which also bounds size.
	if !devMayRenumber {
		return 0, false, false
	}
	if c.Offset > 0 {
		anchor := inboxAnchor(c)
		if anchor == nil || !inboxRecordMatches(f, anchor.End, anchor.Hash, 0) {
			return 0, false, false
		}
	}
	if c.Pending != nil && !inboxRecordMatches(f, c.Pending.End, c.Pending.Hash, c.Offset) {
		return 0, false, false
	}
	return dev, true, true
}

// Offset advances only at accept and abandon, and each records the attempt it
// consumed. The anchor is whichever of them ends at the current offset.
func inboxAnchor(c *ConnectionState) *queueAttempt {
	if c.LastAccepted != nil && c.LastAccepted.Attempt.End == c.Offset {
		return &c.LastAccepted.Attempt
	}
	for i := len(c.Resolutions) - 1; i >= 0; i-- {
		if c.Resolutions[i].Attempt.End == c.Offset {
			return &c.Resolutions[i].Attempt
		}
	}
	return nil
}

// The record ending at end starts after the previous newline, or at floor.
func inboxRecordMatches(f *os.File, end int64, hash string, floor int64) bool {
	start := max(floor, end-int64(daemonMaxInboxLine)-1)
	if end <= start || hash == "" {
		return false
	}
	buf := make([]byte, end-start)
	if n, err := f.ReadAt(buf, start); n != len(buf) || (err != nil && err != io.EOF) {
		return false
	}
	if buf[len(buf)-1] != '\n' {
		return false
	}
	line := buf
	if i := bytes.LastIndexByte(buf[:len(buf)-1], '\n'); i >= 0 {
		line = buf[i+1:]
	} else if start != floor {
		return false // longer than any inbox record
	}
	return fmt.Sprintf("%x", sha256.Sum256(line)) == hash
}

func inboxEpochRefusal(c *ConnectionState, what string) error {
	return fmt.Errorf("inbox changed or truncated; %s. Save any unread mail past byte %d of the %s inbox, then run cbus unregister %s and connect again", what, c.Offset, ConnectionTarget(c), ConnectionTarget(c))
}

// The caller holds the connection lane and the peer lock. A re-stamp is
// persisted before any delivery relies on it; a failed save refuses.
func (d *busDaemon) adoptInboxEpoch(c *ConnectionState, f *os.File, end int64, what string) error {
	dev, restamp, ok := inboxEpochDecision(c, f, end)
	if !ok {
		return inboxEpochRefusal(c, what)
	}
	if !restamp {
		return nil
	}
	next := *c
	next.Dev = dev
	if err := d.save(&next); err != nil {
		return err
	}
	*c = next
	fmt.Fprintf(os.Stderr, "cbus daemon: %s: inbox device changed with the same inode and matching records; identity re-stamped\n", ConnectionTarget(c))
	return nil
}
