package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Only the durable top-level `compacted` record is used. Codex also renders
// completion events, which must not produce a second notice for one compaction.
// Its pre-compaction ItemStarted event is not persisted in an ordinary rollout.
type compactionObservation struct {
	Dev        uint64 `json:"dev"`
	Ino        uint64 `json:"ino"`
	Offset     int64  `json:"offset"`
	Completed  uint64 `json:"completed"`
	ObservedAt string `json:"observedAt"`
}

func (d *busDaemon) observeCompaction(c *ConnectionState) error {
	if err := validateConnectionAdapter(c); err != nil {
		return err
	}
	if daemonHarness(c.Harness) == daemonHarnessClaude {
		return nil // Claude lifecycle hooks retain their own compaction grammar.
	}
	if c.RolloutPath == "" || c.State == "detached" || c.State == "binding-required" {
		return nil
	}
	path, err := validateConsumerRollout(c.RolloutPath, c.ThreadID)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// Validate the descriptor we will scan, not only the path checked before
	// open: a replacement between those operations must not borrow the old ID.
	if err := validateCompactionFile(f, c.ThreadID); err != nil {
		return err
	}
	dev, ino, size, ok := fileIdentityOf(f)
	if !ok {
		return errors.New("inspect exact compaction rollout identity")
	}
	next := cloneConnection(c)
	if c.Compaction == nil || c.Compaction.Dev != dev || c.Compaction.Ino != ino || size < c.Compaction.Offset || c.State == "disconnected" {
		end, err := rolloutCompleteEnd(f, size)
		if err != nil {
			return err
		}
		completed := uint64(0)
		if c.Compaction != nil {
			completed = c.Compaction.Completed
			if c.Compaction.Dev == dev && c.Compaction.Ino == ino && c.Compaction.Offset == end {
				return nil
			}
		}
		next.Compaction = &compactionObservation{Dev: dev, Ino: ino, Offset: end, Completed: completed, ObservedAt: Now()}
		// Baseline an existing file without replaying historical compactions.
		if err := d.save(next); err != nil {
			return err
		}
		*c = *next
		return nil
	}
	if size == c.Compaction.Offset {
		return nil
	}
	if _, err := f.Seek(c.Compaction.Offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(f, 64<<10)
	for records := 0; records < 256; records++ {
		line, err := readRolloutLine(reader)
		if errors.Is(err, io.EOF) {
			break
		} // never consume an unfinished physical tail
		if err != nil {
			return err
		}
		var item struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &item); err != nil {
			return fmt.Errorf("invalid complete compaction rollout record: %w", err)
		}
		if item.Type == "compacted" {
			// Retain the existing local-only compaction contract. Remote consumer
			// presence is a separate protocol and must not widen that policy.
			if c.Relay == nil {
				if err := d.preparePresence(next, "compact-post", "Codex context compacted; in-context state was reset (observed completion)"); err != nil {
					return err
				}
			}
			next.Compaction.Completed++
		}
		next.Compaction.Offset += int64(len(line))
		next.Compaction.ObservedAt = Now()
		if next.Compaction.Offset-c.Compaction.Offset >= 8<<20 {
			break
		}
	}
	if next.Compaction.Offset != c.Compaction.Offset {
		// Cursor and presence outbox commit together, so a restart cannot repeat
		// or silently skip a completion whose fanout has yet to finish.
		if err := d.save(next); err != nil {
			return err
		}
		*c = *next
	}
	return d.flushPresence(c)
}

func validateCompactionFile(f *os.File, thread string) error {
	line, err := readRolloutLine(bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("read opened compaction rollout identity: %w", err)
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &meta); err != nil || meta.Type != "session_meta" || meta.Payload.ID != thread {
		return errors.New("opened compaction rollout does not match registered thread")
	}
	return nil
}

// A single replacement-history record may exceed the bus message size. The
// cap still bounds memory; the content is inspected locally and never forwarded.
func readRolloutLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > 64<<20 {
			return nil, errors.New("compaction rollout record exceeds 64MiB observation limit")
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func rolloutCompleteEnd(f *os.File, size int64) (int64, error) {
	buf := make([]byte, 4096)
	for end := size; end > 0; {
		start := end - int64(len(buf))
		if start < 0 {
			start = 0
		}
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, nil
}
