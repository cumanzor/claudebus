package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const daemonMaxOperations = 4

const (
	// longer than one Claude submit (5 s) plus its peer-lock wait (5 s)
	daemonControlWait = 12 * time.Second
	// below the CLI's 90 s daemon request timeout, so a queued connect still answers
	daemonConnectWait = 60 * time.Second
)

var errDaemonBusy = errors.New("connection or daemon workers busy; try again")
var errDaemonSlotsFull = fmt.Errorf("%w", errDaemonBusy)
var errDaemonStopping = errors.New("daemon is stopping")

// Only called before the control socket and scheduler start. A slow peer lock
// must not prevent unrelated managed aliases from accepting local inbox writes.
// Failures remain visible independently of a pending native enqueue outcome.
func (d *busDaemon) rearmLoaded(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	for _, selected := range d.statusSnapshots() {
		if selected.State == "disconnected" || selected.State == "detached" || selected.State == "binding-required" {
			continue
		}
		c, finish, err := d.beginOperation(selected.ID)
		if err != nil {
			continue
		}
		unlock, err := lockPeerForContext(ctx, connectionLockChannel(c), c.Alias, 100*time.Millisecond)
		if err == nil {
			var owned bool
			owned, err = d.ownership(c)
			if err == nil && !owned {
				c.State, c.Error = "detached", "registration removed or replaced; delivery stopped"
				err = d.save(c)
			} else if err == nil {
				var f *os.File
				if f, err = openSharedRead(filepath.Join(d.peerDir(c), "inbox.jsonl")); err != nil {
					err = inboxEpochRefusal(c, "refusing to rearm an unknown epoch")
				} else {
					err = d.adoptInboxEpoch(c, f, c.Offset, "refusing to rearm an unknown epoch")
					f.Close()
					if err == nil {
						err = d.armLocked(c)
					}
				}
			}
			unlock()
		}
		if err != nil {
			c.ListenerError = "restore daemon listener: " + err.Error()
			daemonLogf("%s: %s", ConnectionTarget(c), c.ListenerError)
		} else {
			c.ListenerError = ""
		}
		finish()
	}
}

func (d *busDaemon) lockPeer(ch, alias string) (func(), error) {
	return lockPeerForContext(d.ctx, ch, alias, 5*time.Second)
}

func (d *busDaemon) lockPeers(ch string, aliases ...string) (func(), error) {
	return lockPeersContext(d.ctx, ch, aliases...)
}

// Registry/cache access uses d.mu. A lane owns its mutable ConnectionState for
// one complete delivery or control operation. Published snapshots never alias
// that state, so status does not wait for a sidecar, peer lock, or disk write.
func cloneConnection(c *ConnectionState) *ConnectionState {
	copyAttempt := func(a queueAttempt) queueAttempt {
		if a.Evidence != nil {
			e := *a.Evidence
			if e.ReceiptOffset != nil {
				v := *e.ReceiptOffset
				e.ReceiptOffset = &v
			}
			a.Evidence = &e
		}
		return a
	}
	next := *c
	if c.Claude != nil {
		cfg := *c.Claude
		next.Claude = &cfg
	}
	clonePresenceFields(&next, c)
	if c.Relay != nil {
		v := *c.Relay
		next.Relay = &v
	}
	if c.RelayStatus != nil {
		v := *c.RelayStatus
		next.RelayStatus = &v
	}
	if c.Compaction != nil {
		v := *c.Compaction
		next.Compaction = &v
	}
	if c.Pending != nil {
		a := copyAttempt(*c.Pending)
		next.Pending = &a
	}
	if c.LastAccepted != nil {
		o := *c.LastAccepted
		o.Attempt = copyAttempt(o.Attempt)
		next.LastAccepted = &o
	}
	next.Resolutions = append([]abandonedAttempt(nil), c.Resolutions...)
	for i := range next.Resolutions {
		next.Resolutions[i].Attempt = copyAttempt(next.Resolutions[i].Attempt)
	}
	return &next
}

func (d *busDaemon) publish(c *ConnectionState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.connections[c.ID] != nil {
		d.snapshots[c.ID] = cloneConnection(c)
	}
}

func (d *busDaemon) register(c *ConnectionState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.connections[c.ID] = c
	d.snapshots[c.ID] = cloneConnection(c)
	d.laneLocked(c.ID)
}

