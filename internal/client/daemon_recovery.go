package client

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// An observation describes only the latest accepted message, at ObservedAt.
// Received means the exact client ID appeared in this thread's user history;
// neither it nor a queue observation proves a completed turn or a live process.
type deliveryObservation struct {
	Attempt    queueAttempt `json:"attempt"`
	State      string       `json:"state"`
	ItemID     string       `json:"itemId,omitempty"`
	ObservedAt string       `json:"observedAt"`
}

type AbandonRequest struct {
	Target   string `json:"target"`
	ClientID string `json:"clientId"`
	Reason   string `json:"reason"`
}

// Abandonment releases the local delivery block. It never cancels a submission
// at Codex, proves nonreceipt, or authorizes a duplicate enqueue.
type abandonedAttempt struct {
	Attempt queueAttempt `json:"attempt"`
	Start   int64        `json:"start"`
	Reason  string       `json:"reason"`
	At      string       `json:"at"`
}

// The connection lane serializes state changes; the peer lock fences ownership
// for the whole operation. Neither is acquired while holding the registry lock.
func (d *busDaemon) lockConnection(target string) (*ConnectionState, func(), error) {
	c, unlock, finish, err := d.lockConnectionWithRelease(target)
	if err != nil {
		return nil, nil, err
	}
	return c, func() { unlock(); finish() }, nil
}

func (d *busDaemon) lockConnectionWithRelease(target string) (*ConnectionState, func(), func(), error) {
	ch, al, err := ParseLocal(target)
	host := ""
	if IsRemote(target) {
		ch, host, al, err = ParseRemote(target)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if ch == "" {
		return nil, nil, nil, errors.New("use channel/alias")
	}
	for _, selected := range d.statusSnapshots() {
		if selected.Channel != ch || selected.Alias != al || (selected.Relay == nil && host != "") || (selected.Relay != nil && selected.Relay.Host != host) {
			continue
		}
		c, finish, err := d.beginOperation(selected.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		if c.State == "detached" {
			finish()
			continue
		}
		unlock, err := d.lockPeer(connectionLockChannel(c), al)
		if err != nil {
			finish()
			return nil, nil, nil, err
		}
		owns, err := d.ownership(c)
		if err != nil {
			unlock()
			finish()
			return nil, nil, nil, err
		}
		if owns {
			return c, unlock, finish, nil
		}
		unlock()
		finish()
	}
	return nil, nil, nil, fmt.Errorf("no owned managed connection for %s", target)
}

// Recovery advances a cursor only after verifying the same complete inbox line.
func (d *busDaemon) validatePending(c *ConnectionState) error {
	if c.Pending == nil {
		return errors.New("no pending attempt")
	}
	f, err := os.Open(filepath.Join(d.peerDir(c), "inbox.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	dev, ino, size, ok := fileIdentityOf(f)
	if !ok || dev != c.Dev || ino != c.Ino || size < c.Pending.End {
		return errors.New("inbox changed or truncated; pending attempt retained")
	}
	if _, err = f.Seek(c.Offset, io.SeekStart); err != nil {
		return err
	}
	line, err := readDaemonLine(bufio.NewReaderSize(f, 64<<10))
	if err != nil {
		return err
	}
	if c.Offset+int64(len(line)) != c.Pending.End || fmt.Sprintf("%x", sha256.Sum256(line)) != c.Pending.Hash {
		return errors.New("pending message bytes changed; recovery refused")
	}
	return nil
}

func (d *busDaemon) lookup(c *ConnectionState, q nativeQueue, clientID string) (codexMessageLookup, error) {
	found, err := q.lookupMessage(c.ThreadID, clientID)
	if err == nil && found.State != codexMessageNotFound && found.State != codexMessageQueued && found.State != codexMessageReceived {
		err = errors.New("invalid native delivery observation")
	}
	if err != nil {
		d.closeQueue(c.ID)
	}
	return found, err
}

// Reconciliation performs read-only native lookups on explicit operator demand.
// It does not enqueue, resume a thread, or reactivate a disconnected connection.
func (d *busDaemon) reconcile(target string) (*ConnectionState, error) {
	c, unlock, err := d.lockConnection(target)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if c.Pending != nil {
		if err := d.validatePending(c); err != nil {
			return nil, err
		}
		if c.Pending.QueueID != "" || c.Pending.Evidence != nil {
			// A positive acknowledgement survived in memory after a save failed.
			if err := d.accept(c, c.Pending.Evidence); err != nil {
				return nil, err
			}
			d.setRetry(c.ID, time.Time{})
			return cloneConnection(c), nil
		} else {
			q, err := d.queue(c)
			if err != nil {
				return nil, err
			}
			found, err := d.lookup(c, q, c.Pending.ClientID)
			if err != nil {
				return nil, err
			}
			if found.State == codexMessageNotFound {
				next := *c
				if next.State != "disconnected" {
					next.State = "uncertain"
				}
				next.Error = "enqueue outcome remains unknown; absence does not prove rejection; no retry performed"
				if err := d.save(&next); err != nil {
					return nil, err
				}
				*c = next
				return cloneConnection(c), nil
			}
			if err := d.accept(c, &found); err != nil {
				return nil, err
			}
			d.setRetry(c.ID, time.Time{})
			return cloneConnection(c), nil
		}
	}
	if c.LastAccepted == nil || c.LastAccepted.State == codexMessageReceived {
		return cloneConnection(c), nil // positive history evidence remains true even after pruning.
	}
	q, err := d.queue(c)
	if err != nil {
		return nil, err
	}
	found, err := d.lookup(c, q, c.LastAccepted.Attempt.ClientID)
	if err != nil {
		return nil, err
	}
	next := *c
	observation := *c.LastAccepted
	observation.State = found.State
	if found.State == codexMessageNotFound {
		observation.State = "unknown" // acceptance stays known; receipt does not.
	}
	if found.QueueID != "" {
		observation.Attempt.QueueID = found.QueueID
	}
	observation.ItemID, observation.ObservedAt = found.ItemID, Now()
	next.LastAccepted = &observation
	if err := d.save(&next); err != nil {
		return nil, err
	}
	*c = next
	return cloneConnection(c), nil
}

func (d *busDaemon) abandon(req AbandonRequest) (*ConnectionState, error) {
	req.Reason = strings.TrimSpace(req.Reason)
	if req.ClientID == "" || req.Reason == "" || len(req.Reason) > 512 {
		return nil, errors.New("abandon requires the exact pending client ID and a reason of 1-512 bytes")
	}
	c, unlock, err := d.lockConnection(req.Target)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if c.Pending == nil || c.Pending.ClientID != req.ClientID {
		return nil, errors.New("pending client ID changed or no pending attempt; nothing abandoned")
	}
	if c.Pending.QueueID != "" || c.Pending.Evidence != nil {
		return nil, errors.New("acceptance already observed; use connection reconcile to persist it")
	}
	if err := d.validatePending(c); err != nil {
		return nil, err
	}
	next := *c
	next.Resolutions = append(append([]abandonedAttempt(nil), c.Resolutions...), abandonedAttempt{
		Attempt: *c.Pending, Start: c.Offset, Reason: req.Reason, At: Now(),
	})
	next.Offset = c.Pending.End
	next.Pending = nil
	next.Abandoned++
	next.Error = ""
	if next.State != "disconnected" {
		next.State = connectionReadyState(c)
	}
	if err := d.save(&next); err != nil {
		return nil, err
	}
	*c = next
	d.setRetry(c.ID, time.Time{})
	return cloneConnection(c), nil
}
