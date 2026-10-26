//go:build darwin || linux

package client

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func (f *opencodeDeliveryFixture) setCtl(t *testing.T, ctl opencodeHelperControl) {
	t.Helper()
	b, _ := json.Marshal(ctl)
	if err := os.WriteFile(f.ctl, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitForOpencode(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (d *busDaemon) hinted(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ready := d.ready[id]
	_, retry := d.nextTry[id]
	return ready && !retry && d.probeNow[id]
}

func (d *busDaemon) clearHint(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.ready, id)
	delete(d.probeNow, id)
}

func TestOpencodeWatchTurnsEachEventIntoAWakeAndAProbe(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	f.set(t, "busy", ocS1, "ok")
	f.d.startOpencodeWatch(f.c)
	waitForOpencode(t, "the snapshot hint", func() bool { return f.d.hinted(f.c.ID) })
	f.d.clearHint(f.c.ID)
	f.d.setRetry(f.c.ID, time.Now().Add(time.Hour))
	f.set(t, "idle", ocS1, "ok")
	waitForOpencode(t, "the idle hint to clear the retry", func() bool { return f.d.hinted(f.c.ID) })
	f.d.clearHint(f.c.ID)
	f.set(t, "idle", ocS2, "ok")
	waitForOpencode(t, "the route hint", func() bool { return f.d.hinted(f.c.ID) })
}

func TestOpencodeHintForcesAProbeBeforeItsInterval(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	if err := f.d.observeConsumer(f.c); err != nil || f.c.Consumer.State != "online" {
		t.Fatalf("baseline probe = %+v %v", f.c.Consumer, err)
	}
	f.set(t, "idle", ocS2, "ok")
	if err := f.d.observeConsumer(f.c); err != nil || f.c.Consumer.State != "online" {
		t.Fatalf("a probe ran inside its interval without a hint: %+v", f.c.Consumer)
	}
	f.d.opencodeHint(f.c.ID)
	if err := f.d.observeConsumer(f.c); err != nil || f.c.Consumer.State != "unknown" {
		t.Fatalf("hinted probe = %+v %v", f.c.Consumer, err)
	}
}

func TestOpencodeWatchEndsWithItsTUI(t *testing.T) {
	opencodeTestStore(t)
	h := startOpencodeHelper(t, ocS1)
	d := opencodeDaemon(t)
	state, err := d.connect(opencodeConnectRequest(t, "oc", ocS1))
	if err != nil {
		t.Fatal(err)
	}
	c := d.connections[state.ID]
	d.startOpencodeWatch(c)
	waitForOpencode(t, "the watch to start", func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.opencodeWatches[c.ID] != nil })
	waitForOpencode(t, "the snapshot hint", func() bool { return d.hinted(c.ID) })
	d.clearHint(c.ID)
	h.kill(t)
	waitForOpencode(t, "a hint when the stream ends without a dispose", func() bool { return d.hinted(c.ID) })
	waitForOpencode(t, "the watch to stop after the TUI exited", func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.opencodeWatches[c.ID] == nil })
	if p, err := d.consumerProbe(c); err != nil || p.State != "exited" {
		t.Fatalf("probe after exit = %+v %v", p, err)
	}
}

func TestOpencodeDisposeIsAHintAndTheWatchRetries(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	f.d.startOpencodeWatch(f.c)
	waitForOpencode(t, "the snapshot hint", func() bool { return f.d.hinted(f.c.ID) })
	f.d.clearHint(f.c.ID)
	f.setCtl(t, opencodeHelperControl{State: "idle", Shown: ocS1, Deliver: "ok", Dispose: true})
	waitForOpencode(t, "the dispose hint", func() bool { return f.d.hinted(f.c.ID) })
}

func TestOpencodeProbeReportsADeletedSessionAsGone(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	f.setCtl(t, opencodeHelperControl{State: "idle", Shown: ocS1, Deliver: "ok", Deleted: true})
	p, err := observeOpencodeConsumer(f.c)
	if err != nil || p.State != "exited" || !strings.Contains(p.Detail, "was deleted") {
		t.Fatalf("probe = %+v %v", p, err)
	}
}

