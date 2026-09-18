//go:build !windows

package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const claudeTestSession = "01a0b0c6-ffab-7fd0-9319-1ab0275adc21"

func claudeTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func claudeTestListener(t *testing.T) (*net.UnixListener, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cc-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "inbox.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, path
}

func TestClaudeSocketEnvelopeAndUnconfirmedSubmission(t *testing.T) {
	ln, endpoint := claudeTestListener(t)
	wire := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			wire <- nil
			return
		}
		defer conn.Close()
		data, _ := io.ReadAll(conn)
		wire <- data // Deliberately no ACK; a write must not claim acceptance.
	}()
	validated := false
	target := claudeSocketTarget{Endpoint: endpoint, SessionID: claudeTestSession, Validate: func(context.Context, *net.UnixConn) error {
		validated = true
		return nil
	}}
	result, err := submitClaudeSocket(claudeTestContext(t), target, "secret\nquoted\"", "attempt-42", "payload\nsecond line")
	if err != nil || result.State != claudeSubmitted || !validated {
		t.Fatalf("submission: %+v, %v, validated=%v", result, err, validated)
	}
	lines := strings.Split(strings.TrimSuffix(string(<-wire), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want two JSON lines, got %d", len(lines))
	}
	var auth map[string]string
	var envelope struct {
		Type, UUID string
		SessionID  string `json:"session_id"`
		Message    map[string]string
	}
	if json.Unmarshal([]byte(lines[0]), &auth) != nil || auth["type"] != "auth" || auth["token"] != "secret\nquoted\"" {
		t.Fatal("authentication envelope mismatch")
	}
	if json.Unmarshal([]byte(lines[1]), &envelope) != nil || envelope.Type != "user" || envelope.SessionID != claudeTestSession || envelope.UUID != result.UUID || envelope.Message["role"] != "user" || envelope.Message["content"] != "payload\nsecond line" {
		t.Fatal("message envelope mismatch")
	}
	if result.UUID != claudeMessageUUID("attempt-42") || result.UUID == claudeMessageUUID("attempt-43") || !uuidLike(result.UUID) || result.UUID[14] != '8' {
		t.Fatalf("unstable or noncanonical message UUID: %s", result.UUID)
	}
}

func TestClaudeSocketRejectsBeforeSecretLeaves(t *testing.T) {
	ln, endpoint := claudeTestListener(t)
	wire := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			wire <- nil
			return
		}
		defer conn.Close()
		data, _ := io.ReadAll(conn)
		wire <- data
	}()
	secret := "must-never-appear-in-error"
	target := claudeSocketTarget{Endpoint: endpoint, SessionID: claudeTestSession, Validate: func(context.Context, *net.UnixConn) error { return errors.New(secret) }}
	result, err := submitClaudeSocket(claudeTestContext(t), target, secret, "attempt", "hello")
	if err == nil || strings.Contains(err.Error(), secret) || result.State != claudeNotSubmitted || len(<-wire) != 0 {
		t.Fatalf("validation failed to protect secret: %+v", result)
	}
	target.Validate = nil
	if result, err = submitClaudeSocket(claudeTestContext(t), target, secret, "attempt", "hello"); err == nil || result.State != claudeNotSubmitted {
		t.Fatal("nil validator allowed")
	}
	target.Endpoint, target.Validate = filepath.Join(t.TempDir(), secret), func(context.Context, *net.UnixConn) error { return nil }
	if result, err = submitClaudeSocket(claudeTestContext(t), target, secret, "attempt", "hello"); err == nil || strings.Contains(err.Error(), secret) || result.State != claudeNotSubmitted {
		t.Fatal("dial failure leaked its input or claimed submission")
	}
	if _, err = submitClaudeSocket(context.Background(), target, secret, "attempt", "hello"); err == nil {
		t.Fatal("unbounded context allowed")
	}
}

type claudeWriterFunc func([]byte) (int, error)

