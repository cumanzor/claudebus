package client

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// GCResult is what one collection pass did with a record.
type GCResult struct {
	GCRecord
	Collected bool   `json:"collected"`
	Archive   string `json:"archive,omitempty"`
	Left      string `json:"left,omitempty"` // why a collectable record was not collected
}

// GCPass is one collection pass: each record's outcome, plus the session tokens
// no record references any more.
type GCPass struct {
	Records       []GCResult `json:"records"`
	OrphanTokens  int        `json:"orphanTokensRemoved"`
	TokenSweepOff string     `json:"tokenSweepSkipped,omitempty"`
}

type gcRequest struct {
	OlderThanSeconds int64 `json:"olderThanSeconds"`
}

const gcConnectWait = 60 * time.Second

// collectConnections runs one pass. It holds the connect gate for the whole
// pass: a connect already running finishes first and is seen by the recheck
// under each lane, and a resume that arrives meanwhile waits, then connects to
// whatever the pass left, never to a half-collected record.
func (d *busDaemon) collectConnections(olderThan time.Duration, p gcProbe) (GCPass, error) {
	var pass GCPass
	if olderThan <= 0 {
		return pass, errors.New("the inactivity limit must be positive")
	}
	wait := time.NewTimer(gcConnectWait)
	defer wait.Stop()
	select {
	case d.connectGate <- struct{}{}:
		defer func() { <-d.connectGate }()
	case <-wait.C:
		return pass, errDaemonBusy
	case <-d.ctx.Done():
		return pass, errDaemonStopping
	}
	for _, s := range d.statusSnapshots() {
		r := GCResult{GCRecord: classifyGC(s, d.recordPath(s.ID), olderThan, p)}
		r.Unread, r.InboxNote = gcUnread(s, d.root, CBUSDir())
		if r.Class == GCCollect {
			r.Archive, r.Left = d.collectOne(s.ID, olderThan, p)
			r.Collected = r.Left == ""
			if !r.Collected {
				r.Archive = ""
			}
		}
		pass.Records = append(pass.Records, r)
	}
	pass.OrphanTokens, pass.TokenSweepOff = d.sweepOrphanCredentials()
	return pass, nil
}

// sweepOrphanCredentials deletes session tokens no loaded record references: a
// reconnect with a new capability stores a new token and leaves the old one.
// The connect gate is held, so no connect sits between storing a token and
// saving its record. A record that failed to load may reference any token, so
// its presence skips the sweep.
func (d *busDaemon) sweepOrphanCredentials() (int, string) {
	d.mu.Lock()
	if len(d.skipped) > 0 {
		d.mu.Unlock()
		return 0, "a connection record could not be loaded; its token references are unknown"
	}
	refs := map[string]bool{}
	for _, c := range d.connections {
		if c.Claude != nil {
			refs[c.Claude.CredentialRef] = true
		}
	}
	d.mu.Unlock()
	dir := filepath.Join(d.root, claudeCredentialDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ""
		}
		return 0, "read credential directory: " + err.Error()
	}
	removed := 0
	for _, e := range entries {
		if refs[e.Name()] || !validClaudeCredentialRef(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed, ""
}

func (d *busDaemon) recordPath(id string) string {
	return filepath.Join(d.root, "connections", id+".json")
}

// collectOne archives one record and everything it owns. Each step before the
// record file moves leaves the record in place, so a pass that stops partway
// is finished by the next one; moving the record is the commit.
func (d *busDaemon) collectOne(id string, olderThan time.Duration, p gcProbe) (string, string) {
	c, finish, err := d.beginControl(id)
	if err != nil {
		return "", "lane unavailable: " + err.Error()
	}
	defer finish()
	if c == nil {
		return "", "already removed"
	}
	if now := classifyGC(c, d.recordPath(id), olderThan, p); now.Class != GCCollect {
		return "", "now " + now.Class + ": " + now.Reason
	}
	if len(c.PresenceOutbox) > 0 {
		return "", "presence transitions not yet delivered"
	}
	unlock, err := d.lockPeer(connectionLockChannel(c), c.Alias)
	if err != nil {
		return "", "peer lock: " + err.Error()
	}
	defer unlock()

	archive := filepath.Join(d.root, "connections", ".archive", p.now.UTC().Format("2006-01"), id)
	if err := os.MkdirAll(archive, 0o700); err != nil {
		return "", "create archive: " + err.Error()
	}
	d.stopRelay(id)
	d.closeQueue(id)
	peer, owned := d.peerDir(c), gcOwnsPeerDir(c, d.peerDir(c))
	if owned {
		if err := gcExportUnread(c, peer, filepath.Join(archive, "unread.jsonl")); err != nil {
			return "", "export unread mail: " + err.Error()
		}
		if err := os.Rename(peer, filepath.Join(archive, "peer")); err != nil {
			return "", "archive inbox: " + err.Error()
		}
		if c.Relay == nil {
			_ = os.Remove(filepath.Dir(peer)) // the channel folder, only when now empty
		}
	}
	if err := d.removeClaudeCredential(c); err != nil {
		return "", "remove credential: " + err.Error()
	}
	if err := os.Rename(d.recordPath(id), filepath.Join(archive, "record.json")); err != nil {
		return "", "archive record: " + err.Error()
	}
	d.forget(id)
	return archive, ""
}

// gcOwnsPeerDir: after a detach, the same folder can belong to a newer
// connection, whose inbox must stay where it is.
func gcOwnsPeerDir(c *ConnectionState, dir string) bool {
	m, ok := ReadPeerMeta(filepath.Join(dir, "meta.json"))
	if !ok {
		_, err := os.Stat(dir)
		return err == nil && c.Relay != nil
	}
	return m.ConnectionID == c.ID
}

// gcExportUnread copies every line past the delivered offset, when the inbox is
// still the one the record delivered from.
func gcExportUnread(c *ConnectionState, peer, out string) error {
	f, err := openSharedRead(filepath.Join(peer, "inbox.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	dev, ino, size, ok := fileIdentityOf(f)
	if !ok || !devMatches(c.Dev, dev) || ino != c.Ino || size <= c.Offset {
		return nil
	}
	if _, err := f.Seek(c.Offset, io.SeekStart); err != nil {
		return err
	}
	w, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, bufio.NewReader(f)); err != nil {
		w.Close()
		return err
	}
	if err := w.Sync(); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// removeClaudeCredential deletes the stored session token rather than
// archiving it; another record pinned to the same reference keeps it.
func (d *busDaemon) removeClaudeCredential(c *ConnectionState) error {
	if c.Claude == nil || !validClaudeCredentialRef(c.Claude.CredentialRef) {
		return nil
	}
	ref := c.Claude.CredentialRef
	d.mu.Lock()
	for id, other := range d.connections {
		if id != c.ID && other.Claude != nil && other.Claude.CredentialRef == ref {
			d.mu.Unlock()
			return nil
		}
	}
	d.mu.Unlock()
	err := os.Remove(filepath.Join(d.root, claudeCredentialDir, ref))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// forget drops every in-memory trace of a collected record. The caller holds
// its lane; releasing it afterwards finds no record and publishes nothing.
func (d *busDaemon) forget(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.connections, id)
	delete(d.snapshots, id)
	delete(d.lanes, id)
	delete(d.nextTry, id)
	delete(d.ready, id)
	delete(d.due, id)
	delete(d.followups, id)
	delete(d.relayViews, id)
	delete(d.tickErrors, id)
}
