package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const daemonMaxOperations = 4

var errDaemonBusy = errors.New("connection or daemon workers busy; try again")
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
				dev, ino, size, ok := fileIdentity(filepath.Join(d.peerDir(c), "inbox.jsonl"))
				if !ok || dev != c.Dev || ino != c.Ino || size < c.Offset {
					err = errors.New("inbox changed or truncated; refusing to rearm an unknown epoch")
				} else {
					err = d.armLocked(c)
				}
			}
			unlock()
		}
		if err != nil {
			c.ListenerError = "restore daemon listener: " + err.Error()
			fmt.Fprintf(os.Stderr, "cbus daemon: %s: %s\n", ConnectionTarget(c), c.ListenerError)
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
			a.Evidence = &e
		}
		return a
	}
	next := *c
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
	if d.lanes[c.ID] == nil {
		d.lanes[c.ID] = &sync.Mutex{}
	}
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

// Admission never waits while holding another connection's lock. Controls get a
// truthful busy response instead of claiming a disconnect before enqueue ends.
func (d *busDaemon) beginOperation(id string) (*ConnectionState, func(), error) {
	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		return nil, nil, errDaemonStopping
	}
	var lane *sync.Mutex
	if id != "" {
		lane = d.lanes[id]
		if lane == nil {
			lane = &sync.Mutex{}
			d.lanes[id] = lane
		}
		if !lane.TryLock() {
			d.mu.Unlock()
			return nil, nil, errDaemonBusy
		}
	}
	select {
	case d.slots <- struct{}{}:
	default:
		if lane != nil {
			lane.Unlock()
		}
		d.mu.Unlock()
		return nil, nil, errDaemonBusy
	}
	d.workers.Add(1)
	c := d.connections[id]
	d.mu.Unlock()
	return c, func() {
		if lane != nil {
			d.mu.Lock()
			if current := d.connections[id]; current != nil {
				d.snapshots[id] = cloneConnection(current)
			}
			d.mu.Unlock()
			lane.Unlock()
		}
		<-d.slots
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

// Each tick starts at a different connection. At most daemonMaxOperations jobs
// run, and a peer can occupy at most one slot. Empty inboxes never query Codex.
func (d *busDaemon) schedule() {
	connections := d.statusSnapshots()
	if len(connections) == 0 {
		return
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })
	d.mu.Lock()
	start := d.rotation % len(connections)
	d.rotation = (start + 1) % len(connections)
	d.mu.Unlock()
	for i := range connections {
		selected := connections[(start+i)%len(connections)]
		c, finish, err := d.beginOperation(selected.ID)
		if err != nil {
			continue
		}
		if c.State == "detached" || time.Now().Before(d.retryAt(c.ID)) {
			finish()
			continue
		}
		go func() {
			defer finish()
			if err := d.scheduledOperation(c); err != nil {
				c.Error = err.Error()
				if c.State != "disconnected" && c.State != "detached" && c.State != "binding-required" {
					c.State = "error"
					if c.Pending != nil {
						c.State = "uncertain"
					}
				}
				if saveErr := d.save(c); saveErr != nil {
					fmt.Fprintf(os.Stderr, "cbus daemon: persist %s/%s: %v\n", c.Channel, c.Alias, saveErr)
				}
				d.setRetry(c.ID, time.Now().Add(10*time.Second))
			}
		}()
	}
}

func (d *busDaemon) scheduledOperation(c *ConnectionState) error {
	if err := validateDaemonHarness(c.Harness); err != nil {
		return err
	}
	if c.Relay != nil && (c.State != "disconnected" || len(c.PresenceOutbox) > 0) {
		if err := d.startRelay(c, nil); err != nil {
			return err
		}
	}
	observationErr := d.observeConsumer(c)
	compactionErr := d.observeCompaction(c)
	if c.State == "disconnected" || c.State == "detached" || c.State == "binding-required" {
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
