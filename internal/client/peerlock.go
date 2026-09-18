package client

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Peer locks live outside alias directories and are never removed: unregister/reclaim
// must not create a second lock inode while an old daemon still holds the first one.
// Independent open handles exclude one another both within this process and across
// processes (flock on Unix, LockFileEx on Windows). Closing releases on crashes too.
func lockPeer(ch, alias string) (func(), error) {
	return lockPeerFor(ch, alias, 5*time.Second)
}

func peerLockKey(ch, alias string) string {
	// Over-serialize case variants and Windows-equivalent trailing dots rather than
	// allow two lock names to guard the same path on a case-insensitive filesystem.
	canonical := func(s string) string { return strings.ToLower(strings.TrimRight(s, ". ")) }
	return canonical(ch) + "\x00" + canonical(alias)
}

func lockPeerFor(ch, alias string, timeout time.Duration) (func(), error) {
	return lockPeerForContext(context.Background(), ch, alias, timeout)
}

func lockPeerForContext(ctx context.Context, ch, alias string, timeout time.Duration) (func(), error) {
	dir := filepath.Join(CBUSDir(), ".peer-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create peer lock directory: %w", err)
	}
	name := fmt.Sprintf("%x.lock", sha256.Sum256([]byte(peerLockKey(ch, alias))))
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open peer lock %s/%s: %w", ch, alias, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}
		err = tryLockExclusive(f)
		if err == nil {
			var once sync.Once
			return func() { once.Do(func() { _ = unlockFile(f); _ = f.Close() }) }, nil
		}
		if !errors.Is(err, errLockContended) {
			_ = f.Close()
			return nil, fmt.Errorf("lock peer %s/%s: %w", ch, alias, err)
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("peer %s/%s is busy; lifecycle lock timed out after %s", ch, alias, timeout)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// lockPeers uses one canonical order, including when callers request opposite renames.
// A caller already holding one of these locks must not call this helper recursively.
func lockPeers(ch string, aliases ...string) (func(), error) {
	return lockPeersContext(context.Background(), ch, aliases...)
}

func lockPeersContext(ctx context.Context, ch string, aliases ...string) (func(), error) {
	byKey := make(map[string]string, len(aliases))
	for _, alias := range aliases {
		byKey[peerLockKey(ch, alias)] = alias
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var releases []func()
	unlock := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, key := range keys {
		release, err := lockPeerForContext(ctx, ch, byKey[key], 5*time.Second)
		if err != nil {
			unlock()
			return nil, err
		}
		releases = append(releases, release)
	}
	return unlock, nil
}
