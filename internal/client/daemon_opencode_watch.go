package client

import (
	"context"
	"errors"
	"time"
)

const opencodeWatchRetry = 2 * time.Second

// A watch is a hint source, never the record: every event makes the scheduler
// probe the TUI and retry delivery now, and the probe decides presence.
type opencodeWatchSub struct {
	cancel   context.CancelFunc
	endpoint opencodeEndpoint
}

func (d *busDaemon) startOpencodeWatch(c *ConnectionState) {
	if c.Opencode == nil || c.State == "disconnected" || c.State == "detached" || c.State == "binding-required" {
		d.stopOpencodeWatch(c.ID)
		return
	}
	e := c.Opencode.Binding.Endpoint
	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		return
	}
	if d.opencodeWatches == nil {
		d.opencodeWatches = map[string]*opencodeWatchSub{}
	}
	if w := d.opencodeWatches[c.ID]; w != nil {
		if w.endpoint == e {
			d.mu.Unlock()
			return
		}
		w.cancel()
	}
	ctx, cancel := context.WithCancel(d.ctx)
	w := &opencodeWatchSub{cancel: cancel, endpoint: e}
	d.opencodeWatches[c.ID] = w
	d.relayWorkers.Add(1)
	d.mu.Unlock()
	go d.runOpencodeWatch(ctx, c.ID, w)
}

func (d *busDaemon) stopOpencodeWatch(id string) {
	d.mu.Lock()
	w := d.opencodeWatches[id]
	delete(d.opencodeWatches, id)
	d.mu.Unlock()
	if w != nil {
		w.cancel()
	}
}

func (d *busDaemon) runOpencodeWatch(ctx context.Context, id string, w *opencodeWatchSub) {
	defer d.relayWorkers.Done()
	defer func() {
		d.mu.Lock()
		if d.opencodeWatches[id] == w {
			delete(d.opencodeWatches, id)
		}
		d.mu.Unlock()
	}()
	for ctx.Err() == nil {
		err := opencodeWatch(ctx, w.endpoint, func(opencodeWatchEvent) { d.opencodeHint(id) })
		if ctx.Err() != nil {
			return
		}
		// the stream ending is itself news: the TUI disposed, exited or restarted
		d.opencodeHint(id)
		if errors.Is(err, errOpencodeEndpointGone) {
			return // the probe reports the exit; a reconnect starts a new watch
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(opencodeWatchRetry):
		}
	}
}

func (d *busDaemon) opencodeHint(id string) {
	d.mu.Lock()
	if d.connections[id] == nil {
		d.mu.Unlock()
		return
	}
	if d.probeNow == nil {
		d.probeNow = map[string]bool{}
	}
	d.probeNow[id] = true
	delete(d.nextTry, id)
	d.mu.Unlock()
	d.wake(id)
}

func (d *busDaemon) takeProbeNow(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	due := d.probeNow[id]
	delete(d.probeNow, id)
	return due
}
