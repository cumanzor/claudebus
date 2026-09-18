package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Called under the peer lock. A launcher placeholder has no consumer or session
// yet; a dead real peer is not a reservation and must never be reclaimed here.
func daemonReservation(dir, channel, alias string) (*peerMeta, error) {
	f, err := openReservationFile(dir, "meta.json")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	var m peerMeta
	if err != nil || len(b) > 1<<20 || json.Unmarshal(b, &m) != nil || m.SessionID != "reserved" ||
		m.Channel != channel || m.Alias != alias || m.ConnectionID != "" ||
		m.ListenerStart != "" || m.Harness != "" || m.Profile != "" ||
		!bytes.Equal(bytes.TrimSpace(m.ListenerPid), jsonNull) || !bytes.Equal(bytes.TrimSpace(m.OwnerPid), jsonNull) {
		return nil, errors.New("existing alias is not an unclaimed launch reservation")
	}
	return &m, nil
}

// Preserve any mail submitted to the reserved alias before its session started.
// Creation is exclusive; a non-reservation never opens an existing inbox.
func createDaemonInbox(dir string, reserved bool) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "inbox.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if reserved && errors.Is(err, os.ErrExist) {
		return openReservationFile(dir, "inbox.jsonl")
	}
	return f, err
}

func openReservationFile(dir, name string) (*os.File, error) {
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() {
		return nil, errors.New("reservation must be a real peer directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return nil, errors.New("reservation metadata and inbox must be regular files")
	}
	f, err := root.OpenFile(name, reservationReadFlags(), 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	current, currentErr := root.Lstat(name)
	dirAfter, dirErr := os.Lstat(dir)
	if err != nil || currentErr != nil || dirErr != nil || !opened.Mode().IsRegular() ||
		!current.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, current) || !os.SameFile(dirInfo, dirAfter) {
		f.Close()
		return nil, errors.New("launch reservation changed while being inspected")
	}
	return f, nil
}