func (d *busDaemon) statusSnapshots() []*ConnectionState {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*ConnectionState, 0, len(d.snapshots))
	for _, c := range d.snapshots {
		copy := cloneConnection(c)
		d.decorateRelay(copy)
		out = append(out, copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (d *busDaemon) snapshot(id string) *ConnectionState {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.snapshots[id]; c != nil {
		copy := cloneConnection(c)
		d.decorateRelay(copy)
		return copy
	}
	return nil
}

// laneLocked returns the connection's lane, a 1-slot semaphore. The caller
// holds d.mu.
func (d *busDaemon) laneLocked(id string) chan struct{} {
	lane := d.lanes[id]
	if lane == nil {
		lane = make(chan struct{}, 1)
		d.lanes[id] = lane
	}
	return lane
}

// releaseLane publishes the connection's snapshot before a waiter can take the lane.
func (d *busDaemon) releaseLane(id string, lane chan struct{}) {
	d.mu.Lock()
	if current := d.connections[id]; current != nil {
		d.snapshots[id] = cloneConnection(current)
	}
	d.mu.Unlock()
	<-lane
}

// beginOperation admits scheduled work. It never waits: a busy lane or a full
// pool is skipped until a later pass.
func (d *busDaemon) beginOperation(id string) (*ConnectionState, func(), error) {
	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		return nil, nil, errDaemonStopping
	}
	var lane chan struct{}
	if id != "" {
		lane = d.laneLocked(id)
		select {
		case lane <- struct{}{}:
		default:
			d.mu.Unlock()
			return nil, nil, errDaemonBusy
		}
	}
	select {
	case d.slots <- struct{}{}:
	default:
		if lane != nil {
			<-lane
		}
		d.mu.Unlock()
		return nil, nil, errDaemonSlotsFull
	}
	d.workers.Add(1)
	c := d.connections[id]
	d.mu.Unlock()
	return c, func() {
		if lane != nil {
			d.releaseLane(id, lane)
		}
		<-d.slots
		select {
		case d.slotFreed <- struct{}{}:
		default:
		}
		d.workers.Done()
	}, nil
}

// beginControl admits a control operation (connect, disconnect, reconcile,
// abandon, relay acknowledgement). It takes no delivery slot, so full delivery
// workers never refuse it. It waits up to d.controlWait for the connection's
// lane; a delivery still holding it past that is a truthful busy, never a
// disconnect claimed before the enqueue ends.
func (d *busDaemon) beginControl(id string) (*ConnectionState, func(), error) {
	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		return nil, nil, errDaemonStopping
	}
	lane := d.laneLocked(id)
	d.workers.Add(1)
	d.mu.Unlock()
	wait := time.NewTimer(d.controlWait)
	defer wait.Stop()
	select {
	case lane <- struct{}{}:
	case <-wait.C:
		d.workers.Done()
		return nil, nil, errDaemonBusy
	case <-d.ctx.Done():
		d.workers.Done()
		return nil, nil, errDaemonStopping
	}
	d.mu.Lock()
	c := d.connections[id]
	d.mu.Unlock()
	return c, func() {
		d.releaseLane(id, lane)
		d.workers.Done()
	}, nil
}

func (d *busDaemon) cachedQueue(id string) nativeQueue {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.queues[id]
}

func (d *busDaemon) cacheQueue(id string, q nativeQueue) error {
	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		_ = q.Close()
		return errDaemonStopping
	}
	d.queues[id] = q
	d.mu.Unlock()
	return nil
}

func (d *busDaemon) closeQueue(id string) {
	d.mu.Lock()
	q := d.queues[id]
	delete(d.queues, id)
	d.mu.Unlock()
	if q != nil {
		_ = q.Close()
	}
}

func (d *busDaemon) retryAt(id string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nextTry[id]
}

func (d *busDaemon) setRetry(id string, at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if at.IsZero() {
		delete(d.nextTry, id)
	} else {
		d.nextTry[id] = at
	}
}

// A tick starts a pass over every connection from where the last one stopped.
// At most daemonMaxOperations jobs run, and a peer occupies at most one slot, so
// a pass that runs out of slots stops in place and continueSchedule resumes it
// when one frees: a peer waits for one pass, not one tick per registered
// connection. Empty inboxes never query Codex.
func (d *busDaemon) schedule() {
	d.mu.Lock()
	d.passLeft = len(d.snapshots)
	d.mu.Unlock()
	d.continueSchedule()
}

