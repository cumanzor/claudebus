package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This fake keeps RPCs blocked until the test releases them or shutdown closes
// the sidecar. All observations remain safe while scheduler workers are active.
type schedulerQueue struct {
	thread  string
	gate    <-chan struct{}
	entered chan daemonQueueCall
	closed  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   []daemonQueueCall
	reads   atomic.Int32
	active  *atomic.Int32
	peak    *atomic.Int32
}

func (q *schedulerQueue) inspect(string) (codexQueueThread, error) {
	q.reads.Add(1)
	return codexQueueThread{ID: q.thread, Source: "cli", CliVersion: "test"}, nil
}
func (q *schedulerQueue) lookupMessage(string, string) (codexMessageLookup, error) {
	q.reads.Add(1)
	return codexMessageLookup{State: codexMessageNotFound}, nil
}
func (q *schedulerQueue) enqueue(thread, id, text string) (string, error) {
	call := daemonQueueCall{thread, id, text}
	q.mu.Lock()
	q.calls = append(q.calls, call)
	q.mu.Unlock()
	if q.active != nil {
		n := q.active.Add(1)
		defer q.active.Add(-1)
		for p := q.peak.Load(); n > p && !q.peak.CompareAndSwap(p, n); p = q.peak.Load() {
		}
	}
	q.entered <- call
	if q.gate != nil {
		select {
		case <-q.gate:
		case <-q.closed:
			return "", &codexQueueTransportError{Method: "thread/queue/add", Err: errors.New("sidecar closed during enqueue")}
		}
	}
	return "queue-" + id, nil
}
func (q *schedulerQueue) Close() error { q.once.Do(func() { close(q.closed) }); return nil }
func (q *schedulerQueue) observedCalls() []daemonQueueCall {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]daemonQueueCall(nil), q.calls...)
}

func schedulerFixture(t *testing.T, count int) (*busDaemon, []*ConnectionState, []*schedulerQueue) {
	t.Helper()
	root := setupStore(t)
	d := newBusDaemon()
	d.start = selfStart(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		return consumerProbe{State: "unknown"}, nil
	}
	if err := d.load(); err != nil {
		t.Fatal(err)
	}
	queues := make([]*schedulerQueue, count)
	byHome := make(map[string]*schedulerQueue)
	for i := range queues {
		queues[i] = &schedulerQueue{thread: fmt.Sprintf("%08d-1111-4111-8111-111111111111", i+1), entered: make(chan daemonQueueCall, 16), closed: make(chan struct{})}
		byHome[filepath.Join(root, fmt.Sprintf("home-%d", i))] = queues[i]
	}
	d.openQueue = func(cfg CodexQueueConfig) (nativeQueue, error) { return byHome[cfg.Home], nil }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := d.shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	connections := make([]*ConnectionState, count)
	for i, q := range queues {
		home := filepath.Join(root, fmt.Sprintf("home-%d", i))
		c, err := d.connect(ConnectRequest{Channel: "scheduler", Alias: fmt.Sprintf("worker-%d", i), ThreadID: q.thread,
			Config: CodexQueueConfig{Binary: filepath.Join(root, "codex"), Home: home, Cwd: root, UserHome: root, SQLiteHome: home}})
		if err != nil {
			t.Fatal(err)
		}
		connections[i] = c
	}
	// Registration broadcasts presence to earlier peers. Consume those records
	// before tests start blocking user-message RPCs.
	schedulerWait(t, "registration presence drained", func() bool {
		d.schedule()
		for _, c := range d.statusSnapshots() {
			_, _, size, ok := fileIdentity(InboxPath(c.Channel, c.Alias))
			if !ok || c.Offset != size {
				return false
			}
		}
		return true
	})
	schedulerWait(t, "registration workers completed", func() bool { return len(d.slots) == 0 })
	return d, connections, queues
}

