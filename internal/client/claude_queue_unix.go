//go:build darwin || linux

package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

type claudeQueue struct {
	ctx                   context.Context
	cancel                context.CancelFunc
	root                  string
	cfg                   ClaudeConnectionConfig
	mu                    sync.Mutex
	scanAttempt           string
	scanOffset, scanBytes int64
}

var _ nativeQueue = (*claudeQueue)(nil)

// Constructing this inactive adapter neither contacts Claude nor reads its token.
// The endpoint may already be gone: persisted receipts must remain discoverable.
func newClaudeQueueContext(parent context.Context, daemonRoot string, cfg ClaudeConnectionConfig) (nativeQueue, error) {
	if parent == nil || !validClaudeCredentialRef(cfg.CredentialRef) || cfg.ReceiptOffset < 0 {
		return nil, errors.New("invalid Claude queue configuration")
	}
	ctx, cancel := context.WithCancel(parent)
	q := &claudeQueue{ctx: ctx, cancel: cancel, root: daemonRoot, cfg: cfg, scanBytes: claudeMaxScan}
	if _, err := q.inspect(cfg.Binding.SessionID); err != nil {
		cancel()
		return nil, err
	}
	return q, nil
}

func (q *claudeQueue) Close() error { q.cancel(); return nil }

func (q *claudeQueue) inspect(threadID string) (codexQueueThread, error) {
	if err := q.ctx.Err(); err != nil {
		return codexQueueThread{}, err
	}
	if threadID != q.cfg.Binding.SessionID || !uuidLike(threadID) {
		return codexQueueThread{}, errors.New("exact bound Claude session required")
	}
	f, err := openBoundClaudeTranscript(q.cfg.Binding)
	if err != nil {
		return codexQueueThread{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || q.cfg.ReceiptOffset > info.Size() {
		return codexQueueThread{}, errors.New("Claude receipt checkpoint exceeds transcript")
	}
	return codexQueueThread{ID: threadID, Source: "cli", Cwd: q.cfg.Binding.Cwd, Path: q.cfg.Binding.TranscriptPath}, nil
}

func (q *claudeQueue) enqueue(threadID, attemptID, payload string) (string, error) {
	reject := func(err error) (string, error) { return "", &claudeNotSubmittedError{Err: err} }
	if attemptID == "" || payload == "" {
		return reject(errors.New("Claude message requires an attempt ID and content"))
	}
	if _, err := q.inspect(threadID); err != nil {
		return reject(err)
	}
	token, err := readClaudeCredential(q.root, q.cfg.CredentialRef)
	if err != nil {
		return reject(err)
	}
	ctx, cancel := context.WithTimeout(q.ctx, 5*time.Second)
	defer cancel()
	target := claudeSocketTarget{Endpoint: q.cfg.Binding.Endpoint.Socket, SessionID: threadID, Validate: func(ctx context.Context, conn *net.UnixConn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return q.cfg.Binding.Endpoint.validateConnected(conn)
	}}
	result, err := submitClaudeSocket(ctx, target, token, attemptID, payload)
	if result.State == claudeNotSubmitted {
		return reject(err)
	}
	// The native socket has no acceptance ACK. Never invent a queue ID or
	// return nil error: daemon acceptance requires independent transcript proof.
	return "", &claudeAwaitingReceiptError{State: result.State, Err: err}
}

func (q *claudeQueue) lookupMessage(threadID, attemptID string) (codexMessageLookup, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if attemptID == "" {
		return codexMessageLookup{}, errors.New("Claude receipt requires an attempt ID")
	}
	if _, err := q.inspect(threadID); err != nil {
		return codexMessageLookup{}, err
	}
	f, err := openBoundClaudeTranscript(q.cfg.Binding)
	if err != nil {
		return codexMessageLookup{}, err
	}
	defer f.Close()
	if q.scanAttempt != attemptID {
		q.scanAttempt, q.scanOffset = attemptID, q.cfg.ReceiptOffset
	}
	ctx, cancel := context.WithTimeout(q.ctx, 5*time.Second)
	defer cancel()
	uuid := claudeMessageUUID(attemptID)
	observed, err := observeClaudeReceipt(ctx, f, threadID, uuid, q.scanOffset, q.scanBytes)
	if err != nil {
		return codexMessageLookup{}, err
	}
	if observed.Observed {
		return codexMessageLookup{State: codexMessageReceived, ItemID: uuid, ReceiptOffset: &observed.NextOffset}, nil
	}
	if observed.BudgetExhausted && observed.NextOffset == q.scanOffset {
		return codexMessageLookup{}, errors.New("Claude receipt row exceeds the bounded scan budget; receipt remains unknown")
	}
	q.scanOffset = observed.NextOffset
	// This is only a negative observation, never rejection or permission to resend.
	return codexMessageLookup{State: codexMessageNotFound}, nil
}
