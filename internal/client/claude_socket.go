//go:build !windows

package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

type claudeSocketTarget struct {
	Endpoint, SessionID string
	// Required: check connected peer PID/UID/start and endpoint file identity.
	// The caller binds these to the exact session before calling this primitive.
	Validate func(context.Context, *net.UnixConn) error
}

type claudeSubmission struct {
	UUID, State string
}

const (
	claudeNotSubmitted = "not-submitted"
	claudeUncertain    = "uncertain"
	claudeSubmitted    = "submitted-unconfirmed"
	claudeMaxLine      = 1 << 20
	claudeMaxScan      = 16 << 20
)

// Version 8 UUIDs reserve application-defined bits. The namespace prevents an
// unrelated hash of the attempt ID from becoming the same message identity.
func claudeMessageUUID(attemptID string) string {
	h := sha256.Sum256([]byte("cbus/claude-message/v1\x00" + attemptID))
	h[6], h[8] = h[6]&15|128, h[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// This inactive transport has no ACK protocol and never retries. A completed
// write is only submission; EOF, silence, and socket closure prove no receipt.
// Keep the token separate from structs that may later be formatted or persisted.
func submitClaudeSocket(ctx context.Context, target claudeSocketTarget, token, attemptID, payload string) (claudeSubmission, error) {
	result := claudeSubmission{UUID: claudeMessageUUID(attemptID), State: claudeNotSubmitted}
	deadline, bounded := ctx.Deadline()
	if !bounded || !filepath.IsAbs(target.Endpoint) || !uuidLike(target.SessionID) || target.Validate == nil || token == "" || attemptID == "" || payload == "" {
		return result, errors.New("Claude socket requires a bounded context and an exact validated target")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(token) > claudeMaxLine || len(payload) > claudeMaxLine {
		return result, errors.New("Claude socket envelope exceeds the byte limit")
	}
	auth, _ := json.Marshal(map[string]string{"type": "auth", "token": token})
	message, _ := json.Marshal(map[string]any{
		"type": "user", "session_id": target.SessionID, "uuid": result.UUID,
		"message": map[string]string{"role": "user", "content": payload},
	})
	if len(auth)+1 > claudeMaxLine || len(message)+1 > claudeMaxLine {
		return result, errors.New("Claude socket envelope exceeds the byte limit")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", target.Endpoint)
	if err != nil {
		return result, errors.New("Claude socket dial failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.SetDeadline(deadline); err != nil {
		return result, errors.New("Claude socket deadline failed")
	}
	if err := target.Validate(ctx, conn.(*net.UnixConn)); err != nil {
		return result, errors.New("Claude socket identity validation failed")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if n, err := conn.Write(append(auth, '\n')); err != nil || n != len(auth)+1 {
		return result, errors.New("Claude socket authentication write failed")
	}
	return writeClaudeMessage(ctx, conn, append(message, '\n'), result)
}

func writeClaudeMessage(ctx context.Context, dst io.Writer, message []byte, result claudeSubmission) (claudeSubmission, error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// Once Write starts, even a zero-byte error is treated conservatively. Only
	// receipt reconciliation can resolve uncertainty; retry could duplicate it.
	result.State = claudeUncertain
	if n, err := dst.Write(message); err != nil || n != len(message) {
		return result, errors.New("Claude socket message write is unconfirmed")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.State = claudeSubmitted
	return result, nil
}

type claudeReceipt struct {
	Observed        bool
	NextOffset      int64
	BudgetExhausted bool // Snapshot has unread bytes: continue/increase budget, not nonreceipt.
}

// The caller supplies an already identity-bound descriptor, never a discovered
// path. Offset must be zero or a previous complete-line cursor. Scan at most
// maxBytes without moving the descriptor cursor.
// A positive receipt means persisted peer input, not model execution or reply.
func observeClaudeReceipt(ctx context.Context, transcript *os.File, sessionID, messageUUID string, offset, maxBytes int64) (claudeReceipt, error) {
	result := claudeReceipt{NextOffset: offset}
	_, bounded := ctx.Deadline()
	if !bounded || transcript == nil || !uuidLike(sessionID) || !uuidLike(messageUUID) || offset < 0 || maxBytes <= 0 || maxBytes > claudeMaxScan {
		return result, errors.New("Claude receipt requires bounded exact transcript input")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	info, err := transcript.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < offset {
		return result, errors.New("Claude receipt transcript is unavailable or truncated")
	}
	scan := bufio.NewScanner(io.NewSectionReader(transcript, offset, min(maxBytes, info.Size()-offset)))
	scan.Buffer(make([]byte, 32<<10), claudeMaxScan)
	// Incomplete trailing JSON is not yet a record, even if it parses. Leave its
	// bytes before the returned cursor so a later scan can observe completion.
	scan.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if end := bytes.IndexByte(data, '\n'); end >= 0 {
			return end + 1, data[:end], nil
		}
		return 0, nil, nil
	})
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var row struct {
			Type, SessionID, UUID string
			IsSidechain           *bool `json:"isSidechain"`
			Attachment            struct {
				Type, CommandMode string
				SourceUUID        string `json:"source_uuid"`
				IsMeta            bool
				Origin            struct{ Kind string }
			}
		}
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			return result, errors.New("Claude receipt transcript has a malformed complete row")
		}
		result.NextOffset += int64(len(scan.Bytes()) + 1)
		// Busy Claude sessions persist peer prompts as queued_command attachments
		// instead of user rows. source_uuid retains the submitted message UUID;
		// the attachment's own UUID identifies a different transcript record.
		// Queue enqueue/remove records alone contain no such identity evidence.
		peerAttachment := row.Type == "attachment" && row.IsSidechain != nil && !*row.IsSidechain &&
			row.Attachment.Type == "queued_command" && row.Attachment.SourceUUID == messageUUID &&
			row.Attachment.CommandMode == "prompt" && row.Attachment.IsMeta && row.Attachment.Origin.Kind == "peer"
		if row.SessionID == sessionID && (row.Type == "user" && row.UUID == messageUUID || peerAttachment) {
			result.Observed = true
			return result, nil
		}
	}
	if scan.Err() != nil {
		return result, errors.New("Claude receipt transcript read failed or exceeded the row limit")
	}
	result.BudgetExhausted = maxBytes < info.Size()-offset
	return result, ctx.Err()
}