func schedulerWait(t *testing.T, why string, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("timed out: " + why)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDaemonSchedulerSlowPeerDoesNotBlockDeliveryOrControls(t *testing.T) {
	d, cs, qs := schedulerFixture(t, 2)
	gate := make(chan struct{})
	qs[0].gate = gate
	for i, c := range cs {
		appendDaemonMessage(t, c, "", fmt.Sprintf("payload-%d", i))
	}
	d.schedule()
	schedulerWait(t, "slow enqueue started", func() bool { return len(qs[0].observedCalls()) == 1 })
	schedulerWait(t, "fast peer accepted independently", func() bool { return d.snapshot(cs[1].ID).Accepted == 1 })
	if err := d.disconnect("scheduler/worker-0"); !errors.Is(err, errDaemonBusy) {
		t.Fatalf("in-flight disconnect must report busy, got %v", err)
	}
	if d.snapshot(cs[0].ID).State == "disconnected" {
		t.Fatal("disconnect was falsely published before the in-flight enqueue ended")
	}
	for _, path := range []string{"/health", "/connections"} {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			r := httptest.NewRecorder()
			d.handler(func() {}).ServeHTTP(r, httptest.NewRequest("GET", path, nil))
			done <- r
		}()
		select {
		case r := <-done:
			if r.Code != 200 {
				t.Fatalf("%s: %d %s", path, r.Code, r.Body.String())
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("%s waited for a blocked peer", path)
		}
	}
	schedulerWait(t, "other peer control lane released", func() bool {
		err := d.disconnect("scheduler/worker-1")
		if err != nil && !errors.Is(err, errDaemonBusy) {
			t.Fatal(err)
		}
		return err == nil
	})
	for range 10 {
		d.schedule()
	}
	if len(qs[0].observedCalls()) != 1 {
		t.Fatal("the same peer acquired multiple concurrent delivery lanes")
	}
	close(gate)
	schedulerWait(t, "slow enqueue committed", func() bool { return d.snapshot(cs[0].ID).Accepted == 1 })
	for i, q := range qs {
		calls := q.observedCalls()
		if len(calls) != 1 || calls[0].thread != cs[i].ThreadID || !strings.Contains(calls[0].text, fmt.Sprintf("payload-%d", i)) {
			t.Fatalf("crossed or duplicated peer delivery: %+v", calls)
		}
	}
}

func TestDaemonSchedulerBoundsWorkersAndDrainsBacklog(t *testing.T) {
	d, cs, qs := schedulerFixture(t, daemonMaxOperations+2)
	gate := make(chan struct{})
	var active, peak atomic.Int32
	for i, q := range qs {
		q.gate, q.active, q.peak = gate, &active, &peak
		appendDaemonMessage(t, cs[i], "", fmt.Sprintf("peer %d", i))
	}
	d.schedule()
	schedulerWait(t, "worker limit reached", func() bool { return active.Load() == daemonMaxOperations })
	for range 20 {
		d.schedule()
	}
	if peak.Load() != daemonMaxOperations {
		t.Fatalf("RPC concurrency escaped the bound: %d", peak.Load())
	}
	close(gate)
	schedulerWait(t, "bounded backlog drained", func() bool {
		d.schedule()
		var accepted uint64
		for _, c := range d.statusSnapshots() {
			accepted += c.Accepted
		}
		return accepted == uint64(len(cs))
	})
	for i, q := range qs {
		calls := q.observedCalls()
		if len(calls) != 1 || calls[0].thread != cs[i].ThreadID {
			t.Fatalf("peer %d: %+v", i, calls)
		}
	}
	if peak.Load() > daemonMaxOperations {
		t.Fatalf("backlog exceeded the worker limit: %d", peak.Load())
	}
}

func TestDaemonSchedulerShutdownPersistsAmbiguousAttempt(t *testing.T) {
	d, cs, qs := schedulerFixture(t, 1)
	qs[0].gate = make(chan struct{})
	appendDaemonMessage(t, cs[0], "", "interrupted transport")
	d.schedule()
	schedulerWait(t, "enqueue reached backend", func() bool { return len(qs[0].observedCalls()) == 1 })
	before := d.snapshot(cs[0].ID)
	if before.Pending == nil {
		t.Fatal("enqueue began without a published durable attempt")
	}
	stopped := make(chan struct{})
	r := httptest.NewRecorder()
	d.handler(func() { close(stopped) }).ServeHTTP(r, httptest.NewRequest("POST", "/stop", nil))
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	select {
	case <-stopped:
	default:
		t.Fatal("stop did not reach cancellation while enqueue was blocked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	state := d.snapshot(cs[0].ID)
	if state.Pending == nil || state.Pending.ClientID != before.Pending.ClientID || state.Offset != 0 || state.Accepted != 0 || state.State != "uncertain" {
		t.Fatalf("shutdown lost the ambiguous attempt: %+v", state)
	}
	if _, _, err := d.beginOperation(cs[0].ID); !errors.Is(err, errDaemonStopping) {
		t.Fatalf("shutdown allowed new work: %v", err)
	}
	// A fresh daemon must retain the fence and only reconcile, never re-enqueue.
	restarted := newBusDaemon()
	defer restarted.cancel()
	restarted.start = d.start
	restarted.openQueue = d.openQueue
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	c := restarted.connections[cs[0].ID]
	if err := restarted.deliver(c); err == nil || c.Pending == nil || c.Pending.ClientID != before.Pending.ClientID {
		t.Fatalf("restart lost uncertainty fence: %+v, %v", c, err)
	}
	if len(qs[0].observedCalls()) != 1 {
		t.Fatal("restart blindly resent an interrupted enqueue")
	}
}

func TestDaemonSchedulerShutdownCancelsSidecarInitialization(t *testing.T) {
	d, cs, _ := schedulerFixture(t, 1)
	d.closeQueue(cs[0].ID)
	entered := make(chan struct{})
	d.openQueue = func(CodexQueueConfig) (nativeQueue, error) {
		close(entered)
		<-d.ctx.Done()
		return nil, d.ctx.Err()
	}
	appendDaemonMessage(t, cs[0], "", "initializing sidecar")
	d.schedule()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("sidecar initialization did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if state := d.snapshot(cs[0].ID); state.Accepted != 0 || state.Pending != nil || state.Offset != 0 {
		t.Fatalf("cancelled initialization advanced delivery: %+v", state)
	}
}

func TestDaemonSchedulerShutdownCancelsPeerLockWait(t *testing.T) {
	d, cs, qs := schedulerFixture(t, 1)
	appendDaemonMessage(t, cs[0], "", "must wait for lifecycle lock")
	unlock, err := lockPeer(cs[0].Channel, cs[0].Alias)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	d.schedule()
	schedulerWait(t, "worker admitted while lifecycle lock held", func() bool { return len(d.slots) == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if len(qs[0].observedCalls()) != 0 || len(d.slots) != 0 || d.snapshot(cs[0].ID).Pending != nil {
		t.Fatal("stop failed to join a peer-lock waiter before any enqueue")
	}
}

func TestDaemonSchedulerSnapshotsOwnNestedState(t *testing.T) {
	d := newBusDaemon()
	defer d.cancel()
	c := &ConnectionState{ID: "snapshot", Pending: &queueAttempt{Evidence: &codexMessageLookup{State: codexMessageQueued}},
		LastAccepted: &deliveryObservation{Attempt: queueAttempt{Evidence: &codexMessageLookup{State: codexMessageQueued}}},
		Resolutions:  []abandonedAttempt{{Attempt: queueAttempt{Evidence: &codexMessageLookup{State: codexMessageQueued}}}}}
	d.register(c)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 100 {
			owned, finish, err := d.beginOperation(c.ID)
			if err != nil {
				panic(err)
			}
			owned.Pending.Evidence.QueueID = fmt.Sprint(i)
			owned.LastAccepted.Attempt.Evidence.QueueID = fmt.Sprint(i)
			owned.Resolutions[0].Attempt.Evidence.QueueID = fmt.Sprint(i)
			finish()
		}
	}()
	for {
		s := d.statusSnapshots()[0]
		s.Pending.Evidence.QueueID = "outside mutation"
		s.LastAccepted.Attempt.Evidence.QueueID = "outside mutation"
		s.Resolutions[0].Attempt.Evidence.QueueID = "outside mutation"
		// Encoding is what the control handler does concurrently with delivery.
		if _, err := json.Marshal(d.statusSnapshots()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
			if c.Pending.Evidence.QueueID != "99" || c.LastAccepted.Attempt.Evidence.QueueID != "99" || c.Resolutions[0].Attempt.Evidence.QueueID != "99" {
				t.Fatalf("published snapshot shared nested state: %+v", c)
			}
			return
		default:
		}
	}
}

func TestDaemonSchedulerIdleNeverQueriesHarness(t *testing.T) {
	d, _, qs := schedulerFixture(t, 2)
	for range 10 {
		d.schedule()
		schedulerWait(t, "idle workers completed", func() bool { return len(d.slots) == 0 })
	}
	for _, q := range qs {
		if len(q.observedCalls()) != 0 || q.reads.Load() != 1 {
			t.Fatalf("idle delivery queried the harness: calls=%d reads=%d", len(q.observedCalls()), q.reads.Load())
		}
	}
}
