//go:build darwin || linux

package client

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func claudeWireFixture(t *testing.T) (*busDaemon, *ConnectionState, *claudeQueue, chan string) {
	t.Helper()
	d, c, q, listener := daemonClaudeFixture(t)
	wire := make(chan string, 4)
	go func() {
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(conn)
			conn.Close()
			wire <- string(b)
		}
	}()
	t.Cleanup(func() { listener.Close() })
	return d, c, q, wire
}

func scheduleUntil(t *testing.T, d *busDaemon, id, why string, predicate func(*ConnectionState) bool) *ConnectionState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.schedule()
		time.Sleep(10 * time.Millisecond)
		if s := d.snapshot(id); s != nil && predicate(s) {
			return s
		}
	}
	t.Fatalf("timed out waiting for %s: %+v", why, d.snapshot(id))
	return nil
}

func TestDaemonClaudeCleanWriteAwaitsReceiptOnNextTick(t *testing.T) {
	d, c, q, wire := claudeWireFixture(t)
	firstEnd := appendDaemonMessage(t, c, "", "first")
	secondEnd := firstEnd + appendDaemonMessage(t, c, "", "second")
	s := scheduleUntil(t, d, c.ID, "clean write", func(s *ConnectionState) bool { return s.Pending != nil && s.State != "submitting" })
	if s.State != claudeAwaitingReceiptState || s.Error != "" || s.Pending.SubmittedAt.IsZero() || s.Offset != 0 {
		t.Fatalf("clean socket write was reported as a failure: %+v", s)
	}
	if !d.retryAt(c.ID).IsZero() {
		t.Fatal("clean socket write started the error backoff")
	}
	if d.cachedQueue(c.ID) == nil {
		t.Fatal("clean socket write closed the sidecar")
	}
	first := s.Pending.ClientID
	if got := <-wire; !strings.Contains(got, claudeMessageUUID(first)) {
		t.Fatal("first frame has wrong identity")
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow(first))
	s = scheduleUntil(t, d, c.ID, "first receipt", func(s *ConnectionState) bool { return s.Accepted == 1 })
	if s.Offset != firstEnd || s.LastAccepted.Attempt.ClientID != first {
		t.Fatalf("receipt was not accepted for the first line: %+v", s)
	}
	// The following line must not wait out a backoff left by the first.
	s = scheduleUntil(t, d, c.ID, "second submission", func(s *ConnectionState) bool {
		return s.Pending != nil && s.Pending.ClientID != first && s.State != "submitting"
	})
	if s.State != claudeAwaitingReceiptState || !d.retryAt(c.ID).IsZero() {
		t.Fatalf("second line was delayed or reported as a failure: %+v", s)
	}
	second := s.Pending.ClientID
	if got := <-wire; !strings.Contains(got, claudeMessageUUID(second)) {
		t.Fatal("second frame has wrong identity")
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow(second))
	s = scheduleUntil(t, d, c.ID, "second receipt", func(s *ConnectionState) bool { return s.Accepted == 2 })
	if s.Offset != secondEnd || s.Pending != nil {
		t.Fatalf("second receipt did not commit: %+v", s)
	}
}