func (f claudeWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestClaudeMessageWriteFailuresNeverRetry(t *testing.T) {
	for _, n := range []int{0, 2, 5} {
		calls := 0
		writer := claudeWriterFunc(func(p []byte) (int, error) { calls++; return n, io.ErrClosedPipe })
		result, err := writeClaudeMessage(claudeTestContext(t), writer, []byte("hello"), claudeSubmission{State: claudeNotSubmitted})
		if err == nil || result.State != claudeUncertain || calls != 1 {
			t.Fatalf("n=%d: %+v err=%v calls=%d", n, result, err, calls)
		}
	}
	ctx, cancel := context.WithCancel(claudeTestContext(t))
	writer := claudeWriterFunc(func(p []byte) (int, error) { cancel(); return len(p), nil })
	if result, err := writeClaudeMessage(ctx, writer, []byte("hello"), claudeSubmission{State: claudeNotSubmitted}); !errors.Is(err, context.Canceled) || result.State != claudeUncertain {
		t.Fatal("cancellation after write was not uncertain")
	}
	writer = claudeWriterFunc(func([]byte) (int, error) { t.Fatal("wrote after cancellation"); return 0, nil })
	if result, err := writeClaudeMessage(ctx, writer, nil, claudeSubmission{State: claudeNotSubmitted}); !errors.Is(err, context.Canceled) || result.State != claudeNotSubmitted {
		t.Fatal("cancellation before write was not safe")
	}
}

func TestClaudeSocketWriteDeadline(t *testing.T) {
	ln, endpoint := claudeTestListener(t)
	done := make(chan struct{})
	defer close(done)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		<-done // Accept auth, then stop reading until the sender times out.
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	target := claudeSocketTarget{Endpoint: endpoint, SessionID: claudeTestSession, Validate: func(context.Context, *net.UnixConn) error { return nil }}
	result, err := submitClaudeSocket(ctx, target, "secret", "attempt", strings.Repeat("x", 900<<10))
	if err == nil || result.State != claudeUncertain {
		t.Fatalf("blocked socket: %+v err=%v", result, err)
	}
}

func TestClaudeReceiptRequiresExactPersistedUserRow(t *testing.T) {
	id := claudeMessageUUID("attempt")
	row := `{"type":"user","sessionId":"` + claudeTestSession + `","uuid":"` + id + `"}`
	for _, tc := range []struct {
		name, data        string
		observed, invalid bool
	}{
		{"exact", row + "\n", true, false},
		{"assistant", strings.Replace(row, `"user"`, `"assistant"`, 1) + "\n", false, false},
		{"wrong session", strings.Replace(row, claudeTestSession, claudeMessageUUID("other"), 1) + "\n", false, false},
		{"wrong uuid", strings.Replace(row, id, claudeMessageUUID("other"), 1) + "\n", false, false},
		{"marker only", `{"type":"user","content":"` + id + `"}` + "\n", false, false},
		{"partial", row, false, false},
		{"malformed complete", "{bad}\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "transcript")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString(tc.data); err != nil {
				t.Fatal(err)
			}
			got, err := observeClaudeReceipt(claudeTestContext(t), f, claudeTestSession, id, 0, 1<<20)
			if got.Observed != tc.observed || (err != nil) != tc.invalid {
				t.Fatalf("%+v, %v", got, err)
			}
			if tc.name == "partial" {
				if got.NextOffset != 0 {
					t.Fatal("partial row advanced cursor")
				}
				_, _ = f.WriteString("\n")
				got, err = observeClaudeReceipt(claudeTestContext(t), f, claudeTestSession, id, got.NextOffset, 1<<20)
				if err != nil || !got.Observed {
					t.Fatalf("completed row: %+v, %v", got, err)
				}
			}
		})
	}
}

func TestClaudeReceiptBoundsCancellationAndFileFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "transcript")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	id := claudeMessageUUID("attempt")
	_, _ = f.WriteString("{\"type\":\"assistant\"}\n{\"type\":\"user\"")
	ctx := claudeTestContext(t)
	got, err := observeClaudeReceipt(ctx, f, claudeTestSession, id, 0, 25)
	if err != nil || got.Observed || got.NextOffset != 21 {
		t.Fatalf("bounded scan: %+v, %v", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := observeClaudeReceipt(canceled, f, claudeTestSession, id, 0, 25); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation")
	}
	if _, err := observeClaudeReceipt(ctx, f, claudeTestSession, id, 999, 25); err == nil {
		t.Fatal("ignored truncation")
	}
	if _, err := observeClaudeReceipt(ctx, f, claudeTestSession, id, 0, claudeMaxScan+1); err == nil {
		t.Fatal("ignored byte cap")
	}
	_ = f.Close()
	if _, err := observeClaudeReceipt(ctx, f, claudeTestSession, id, 0, 25); err == nil {
		t.Fatal("ignored file failure")
	}
}
