package client

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// runLoopWithoutTicks runs the real scheduler loop with a tick that never
// fires, so anything delivered was woken, followed up or requeued.
func runLoopWithoutTicks(t *testing.T, d *busDaemon) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.scheduleLoop(ctx, nil)
	}()
	t.Cleanup(func() { cancel(); <-done })
}

func TestDaemonLoopDrainsABacklogWithoutTicks(t *testing.T) {
	d, cs, _ := schedulerFixture(t, 1)
	for i := range 5 {
		appendDaemonMessage(t, cs[0], "", fmt.Sprintf("burst %d", i))
	}
	runLoopWithoutTicks(t, d)
	d.wake(cs[0].ID)
	schedulerWait(t, "burst drained by requeue", func() bool { return d.snapshot(cs[0].ID).Accepted == 5 })
}

func TestDaemonLoopKeepsAWakeThatFindsTheLaneBusy(t *testing.T) {
	d, cs, _ := schedulerFixture(t, 1)
	_, release, err := d.beginControl(cs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	runLoopWithoutTicks(t, d)
	appendDaemonMessage(t, cs[0], "", "arrives while a control holds the lane")
	d.wake(cs[0].ID)
	time.Sleep(100 * time.Millisecond) // the loop tries the held lane and moves on
	if d.snapshot(cs[0].ID).Accepted != 0 {
		t.Fatal("delivered while a control held the lane")
	}
	release()
	schedulerWait(t, "wake survived the busy lane", func() bool { return d.snapshot(cs[0].ID).Accepted == 1 })
}

func TestDaemonPresenceFanoutWakesTheRecipient(t *testing.T) {
	d, cs, _ := schedulerFixture(t, 1)
	root := cs[0].Config.Cwd
	home := filepath.Join(root, "late-home")
	late := &schedulerQueue{thread: "00000001-3333-4333-8333-333333333333", entered: make(chan daemonQueueCall, 16), closed: make(chan struct{})}
	known := d.openQueue
	d.openQueue = func(cfg CodexQueueConfig) (nativeQueue, error) {
		if cfg.Home == home {
			return late, nil
		}
		return known(cfg)
	}
	probe := d.probeConsumer
	d.probeConsumer = func(ctx context.Context, c *ConnectionState) (consumerProbe, error) {
		if c.ThreadID == late.thread {
			return onlinePresence(c), nil // its join fans out to the existing peer
		}
		return probe(ctx, c)
	}
	before := d.snapshot(cs[0].ID).Accepted
	runLoopWithoutTicks(t, d)
	if _, err := d.connect(ConnectRequest{Channel: "scheduler", Alias: "late", ThreadID: late.thread,
		Config: CodexQueueConfig{Binary: filepath.Join(root, "codex"), Home: home, Cwd: root, UserHome: root, SQLiteHome: home}}); err != nil {
		t.Fatal(err)
	}
	schedulerWait(t, "join presence delivered to the existing peer", func() bool { return d.snapshot(cs[0].ID).Accepted > before })
}