func (d *busDaemon) continueSchedule() {
	d.mu.Lock()
	ids := make([]string, 0, len(d.snapshots))
	for id := range d.snapshots {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	sort.Strings(ids)
	for {
		d.mu.Lock()
		d.passLeft = min(d.passLeft, len(ids))
		if d.passLeft == 0 {
			d.mu.Unlock()
			return
		}
		id := ids[d.rotation%len(ids)]
		d.mu.Unlock()
		c, finish, err := d.beginOperation(id)
		if errors.Is(err, errDaemonStopping) || errors.Is(err, errDaemonSlotsFull) {
			return
		}
		d.mu.Lock()
		d.rotation = (d.rotation + 1) % len(ids)
		d.passLeft--
		d.mu.Unlock()
		if err != nil {
			continue // lane busy: that peer's operation is already running
		}
		if c.State == "detached" || time.Now().Before(d.retryAt(c.ID)) {
			finish()
			continue
		}
		go func() {
			defer finish()
			d.runScheduled(c)
		}()
	}
}

func (d *busDaemon) runScheduled(c *ConnectionState) {
	if err := d.scheduledOperation(c); err != nil {
		d.scheduledFailure(c, err)
		return
	}
	if parkedClaude(c) {
		d.setRetry(c.ID, time.Now().Add(parkedClaudeRetry))
		return
	}
	d.mu.Lock()
	last, failed := d.tickErrors[c.ID]
	delete(d.tickErrors, c.ID)
	d.mu.Unlock()
	// only errors a tick or a deferred connect recorded; a failure from elsewhere stays
	if strings.HasPrefix(c.Error, connectDeferredPrefix) || failed && c.Error == last {
		c.Error = ""
		if c.State == "error" {
			c.State = connectionReadyState(c)
		}
		if err := d.save(c); err != nil {
			daemonLogf("persist %s: %v", ConnectionTarget(c), err)
		}
	}
}

// scheduledFailure logs a tick error once until a tick succeeds, so a failure
// that repeats every retry writes one line rather than one per tick.
func (d *busDaemon) scheduledFailure(c *ConnectionState, err error) {
	d.mu.Lock()
	logged := d.tickErrors[c.ID] == err.Error()
	if d.tickErrors == nil {
		d.tickErrors = map[string]string{}
	}
	d.tickErrors[c.ID] = err.Error()
	d.mu.Unlock()
	if daemonHarness(c.Harness) == daemonHarnessClaude && c.Pending != nil && c.Error != err.Error() {
		logClaudeAttempt(c, *c.Pending, "error: "+err.Error())
	} else if !logged {
		daemonLogf("%s: %s", ConnectionTarget(c), strings.ReplaceAll(err.Error(), "\n", "; "))
	}
	c.Error = err.Error()
	if c.State != "disconnected" && c.State != "detached" && c.State != "binding-required" {
		c.State = "error"
		if c.Pending != nil {
			c.State = "uncertain"
		}
	}
	if saveErr := d.save(c); saveErr != nil {
		daemonLogf("persist %s/%s: %v", c.Channel, c.Alias, saveErr)
	}
	retry := 10 * time.Second
	var epoch inboxEpochError
	if errors.As(err, &epoch) {
		retry = inboxEpochRetry // only unregister and connect again clears it
	}
	d.setRetry(c.ID, time.Now().Add(retry))
}

const (
	parkedClaudeRetry = time.Minute
	inboxEpochRetry   = 5 * time.Minute
)

// A Claude endpoint is pinned to one process, so mail for an exited consumer
// waits for a reconnect, which clears the retry. A pending attempt still gets
// its receipt lookup: the transcript outlives the process.
func parkedClaude(c *ConnectionState) bool {
	return daemonHarness(c.Harness) == daemonHarnessClaude && c.Pending == nil &&
		c.Consumer != nil && c.Consumer.State == "exited"
}

func (d *busDaemon) scheduledOperation(c *ConnectionState) error {
	if err := validateConnectionAdapter(c); err != nil {
		return err
	}
	if c.Relay != nil && (c.State != "disconnected" || len(c.PresenceOutbox) > 0) {
		if err := d.startRelay(c, nil); err != nil {
			return err
		}
	}
	observationErr := d.observeConsumer(c)
	compactionErr := d.observeCompaction(c)
	if c.State == "disconnected" || c.State == "detached" || c.State == "binding-required" || parkedClaude(c) {
		return errors.Join(observationErr, compactionErr)
	}
	// A broken presence recipient must not starve this connection's incoming
	// mailbox. Delivery independently rechecks ownership while holding its lock.
	return errors.Join(observationErr, compactionErr, d.deliver(c))
}

// Cancellation reaches sidecar initialization as well as active RPCs. Close
// existing sidecars concurrently, then wait for operations to finish journaling
// ambiguous outcomes before the process releases its daemon lock.
func (d *busDaemon) shutdown(ctx context.Context) error {
	d.mu.Lock()
	d.closing = true
	d.cancel()
	for _, sub := range d.relays {
		sub.stop()
	}
	queues := make([]nativeQueue, 0, len(d.queues))
	for _, q := range d.queues {
		queues = append(queues, q)
	}
	d.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		var closeWorkers sync.WaitGroup
		var closeMu sync.Mutex
		var closeErrors []error
		jobs := make(chan nativeQueue)
		for i := 0; i < min(daemonMaxOperations, len(queues)); i++ {
			closeWorkers.Add(1)
			go func() {
				defer closeWorkers.Done()
				for q := range jobs {
					if err := q.Close(); err != nil {
						closeMu.Lock()
						closeErrors = append(closeErrors, err)
						closeMu.Unlock()
					}
				}
			}()
		}
		for _, q := range queues {
			jobs <- q
		}
		close(jobs)
		closeWorkers.Wait()
		d.workers.Wait()
		d.relayWorkers.Wait()
		done <- errors.Join(closeErrors...)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("daemon operations did not stop: %w", ctx.Err())
	}
}
