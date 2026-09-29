//go:build darwin || linux

package client

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// the caller holds the connection lane; keep the old receipt source until a pending attempt (uncertain or awaiting-receipt) is reconciled or abandoned.
func (d *busDaemon) reconnectClaude(c *ConnectionState, req ConnectRequest, cfg *ClaudeConnectionConfig, token string) (*ConnectionState, error) {
	if c.Claude == nil || cfg == nil {
		return nil, errors.New("existing Claude binding is unavailable; refusing implicit adoption")
	}
	old, incoming := c.Claude.Binding, cfg.Binding
	if old.SessionID != incoming.SessionID || old.ConfigHome != incoming.ConfigHome ||
		old.TranscriptPath != incoming.TranscriptPath || !devMatches(old.TranscriptDev, incoming.TranscriptDev) || old.TranscriptIno != incoming.TranscriptIno || incoming.TranscriptSize < old.TranscriptSize {
		return nil, fmt.Errorf("Claude transcript epoch changed; refusing to redirect the existing connection or receipt history. Connect this session under a fresh alias, or save any unread mail past byte %d of the %s inbox, then run cbus unregister %s and connect again", c.Offset, ConnectionTarget(c), ConnectionTarget(c))
	}
	if c.Relay != nil && (req.Relay == nil || c.Relay.Base != req.Relay.Base) {
		return nil, errors.New("relay endpoint changed; refusing to redirect an existing connection")
	}
	unlock, err := d.lockPeer(connectionLockChannel(c), c.Alias)
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, ok := ReadPeerMeta(filepath.Join(d.peerDir(c), "meta.json"))
	if !ok || m.ConnectionID != c.ID || m.SessionID != c.ThreadID || m.Harness != daemonHarnessClaude {
		return nil, errors.New("Claude alias ownership changed; refusing to update its binding")
	}
	inbox, err := openSharedRead(filepath.Join(d.peerDir(c), "inbox.jsonl"))
	if err != nil {
		return nil, inboxEpochRefusal(c, "refusing reconnect")
	}
	end := c.Offset
	if c.Pending != nil {
		end = c.Pending.End
	}
	err = d.adoptInboxEpoch(c, inbox, end, "refusing reconnect")
	inbox.Close()
	if err != nil {
		return nil, err
	}
	previousToken, readErr := readClaudeCredential(d.root, c.Claude.CredentialRef)
	sameCapability := readErr == nil && previousToken == token
	sameRuntime := old.Endpoint == incoming.Endpoint
	if c.Pending != nil && (!sameRuntime || !sameCapability) {
		return nil, errors.New("Claude has an unresolved pending attempt in its original runtime/capability epoch; reconcile a positive receipt or explicitly abandon that exact attempt before reconnecting a changed runtime")
	}
	if sameRuntime && sameCapability {
		cfg.CredentialRef = c.Claude.CredentialRef
	}
	// A resumed capture observes a later EOF. Never replace the durable receipt
	// cursor with that new capture point or an old pending receipt could be lost.
	cfg.ReceiptOffset = c.Claude.ReceiptOffset
	next := cloneConnection(c)
	next.Claude, next.Relay = cfg, cloneRelay(req.Relay)
	refresh := *cfg != *c.Claude
	if refresh {
		d.closeQueue(c.ID)
	}
	if _, err := d.queue(next); err != nil {
		return nil, err
	}
	if err := d.requireCLIConsumer(next); err != nil {
		d.closeQueue(c.ID)
		return nil, err
	}
	if cfg.CredentialRef != c.Claude.CredentialRef {
		if err := d.persistClaudeCapability(cfg, token); err != nil {
			d.closeQueue(c.ID)
			return nil, err
		}
	}
	next.State, next.Error = connectionReadyState(next), ""
	if next.Pending != nil {
		next.State = pendingConnectionState(next, time.Now())
	}
	if err := d.save(next); err != nil {
		if refresh {
			d.closeQueue(c.ID)
		}
		return nil, err
	}
	*c = *next
	armErr := d.armLocked(c)
	unlock() // Presence fanout acquires its own ordered peer locks.
	deferred := d.deferConnect(c, armErr, d.connectPresence(c, false), d.flushPresence(c), d.connectRelay(c))
	d.setRetry(c.ID, time.Time{})
	return connectResult(c, deferred), nil
}
