//go:build darwin || linux

package client

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonSchedulerParksExitedClaudeConsumer(t *testing.T) {
	d, c, _, wire := claudeWireFixture(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		return consumerProbe{State: "exited", Detail: "fixture exit"}, nil
	}
	if c.Consumer != nil {
		c.Consumer.ObservedAt = "2000-01-01T00:00:00Z" // probe due now
		if err := d.save(c); err != nil {
			t.Fatal(err)
		}
	}
	appendDaemonMessage(t, c, "", "for a session that exited")
	schedulerTick(t, d)
	s := d.snapshot(c.ID)
	if s.Consumer == nil || s.Consumer.State != "exited" {
		t.Fatalf("exit was not observed: %+v", s.Consumer)
	}
	journal := filepath.Join(d.root, "connections", c.ID+".json")
	before, err := os.Stat(journal)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		schedulerTick(t, d)
	}
	select {
	case got := <-wire:
		t.Fatalf("mail was submitted to an exited consumer: %q", got)
	default:
	}
	s = d.snapshot(c.ID)
	if s.Pending != nil || s.Offset != 0 || s.State == "error" {
		t.Fatalf("parked connection attempted delivery: %+v", s)
	}
	after, err := os.Stat(journal)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("parked connection rewrote its journal")
	}
	if wait := time.Until(d.retryAt(c.ID)); wait < 30*time.Second {
		t.Fatalf("parked connection retries in %v", wait)
	}
}
