package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claudebus/internal/core"
)

// The record sits outside the peer dir because connection collection and every alias
// reclaim remove that dir, and an exact-session resume must keep its coordinator.
// Same-user file state, like a grant: it names who a seat answers to, nothing stronger.

const (
	coordinatorDoorApply     = "apply"
	coordinatorDoorBootstrap = "bootstrap"
	coordinatorDoorSpawn     = "spawn"
	coordinatorDoorBranch    = "branch"

	// a launch whose child never booted must not hand its coordinator to whoever takes
	// the alias weeks later; a pasted bootstrap prompt can reasonably wait hours
	coordinatorBindWindow = 24 * time.Hour
	coordinatorRecordMax  = 64 << 10
	coordinatorScanMax    = 256
)

// coordinatorRecord: an empty SessionID is a launch nobody has answered yet.
type coordinatorRecord struct {
	Coordinator string `json:"coordinator"`
	Door        string `json:"door"`
	SessionID   string `json:"sessionId"`
	TS          string `json:"ts"`
}

var coordinatorNow = time.Now

func coordinatorRecordPath(ch, alias string) string {
	return filepath.Join(CBUSDir(), ".coordinators", ch, alias+".json")
}

// CoordinatorOf returns the address that coordinates the session holding ch/alias on this
// machine. Every miss reads the same: missing, corrupt, unanswered by a session, nobody
// holding the name, or held by a session other than the one the record is bound to.
func CoordinatorOf(ch, alias string) (string, bool) {
	if !core.ValidStoreName(ch) || !core.ValidStoreName(alias) {
		return "", false
	}
	rec, ok := readCoordinatorRecord(coordinatorRecordPath(ch, alias))
	if !ok || rec.SessionID == "" {
		return "", false
	}
	// settling a new holder is best-effort, so this is what actually keeps a stale
	// record from speaking for them
	if m, ok := ReadPeerMeta(filepath.Join(CBUSDir(), ch, alias, "meta.json")); !ok || m.SessionID != rec.SessionID {
		return "", false
	}
	return rec.Coordinator, true
}

func readCoordinatorRecord(path string) (coordinatorRecord, bool) {
	b, err := readCoordinatorFile(path)
	if err != nil {
		return coordinatorRecord{}, false
	}
	var rec coordinatorRecord
	if json.Unmarshal(b, &rec) != nil || !validCoordinatorAddress(rec.Coordinator) {
		return coordinatorRecord{}, false
	}
	return rec, true
}

func readCoordinatorFile(path string) ([]byte, error) {
	f, err := openSharedRead(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, coordinatorRecordMax+1))
	if err != nil {
		return nil, err
	}
	if len(b) > coordinatorRecordMax {
		return nil, errors.New("coordinator record too large")
	}
	return b, nil
}

func validCoordinatorAddress(addr string) bool {
	ch, alias, ok := strings.Cut(addr, "/")
	return ok && core.ValidStoreName(ch) && core.ValidStoreName(alias)
}

// recordCoordinator writes what a launch told the seat, or removes the record when the
// prompt names nobody. undo puts back what was there, for a launch that did not happen.
func recordCoordinator(ch, alias, coord, door, sid string) (undo func(), err error) {
	if err := checkCoordinatorKey(ch, alias); err != nil {
		return nil, err
	}
	unlock, err := lockPeer(ch, alias)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return writeCoordinatorLocked(ch, alias, coord, door, sid)
}

func checkCoordinatorKey(ch, alias string) error {
	if err := checkStoreName("channel", ch); err != nil {
		return err
	}
	return checkStoreName("alias", alias)
}