func TestDaemonClaudeReceiptTimeoutBecomesUncertainWithoutResend(t *testing.T) {
	d, c, q, wire := claudeWireFixture(t)
	end := appendDaemonMessage(t, c, "", "slow receipt")
	if err := d.deliver(c); err != nil || c.State != claudeAwaitingReceiptState {
		t.Fatalf("clean write: %v %+v", err, c)
	}
	<-wire
	attempt := c.Pending.ClientID
	c.Pending.SubmittedAt = time.Now().Add(-claudeReceiptTimeout - time.Second)
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	s := scheduleUntil(t, d, c.ID, "timeout", func(s *ConnectionState) bool { return s.State == "uncertain" })
	if s.Pending == nil || s.Pending.ClientID != attempt || s.Offset != 0 || !strings.Contains(s.Error, "deadline") {
		t.Fatalf("timeout lost or rewrote the attempt: %+v", s)
	}
	if at := d.retryAt(c.ID); time.Until(at) < 5*time.Second {
		t.Fatalf("timeout did not fall back to the error backoff: %v", at)
	}
	select {
	case got := <-wire:
		t.Fatalf("timed-out attempt was resent: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
	// A late exact receipt still settles the fenced attempt, and clears the backoff.
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow(attempt))
	d.setRetry(c.ID, time.Now())
	s = scheduleUntil(t, d, c.ID, "late receipt", func(s *ConnectionState) bool { return s.Accepted == 1 })
	if s.Offset != end || s.Pending != nil || s.State != "socket-ready" || !d.retryAt(c.ID).IsZero() {
		t.Fatalf("late receipt was not accepted cleanly: %+v", s)
	}
}

func TestDaemonClaudeAmbiguousAndLegacyAttemptsStayUncertain(t *testing.T) {
	t.Run("socket-uncertain write", func(t *testing.T) {
		d, c, _ := admittedClaude(t)
		appendDaemonMessage(t, c, "", "partial write")
		q := &daemonFakeQueue{enqueueErr: &claudeAwaitingReceiptError{State: claudeUncertain, Err: errors.New("Claude socket message write is unconfirmed")}}
		d.closeQueue(c.ID)
		if err := d.cacheQueue(c.ID, q); err != nil {
			t.Fatal(err)
		}
		var awaiting *claudeAwaitingReceiptError
		if err := d.deliver(c); !errors.As(err, &awaiting) || c.Pending == nil || !c.Pending.SubmittedAt.IsZero() || c.State == claudeAwaitingReceiptState {
			t.Fatalf("ambiguous write was treated as clean: %v %+v", err, c)
		}
	})
	t.Run("pending journaled without a submission time", func(t *testing.T) {
		d, c, _ := admittedClaude(t)
		appendDaemonMessage(t, c, "", "older daemon")
		q := &daemonFakeQueue{enqueueErr: &claudeAwaitingReceiptError{State: claudeSubmitted}}
		d.closeQueue(c.ID)
		if err := d.cacheQueue(c.ID, q); err != nil {
			t.Fatal(err)
		}
		if err := d.deliver(c); err != nil || c.State != claudeAwaitingReceiptState {
			t.Fatalf("clean write: %v", err)
		}
		c.Pending.SubmittedAt = time.Time{}
		if err := d.deliver(c); err == nil || c.Pending == nil || len(q.calls) != 1 {
			t.Fatal("attempt without a submission time was treated as waiting")
		}
	})
}

func awaitingClaude(t *testing.T) (*busDaemon, *ConnectionState, ConnectRequest, *daemonFakeQueue) {
	t.Helper()
	d, c, req := admittedClaude(t)
	appendDaemonMessage(t, c, "", "awaiting receipt")
	q := &daemonFakeQueue{enqueueErr: &claudeAwaitingReceiptError{State: claudeSubmitted}}
	d.closeQueue(c.ID)
	if err := d.cacheQueue(c.ID, q); err != nil {
		t.Fatal(err)
	}
	if err := d.deliver(c); err != nil || c.State != claudeAwaitingReceiptState {
		t.Fatalf("clean write: %v", err)
	}
	return d, c, req, q
}

func TestClaudeReconnectKeepsUnexpiredReceiptWait(t *testing.T) {
	d, c, req, q := awaitingClaude(t)
	submitted := c.Pending.SubmittedAt
	got, err := d.connectWithCredential(req, admissionTestToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != claudeAwaitingReceiptState || !got.Pending.SubmittedAt.Equal(submitted) || len(q.calls) != 1 {
		t.Fatalf("reconnect turned an unexpired wait uncertain or restarted it: %+v", got)
	}
	c.Pending.SubmittedAt = time.Now().Add(-claudeReceiptTimeout - time.Second)
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	got, err = d.connectWithCredential(req, admissionTestToken)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "uncertain" || len(q.calls) != 1 {
		t.Fatalf("reconnect after the deadline: %+v", got)
	}
}

func TestClaudeReconcileKeepsUnexpiredReceiptWait(t *testing.T) {
	d, c, _, q := awaitingClaude(t)
	submitted := c.Pending.SubmittedAt
	got, err := d.reconcile(ConnectionTarget(c))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != claudeAwaitingReceiptState || got.Error != "" || !got.Pending.SubmittedAt.Equal(submitted) || len(q.calls) != 1 {
		t.Fatalf("reconcile turned an unexpired wait uncertain or restarted it: %+v", got)
	}
	c.Pending.SubmittedAt = time.Now().Add(-claudeReceiptTimeout - time.Second)
	got, err = d.reconcile(ConnectionTarget(c))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "uncertain" || got.Error == "" || len(q.calls) != 1 {
		t.Fatalf("reconcile after the deadline: %+v", got)
	}
}
