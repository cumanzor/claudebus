package client

import (
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"
)

// holdEveryDeliverySlot blocks one enqueue per slot and returns the release.
func holdEveryDeliverySlot(t *testing.T, d *busDaemon, cs []*ConnectionState, qs []*schedulerQueue) func() {
	t.Helper()
	gate := make(chan struct{})
	for i := range daemonMaxOperations {
		qs[i].gate = gate
		appendDaemonMessage(t, cs[i], "", fmt.Sprintf("held %d", i))
	}
	d.schedule()
	schedulerWait(t, "every delivery slot held", func() bool { return len(d.slots) == daemonMaxOperations })
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release
}

func TestDaemonConcurrentConnectsSucceedWhileDeliveryIsFull(t *testing.T) {
	d, cs, qs := schedulerFixture(t, daemonMaxOperations)
	root := cs[0].Config.Cwd
	fresh := map[string]*schedulerQueue{}
	reqs := make([]ConnectRequest, 2)
	for i := range reqs {
		home := filepath.Join(root, fmt.Sprintf("late-home-%d", i))
		q := &schedulerQueue{thread: fmt.Sprintf("%08d-2222-4222-8222-222222222222", i+1), entered: make(chan daemonQueueCall, 16), closed: make(chan struct{})}
		fresh[home] = q
		reqs[i] = ConnectRequest{Channel: "scheduler", Alias: fmt.Sprintf("late-%d", i), ThreadID: q.thread,
			Config: CodexQueueConfig{Binary: filepath.Join(root, "codex"), Home: home, Cwd: root, UserHome: root, SQLiteHome: home}}
	}
	known := d.openQueue
	d.openQueue = func(cfg CodexQueueConfig) (nativeQueue, error) {
		if q := fresh[cfg.Home]; q != nil {
			return q, nil
		}
		return known(cfg)
	}
	holdEveryDeliverySlot(t, d, cs, qs)
	errs := make(chan error, len(reqs))
	start := make(chan struct{})
	for _, req := range reqs {
		go func() {
			<-start
			_, err := d.connect(req)
			errs <- err
		}()
	}
	close(start)
	for range reqs {
		if err := <-errs; err != nil {
			t.Fatalf("connect refused while delivery workers were busy: %v", err)
		}
	}
}

func TestDaemonConnectQueuesBehindAConnectInProgress(t *testing.T) {
	d, _, req := daemonFixture(t)
	d.connectGate <- struct{}{} // another connect holds the gate
	done := make(chan error, 1)
	go func() {
		_, err := d.connect(req)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("connect did not wait for the one in progress: %v", err)
	default:
	}
	<-d.connectGate
	if err := <-done; err != nil {
		t.Fatalf("queued connect failed: %v", err)
	}
}

func TestDaemonControlWaitsOutAShortDelivery(t *testing.T) {
	d, cs, qs := schedulerFixture(t, 1)
	gate := make(chan struct{})
	qs[0].gate = gate
	appendDaemonMessage(t, cs[0], "", "short delivery")
	d.schedule()
	schedulerWait(t, "enqueue holds the lane", func() bool { return len(qs[0].observedCalls()) == 1 })
	done := make(chan error, 1)
	go func() { done <- d.disconnect("scheduler/worker-0") }()
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("disconnect answered before the in-flight enqueue ended: %v", err)
	default:
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("disconnect failed although the delivery ended within the control wait: %v", err)
	}
	if s := d.snapshot(cs[0].ID); s.State != "disconnected" || s.Accepted != 1 {
		t.Fatalf("disconnect did not follow the committed enqueue: %+v", s)
	}
}

func TestDaemonLoadSkipsABadRecord(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	bad := filepath.Join(d.root, "connections", "0123456789.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	reloaded := reloadDaemonFixture(t, q)
	if reloaded.snapshot(c.ID) == nil {
		t.Fatal("a bad record kept a good connection from loading")
	}
	if len(reloaded.skipped) != 1 || reloaded.skipped[0] != "0123456789.json" {
		t.Fatalf("skipped records: %v", reloaded.skipped)
	}
	r := httptest.NewRecorder()
	reloaded.handler(func() {}).ServeHTTP(r, httptest.NewRequest("GET", "/health", nil))
	if !regexp.MustCompile(`"skippedRecords":\["0123456789.json"\]`).MatchString(r.Body.String()) {
		t.Fatalf("health does not report the skipped record: %s", r.Body.String())
	}
	if b, _ := os.ReadFile(bad); string(b) != "{not json" {
		t.Fatal("load rewrote the skipped record")
	}
}

func TestDaemonLogLinesAreTimestamped(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	daemonLogf("%s: %s", "dev/worker", "event")
	os.Stderr = stderr
	w.Close()
	b, _ := io.ReadAll(r)
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z cbus daemon: dev/worker: event\n$`).Match(b) {
		t.Fatalf("log line: %q", b)
	}
}