func writeCoordinatorLocked(ch, alias, coord, door, sid string) (undo func(), err error) {
	path := coordinatorRecordPath(ch, alias)
	prev, prevErr := readCoordinatorFile(path)
	var wrote []byte
	if coord == "" {
		err = removeCoordinatorFile(path)
	} else {
		wrote, err = marshalCoordinatorRecord(coordinatorRecord{Coordinator: coord, Door: door, SessionID: sid,
			TS: coordinatorNow().UTC().Format(time.RFC3339)})
		if err == nil {
			err = writeCoordinatorBytes(path, wrote)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("record coordinator of %s/%s: %w", ch, alias, err)
	}
	return func() {
		unlock, lockErr := lockPeer(ch, alias)
		if lockErr != nil {
			return
		}
		defer unlock()
		// a forker can report failure after the window opened: a child that has since
		// answered owns what is there now
		if cur, curErr := readCoordinatorFile(path); wrote == nil && curErr == nil || wrote != nil && (curErr != nil || !bytes.Equal(cur, wrote)) {
			return
		}
		if prevErr == nil {
			_ = writeCoordinatorBytes(path, prev)
			return
		}
		_ = removeCoordinatorFile(path)
	}, nil
}

func marshalCoordinatorRecord(rec coordinatorRecord) ([]byte, error) {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func writeCoordinatorRecord(path string, rec coordinatorRecord) error {
	b, err := marshalCoordinatorRecord(rec)
	if err != nil {
		return err
	}
	return writeCoordinatorBytes(path, b)
}

func writeCoordinatorBytes(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".record-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func removeCoordinatorFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// settleCoordinatorRecord runs when a session takes ch/alias. The launched child binds,
// a returning session keeps its record, anyone else inherits nothing. The caller holds
// the peer lock. A failure here leaves a record CoordinatorOf will not honor.
func settleCoordinatorRecord(ch, alias, sid string) error {
	path := coordinatorRecordPath(ch, alias)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	rec, ok := readCoordinatorRecord(path)
	switch {
	case ok && sid != "" && rec.SessionID == sid:
		return nil
	case ok && sid != "" && rec.SessionID == "" && coordinatorRecordFresh(rec):
		rec.SessionID = sid
		return writeCoordinatorRecord(path, rec)
	}
	return removeCoordinatorFile(path)
}

func coordinatorRecordFresh(rec coordinatorRecord) bool {
	at, err := time.Parse(time.RFC3339, rec.TS)
	return err == nil && coordinatorNow().Sub(at) < coordinatorBindWindow
}

// moveCoordinatorRecord follows a legacy rename. The caller holds both peer locks.
func moveCoordinatorRecord(ch, oldAlias, newAlias string) error {
	oldPath, newPath := coordinatorRecordPath(ch, oldAlias), coordinatorRecordPath(ch, newAlias)
	if err := os.Rename(oldPath, newPath); errors.Is(err, fs.ErrNotExist) {
		return removeCoordinatorFile(newPath)
	} else {
		return err
	}
}

// coordinatesAnySeat reports a record on ch naming addr. Past the cap it answers no.
func coordinatesAnySeat(ch, addr string) bool {
	dir := filepath.Dir(coordinatorRecordPath(ch, "x"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	if len(entries) > coordinatorScanMax {
		entries = entries[:coordinatorScanMax]
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if rec, ok := readCoordinatorRecord(filepath.Join(dir, e.Name())); ok && rec.Coordinator == addr {
			return true
		}
	}
	return false
}

// RecordBootstrapCoordinator records the seat a pasted prompt will name. A name already
// in the store is left alone: bootstrap also previews, and a preview must not unbind a
// live seat. It reports whether it wrote.
func RecordBootstrapCoordinator(f *Formation, alias string) (bool, error) {
	var p *FormationPeer
	for i := range f.Peers {
		if f.Peers[i].Alias == alias {
			p = &f.Peers[i]
			break
		}
	}
	if p == nil {
		return false, fmt.Errorf("formation %q has no peer %q", f.Name, alias)
	}
	self, err := bootstrapReplyTo(f)
	if err != nil {
		return false, err
	}
	if err := checkCoordinatorKey(f.Channel, alias); err != nil {
		return false, err
	}
	unlock, err := lockPeer(f.Channel, alias)
	if err != nil {
		return false, err
	}
	defer unlock()
	if fileExists(filepath.Join(CBUSDir(), f.Channel, alias, "meta.json")) {
		return false, nil
	}
	sid := ""
	if p.Mode == ModeResume {
		sid = p.SessionID
	}
	if _, err := writeCoordinatorLocked(f.Channel, alias, formationSeat(f, p, self).Coord, coordinatorDoorBootstrap, sid); err != nil {
		return false, err
	}
	return true, nil
}
