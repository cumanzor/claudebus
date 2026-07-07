// Package spool is a Maildir-style per-peer message store:
// <root>/<channel>/<alias>/{tmp,new,cur}. Writes land in tmp/ and are
// renamed into new/ (atomic on one filesystem); delivery moves new/ → cur/.
// Process-crash-safe by construction: a message is either invisible (tmp),
// queued (new), or delivered (cur) — never truncated, never half-read.
// NOT power-loss durable (no fsync): acceptable for a session bus. Ordering
// is by wall-clock name; a backwards clock step can reorder across the step.
package spool

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

var seq atomic.Uint64

type Store struct{ Root string }

func (s Store) peerDir(channel, alias string) string {
	return filepath.Join(s.Root, channel, alias)
}

// NewDir returns the queued-messages dir for a peer.
func (s Store) NewDir(channel, alias string) string {
	return filepath.Join(s.peerDir(channel, alias), "new")
}

func (s Store) ensure(channel, alias string) error {
	base := s.peerDir(channel, alias)
	for _, d := range []string{"tmp", "new", "cur"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Write queues one message line for a peer and returns its filename.
func (s Store) Write(channel, alias string, line []byte) (string, error) {
	if err := s.ensure(channel, alias); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%d.%06d.json", time.Now().UnixNano(), seq.Add(1))
	base := s.peerDir(channel, alias)
	tmp := filepath.Join(base, "tmp", name)
	if err := os.WriteFile(tmp, line, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(base, "new", name)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// ListNew returns queued filenames in enqueue order (names sort by time.seq).
func (s Store) ListNew(channel, alias string) ([]string, error) {
	entries, err := os.ReadDir(s.NewDir(channel, alias))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Read returns a queued message's content.
func (s Store) Read(channel, alias, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.NewDir(channel, alias), name))
}

// MarkDelivered moves a message new/ → cur/.
func (s Store) MarkDelivered(channel, alias, name string) error {
	base := s.peerDir(channel, alias)
	return os.Rename(filepath.Join(base, "new", name), filepath.Join(base, "cur", name))
}

// Peers walks the spool tree and returns every channel/alias pair present.
func (s Store) Peers() (map[string]int, error) {
	out := map[string]int{}
	channels, err := os.ReadDir(s.Root)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, ch := range channels {
		if !ch.IsDir() {
			continue
		}
		aliases, err := os.ReadDir(filepath.Join(s.Root, ch.Name()))
		if err != nil {
			continue
		}
		for _, al := range aliases {
			if !al.IsDir() {
				continue
			}
			queued, err := s.ListNew(ch.Name(), al.Name())
			if err != nil {
				return nil, err
			}
			out[ch.Name()+"/"+al.Name()] = len(queued)
		}
	}
	return out, nil
}
