package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type durableTailOwner struct {
	Key      string `json:"key"`
	Consumer string `json:"consumer"`
	Durable  bool   `json:"durable"`
}

func (s *server) tailOwnerPath(key string) string {
	return filepath.Join(s.store.Root, ".durable-owners", fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
}

// This identity survives spool prune and process restart. Caller holds the peer
// gate, and records it before publishing a successfully upgraded subscription.
func (s *server) recordTailOwner(key, consumer string, durable bool) error {
	if err := s.store.EnsureRoot(); err != nil {
		return err
	}
	path := s.tailOwnerPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := relaySyncDir(s.store.Root); err != nil {
		return err
	}
	return saveRelayJSON(path, durableTailOwner{key, consumer, durable})
}

func (s *server) presenceRecipientMatches(key, consumer string) (bool, error) {
	if consumer == "" {
		return true, nil
	} // Legacy recipients retain alias addressing.
	b, err := os.ReadFile(s.tailOwnerPath(key))
	if err != nil {
		return false, err
	}
	var owner durableTailOwner
	if err = json.Unmarshal(b, &owner); err != nil {
		return false, err
	}
	if owner.Key != key {
		return false, fmt.Errorf("durable recipient ownership does not match %s", key)
	}
	return owner.Durable && owner.Consumer == consumer, nil
}

func presenceRecipientSpoolID(id, consumer string) string {
	if consumer == "" {
		return id
	}
	return strings.TrimSuffix(id, ".json") + fmt.Sprintf(".recipient.%x.json", sha256.Sum256([]byte(consumer)))
}

// Epoch-scoped presence cannot enter a replacement consumer's model even when
// publication committed before the source's fanout progress did. Ordinary mail
// and legacy presence retain their existing alias-addressed semantics.
func presenceSpoolMatchesTail(id string, t *tail) bool {
	parts := strings.Split(id, ".")
	if len(parts) != 6 || parts[1] != "presence" || parts[3] != "recipient" {
		return true
	}
	return t.durable && parts[4] == fmt.Sprintf("%x", sha256.Sum256([]byte(t.consumer)))
}

// Never prune the dedup evidence between publication and its progress journal.
// A malformed/unreadable journal makes prune conservative, not destructive.
// Caller holds the recipient gate; publication/progress share that same gate.
func (s *server) hasUnfinishedPresenceRecipient(channel, alias string) bool {
	dir := filepath.Join(s.store.Root, ".durable-events")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return true
		}
		var journal durablePresenceJournal
		if json.Unmarshal(b, &journal) != nil {
			return true
		}
		if journal.Channel != channel {
			continue
		}
		for _, r := range journal.Recipients {
			if r.Alias == alias && !r.Done {
				return true
			}
		}
	}
	return false
}
