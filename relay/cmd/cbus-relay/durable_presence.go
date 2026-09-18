package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"claudebus/internal/core"
)

type durablePresenceRecipient struct {
	Alias    string `json:"alias"`
	Consumer string `json:"consumer,omitempty"`
	Done     bool   `json:"done"`
}
type durablePresenceJournal struct {
	Channel    string                     `json:"channel"`
	Alias      string                     `json:"alias"`
	SpoolID    string                     `json:"spoolId"`
	Consumer   string                     `json:"consumer"`
	Frame      durableClientFrame         `json:"frame"`
	Recipients []durablePresenceRecipient `json:"recipients"`
}

// The ACK means frozen fanout is resolved: each item is durably spooled, or its
// recipient epoch was superseded and deliberately skipped. Interrupted attempts
// remain unacknowledged; the caller retries the same event from its durable outbox.
// This journal plus immutable named spool items deduplicates those retries.
func (s *server) acceptDurablePresence(key string, t *tail, ch, al string, frame durableClientFrame) error {
	if frame.EventID == "" || len(frame.EventID) > 256 || strings.ContainsAny(frame.EventID, "\r\n\x00") || frame.Text == "" || len(frame.Text) > 16<<10 {
		return errors.New("invalid durable presence identity or text")
	}
	if frame.Event != "join" && frame.Event != "departed" && frame.Event != "leave" {
		return errors.New("invalid durable presence event")
	}
	if _, err := time.Parse(time.RFC3339, frame.TS); err != nil {
		return errors.New("invalid durable presence timestamp")
	}
	// Presence operations serialize before acquiring source/recipient gates. That
	// order prevents reciprocal A->B / B->A fanout from deadlocking their tail gates.
	s.presenceMu.Lock()
	defer s.presenceMu.Unlock()
	sourceGate := s.hub.tailGate(key)
	sourceGate.Lock()
	defer sourceGate.Unlock()
	if !s.hub.currentTail(key, t) {
		return errors.New("presence source subscription displaced")
	}
	if err := s.store.EnsureRoot(); err != nil {
		return err
	}
	dir := filepath.Join(s.store.Root, ".durable-events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := relaySyncDir(s.store.Root); err != nil {
		return err
	}
	identity := fmt.Sprintf("%s\x00%s\x00%s", key, t.consumer, frame.EventID)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
	path := filepath.Join(dir, digest+".json")
	journal := durablePresenceJournal{SpoolID: fmt.Sprintf("%d.presence.%s.json", time.Now().UnixNano(), digest), Channel: ch, Alias: al, Consumer: t.consumer, Frame: frame}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &journal); err != nil {
			return fmt.Errorf("read presence journal: %w", err)
		}
		if !core.ValidStoreName(journal.SpoolID) {
			return errors.New("presence journal has invalid spool id")
		}
		if journal.Channel != ch || journal.Alias != al || journal.Consumer != t.consumer || journal.Frame != frame {
			return errors.New("presence event ID already names different source or payload")
		}
	case os.IsNotExist(err):
		s.hub.mu.Lock()
		for address, target := range s.hub.tails {
			channel, alias, _ := strings.Cut(address, "/")
			if channel == ch && alias != al {
				journal.Recipients = append(journal.Recipients, durablePresenceRecipient{Alias: alias, Consumer: target.consumer})
			}
		}
		s.hub.mu.Unlock()
		sort.Slice(journal.Recipients, func(i, j int) bool { return journal.Recipients[i].Alias < journal.Recipients[j].Alias })
		if err := s.savePresence(path, journal); err != nil {
			return err
		}
	default:
		return err
	}
	for i, recipient := range journal.Recipients {
		if recipient.Done {
			continue
		}
		if !core.ValidStoreName(recipient.Alias) {
			return errors.New("presence journal has invalid recipient")
		}
		targetKey := ch + "/" + recipient.Alias
		gate := s.hub.tailGate(targetKey)
		gate.Lock()
		matches, err := s.presenceRecipientMatches(targetKey, recipient.Consumer)
		var payload []byte
		if err == nil && matches {
			payload, err = json.Marshal(core.Message{From: key, To: targetKey, TS: frame.TS, Text: frame.Text, Kind: "presence", Event: frame.Event, EventID: frame.EventID})
		}
		if err == nil && matches {
			err = s.store.WriteNamed(ch, recipient.Alias, presenceRecipientSpoolID(journal.SpoolID, recipient.Consumer), append(payload, '\n'))
		}
		if err == nil {
			journal.Recipients[i].Done = true
			err = s.savePresence(path, journal)
		}
		gate.Unlock()
		if err != nil {
			return err
		}
		s.hub.poke(targetKey)
	}
	return nil
}

func relaySyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func saveDurablePresenceJournal(path string, value durablePresenceJournal) error {
	return saveRelayJSON(path, value)
}

func saveRelayJSON(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".presence-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return relaySyncDir(filepath.Dir(path))
}

func (s *server) savePresence(path string, journal durablePresenceJournal) error {
	if s.savePresenceJournal != nil {
		return s.savePresenceJournal(path, journal)
	}
	return saveDurablePresenceJournal(path, journal)
}
