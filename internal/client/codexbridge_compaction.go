package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"claudebus/internal/core"
)

// A late app-server connection is not necessarily an item-stream subscriber,
// especially with --no-resume. Observe the exact thread's durable rollout using
// read-only thread/read, never another resume. This compatibility path retains
// legacy best-effort presence semantics; the native daemon has a durable outbox.
func (b *codexBridge) watchCompactions() {
	channel, alias, ok := strings.Cut(b.presenceTarget, "/")
	if !ok || !core.ValidName(channel) || !core.ValidName(alias) {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var path, lastError string
	var cursor *compactionObservation
	for {
		var err error
		if path == "" {
			var raw json.RawMessage
			raw, err = b.conn.call("thread/read", map[string]any{"threadId": b.threadID, "includeTurns": false})
			if err == nil {
				var response struct {
					Thread struct {
						ID   string `json:"id"`
						Path string `json:"path"`
					} `json:"thread"`
				}
				err = json.Unmarshal(raw, &response)
				if err == nil && response.Thread.ID == b.threadID && response.Thread.Path != "" {
					path = response.Thread.Path
				} else if err == nil {
					err = errors.New("exact thread has no persisted compaction path yet")
				}
			}
		}
		if err == nil {
			var next *compactionObservation
			var count int
			next, count, err = bridgeCompactionStep(path, b.threadID, cursor)
			if err == nil {
				cursor = next
				for i := 0; i < count; i++ {
					BroadcastPresence(channel, alias, "compact-post", "Codex context compacted; in-context state was reset (observed completion)", alias)
				}
			}
		}
		if err != nil && err.Error() != lastError {
			lastError = err.Error()
			fmt.Fprintf(os.Stderr, "cbus: compaction observation unavailable: %v\n", err)
		} else if err == nil {
			lastError = ""
		}
		select {
		case <-b.conn.closed:
			return
		case <-ticker.C:
		}
	}
}

func bridgeCompactionStep(path, thread string, previous *compactionObservation) (*compactionObservation, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	if err := validateCompactionFile(f, thread); err != nil {
		return nil, 0, err
	}
	dev, ino, size, ok := fileIdentityOf(f)
	if !ok {
		return nil, 0, errors.New("inspect bridge compaction rollout identity")
	}
	if previous == nil || previous.Dev != dev || previous.Ino != ino || size < previous.Offset {
		end, err := rolloutCompleteEnd(f, size)
		return &compactionObservation{Dev: dev, Ino: ino, Offset: end}, 0, err
	}
	next := *previous
	if _, err := f.Seek(next.Offset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	reader := bufio.NewReaderSize(f, 64<<10)
	count := 0
	for records := 0; records < 256; records++ {
		line, err := readRolloutLine(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		var row struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, 0, err
		}
		if row.Type == "compacted" {
			count++
		}
		next.Offset += int64(len(line))
		if next.Offset-previous.Offset >= 8<<20 {
			break
		}
	}
	return &next, count, nil
}
