//go:build darwin || linux

package client

import (
	"strings"
	"testing"
	"time"
)

func TestDaemonLoopFollowsUpAClaudeReceiptWithoutTicks(t *testing.T) {
	d, c, q, wire := claudeWireFixture(t)
	id := c.ID
	appendDaemonMessage(t, c, "", "follow me up")
	runLoopWithoutTicks(t, d)
	d.wake(id)
	var frame string
	select {
	case frame = <-wire:
	case <-time.After(3 * time.Second):
		t.Fatal("woken connection never submitted")
	}
	s := d.snapshot(id)
	if s.Pending == nil || !strings.Contains(frame, claudeMessageUUID(s.Pending.ClientID)) {
		t.Fatalf("submitted frame does not match the pending attempt: %+v", s)
	}
	appendClaudeQueueRow(t, q, claudeQueueReceiptRow(s.Pending.ClientID))
	receipt := time.Now()
	schedulerWait(t, "receipt accepted by a follow-up", func() bool { return d.snapshot(id).Accepted == 1 })
	if wait := time.Since(receipt); wait > 1500*time.Millisecond {
		t.Fatalf("receipt waited %v for a follow-up", wait)
	}
}