func TestOpencodeMailParksWhileTheTUIIsGone(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	f.c.Consumer = &consumerObservation{State: "exited"}
	if !parkedClaude(f.c) {
		t.Fatal("mail for an exited OpenCode TUI is not parked")
	}
	f.c.Pending = &queueAttempt{MessageID: newOpencodeMessageID(time.Now())}
	if parkedClaude(f.c) {
		t.Fatal("a pending attempt was parked instead of reconciled")
	}
}

func TestOpencodeWatchStopsWithTheDaemon(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	f.d.startOpencodeWatch(f.c)
	waitForOpencode(t, "the watch to start", func() bool { f.d.mu.Lock(); defer f.d.mu.Unlock(); return f.d.opencodeWatches[f.c.ID] != nil })
	done := make(chan struct{})
	go func() { f.d.cancel(); f.d.relayWorkers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watch goroutine outlived the daemon context")
	}
}

func TestScheduledOperationStartsTheOpencodeWatch(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	_ = f.d.scheduledOperation(f.c)
	waitForOpencode(t, "a watch for the connection", func() bool { f.d.mu.Lock(); defer f.d.mu.Unlock(); return f.d.opencodeWatches[f.c.ID] != nil })
}

// An exited TUI must not wake the scheduler in a loop: a watch that cannot
// reach its endpoint is no news, and an exited consumer gets no new watch.
func TestOpencodeWatchDoesNotSelfWakeOnAnExitedTUI(t *testing.T) {
	opencodeTestStore(t)
	h := startOpencodeHelper(t, ocS1)
	d := opencodeDaemon(t)
	state, err := d.connect(opencodeConnectRequest(t, "oc", ocS1))
	if err != nil {
		t.Fatal(err)
	}
	c := d.connections[state.ID]
	probes := 0
	d.probeConsumer = func(_ context.Context, c *ConnectionState) (consumerProbe, error) {
		probes++
		return observeOpencodeConsumer(c)
	}
	h.kill(t)
	d.opencodeHint(c.ID)
	ran := 0
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		d.mu.Lock()
		_, ready := d.ready[c.ID]
		delete(d.ready, c.ID)
		d.mu.Unlock()
		if ready {
			ran++
			_ = d.scheduledOperation(c)
		}
	}
	if c.Consumer == nil || c.Consumer.State != "exited" {
		t.Fatalf("consumer = %+v, want exited", c.Consumer)
	}
	if ran > 2 || probes > 2 {
		t.Fatalf("exited TUI kept waking the scheduler: %d operations, %d probes in 300ms", ran, probes)
	}
}

func TestOpencodeWatchIsNotStartedForAnExitedConsumer(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	f.c.Consumer = &consumerObservation{State: "exited"}
	f.d.startOpencodeWatch(f.c)
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if f.d.opencodeWatches[f.c.ID] != nil {
		t.Fatal("a watch was started for a TUI known to have exited")
	}
}

func TestOpencodeWatchThatNeverConnectsSendsNoHint(t *testing.T) {
	opencodeTestStore(t)
	h := startOpencodeHelper(t, ocS1)
	d := opencodeDaemon(t)
	state, err := d.connect(opencodeConnectRequest(t, "oc", ocS1))
	if err != nil {
		t.Fatal(err)
	}
	c := d.connections[state.ID]
	h.kill(t)
	d.clearHint(c.ID)
	d.startOpencodeWatch(c) // the consumer still reads online: no probe has run since the exit
	waitForOpencode(t, "the watch to give up", func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.opencodeWatches[c.ID] == nil })
	time.Sleep(50 * time.Millisecond)
	if d.hinted(c.ID) {
		t.Fatal("a watch that never reached its endpoint woke the scheduler")
	}
}
