// Package spool is a Maildir-style per-peer message store:
// <root>/<channel>/<alias>/{tmp,new,cur}. Writes land in tmp/ and are
// renamed into new/ (atomic on one filesystem); delivery moves new/ → cur/.
// Process-crash-safe by construction: a message is either invisible (tmp),
// queued (new), or delivered (cur) — never truncated, never half-read.
// File data and directory transitions are fsynced before success. Ordering
// is by wall-clock name; a backwards clock step can reorder across the step.
//
// External readers exist: the dashboard's formations sweep reads {new,cur} dir
// mtimes (read-only, never content) as a peer-activity signal, so this layout
// is a compatibility surface — restructuring it blinds those readers.
package spool

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		if err := mkdirAllDurable(filepath.Join(base, d)); err != nil {
			return err
		}
	}
	// Persist the directory chain too, including roots created on a prior failed
	// publication attempt whose parent Sync may not have completed.
	for path := base; ; path = filepath.Dir(path) {
		if err := syncDir(path); err != nil {
			return err
		}
		if filepath.Clean(path) == filepath.Clean(s.Root) {
			break
		}
		if filepath.Dir(path) == path {
			return fmt.Errorf("spool root is not an ancestor")
		}
	}
	return syncDir(filepath.Dir(s.Root))
}

// EnsureRoot durably creates the spool root for metadata journals that can be
// accepted before the first peer message exists.
func (s Store) EnsureRoot() error {
	if err := mkdirAllDurable(s.Root); err != nil {
		return err
	}
	if err := syncDir(s.Root); err != nil {
		return err
	}
	return syncDir(filepath.Dir(s.Root))
}

// Write queues one message line for a peer and returns its filename.
func (s Store) Write(channel, alias string, line []byte) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%d.%06d.%x.json", time.Now().UnixNano(), seq.Add(1), random)
	return name, s.WriteNamed(channel, alias, name, line)
}

// WriteNamed idempotently publishes immutable bytes under a stable delivery ID.
// Existing new/cur bytes must agree. The caller must serialize a reused name
// with pruning; normal Write uses unpredictable unique names.
func (s Store) WriteNamed(channel, alias, name string, line []byte) error {
	if name == "" || name == "." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
		return errors.New("invalid spool id")
	}
	if err := s.ensure(channel, alias); err != nil {
		return err
	}
	base := s.peerDir(channel, alias)
	for _, dir := range []string{"new", "cur"} {
		existing, err := os.ReadFile(filepath.Join(base, dir, name))
		if err == nil {
			if !bytes.Equal(existing, line) {
				return errors.New("spool id already names different bytes")
			}
			return syncExisting(filepath.Join(base, dir, name))
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	f, err := os.CreateTemp(filepath.Join(base, "tmp"), ".write-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(line); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Link(tmp, filepath.Join(base, "new", name)); err != nil {
		if os.IsExist(err) {
			existing, readErr := s.Read(channel, alias, name)
			if readErr == nil && bytes.Equal(existing, line) {
				return syncExisting(filepath.Join(base, "new", name))
			}
		}
		return err
	}
	if err = os.Remove(tmp); err != nil {
		return err
	}
	if err = syncDir(filepath.Join(base, "new")); err != nil {
		return err
	}
	return syncDir(filepath.Join(base, "tmp"))
}

func syncExisting(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func mkdirAllDurable(path string) error {
	if st, err := os.Stat(path); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%s is not a directory", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return fmt.Errorf("cannot create spool root %s", path)
	}
	if err := mkdirAllDurable(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	return syncDir(parent)
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
	if err := os.Rename(filepath.Join(base, "new", name), filepath.Join(base, "cur", name)); err != nil {
		return err
	}
	if err := syncDir(filepath.Join(base, "cur")); err != nil {
		return err
	}
	return syncDir(filepath.Join(base, "new"))
}

// Peers walks the spool tree and returns every channel/alias pair present.
// Dot-prefixed entries are skipped at both levels: they are never valid peer
// names the client produces (matching the local store's convention) and they
// shield Prune's transient .prune.* claim dir from being misread as a peer.
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
		if !ch.IsDir() || strings.HasPrefix(ch.Name(), ".") {
			continue
		}
		aliases, err := os.ReadDir(filepath.Join(s.Root, ch.Name()))
		if err != nil {
			continue
		}
		for _, al := range aliases {
			if !al.IsDir() || strings.HasPrefix(al.Name(), ".") {
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

// Remove drops a peer's spool dir, but ONLY if it holds no queued mail. It claims
// the dir with an atomic same-parent rename to a dot-prefixed temp (EXDEV-proof,
// invisible to Peers), rechecks new/ in the claimed copy, and restores the peer if
// a message raced in between the caller's snapshot and the claim — so a concurrent
// send is either fully spooled (peer kept) or surfaced to the sender as a failed
// new/ rename, never silently dropped. Returns whether the peer was removed; the
// channel dir is rmdir'd once it goes empty. A missing peer (already gone, or lost
// to a concurrent pruner) is not an error — it returns (false, nil).
func (s Store) Remove(channel, alias string) (bool, error) {
	base := s.peerDir(channel, alias)
	tmp := filepath.Join(s.Root, channel, fmt.Sprintf(".prune.%d.%d", os.Getpid(), seq.Add(1)))
	if err := os.Rename(base, tmp); err != nil {
		return false, nil
	}
	entries, err := os.ReadDir(filepath.Join(tmp, "new"))
	if err != nil {
		restoreErr := os.Rename(tmp, base)
		return false, errors.Join(err, restoreErr)
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			_ = os.Rename(tmp, base) // mail raced in — restore, keep the peer
			return false, nil
		}
	}
	if err := os.RemoveAll(tmp); err != nil {
		return false, err
	}
	_ = os.Remove(filepath.Join(s.Root, channel)) // rmdir if now empty
	return true, nil
}
