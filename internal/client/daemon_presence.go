package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claudebus/internal/core"
)

// Consumer observations describe the CLI, independently of the daemon listener,
// desired subscription, and historical message receipts. PresenceOnline is the
// last announced state, so a transient unknown probe cannot manufacture a rejoin.
type consumerObservation struct {
	State          string `json:"state"`
	PID            int    `json:"pid,omitempty"`
	StartToken     string `json:"startToken,omitempty"`
	ObservedAt     string `json:"observedAt"`
	Detail         string `json:"detail,omitempty"`
	PresenceOnline bool   `json:"presenceOnline"`
}

type consumerProbe struct {
	State, StartToken, Detail string
	PID                       int
}

type presenceRecipient struct {
	Alias, SessionID, ConnectionID string
	Dev, Ino                       uint64
	Done                           bool
}

type presenceTransition struct {
	ID, Event, Text, TS string
	Recipients          []presenceRecipient
}

func clonePresenceFields(next, c *ConnectionState) {
	if c.Consumer != nil {
		v := *c.Consumer
		next.Consumer = &v
	}
	next.PresenceOutbox = append([]presenceTransition(nil), c.PresenceOutbox...)
	for i := range next.PresenceOutbox {
		next.PresenceOutbox[i].Recipients = append([]presenceRecipient(nil), c.PresenceOutbox[i].Recipients...)
	}
}

func (d *busDaemon) consumerProbe(c *ConnectionState) (consumerProbe, error) {
	if d.probeConsumer != nil {
		return d.probeConsumer(d.ctx, c)
	}
	return observeCodexConsumer(d.ctx, c)
}

// connectPresence runs with the connection lane held. It only records an outbox;
// callers must release the source peer file lock before flushing that outbox.
func (d *busDaemon) connectPresence(c *ConnectionState, fresh bool) error {
	next := cloneConnection(c)
	if next.Consumer == nil {
		next.Consumer = &consumerObservation{State: "unknown"}
	}
	p, err := d.consumerProbe(next)
	if err != nil {
		p = consumerProbe{State: "unknown", Detail: err.Error()}
	}
	if err := d.applyConsumerProbe(next, p, fresh || c.Consumer != nil); err != nil {
		return err
	}
	if err := d.save(next); err != nil {
		return err
	}
	*c = *next
	return nil
}

// disconnectPresence prepares the transition on the caller's private next-state
// copy, which must be saved atomically with the explicit disconnect.
func (d *busDaemon) disconnectPresence(c *ConnectionState) error {
	clonePresenceFields(c, c)
	if c.Consumer == nil {
		c.Consumer = &consumerObservation{}
	}
	if c.Consumer.PresenceOnline {
		if err := d.preparePresence(c, "leave", "disconnected; durable inbox and alias retained for resume"); err != nil {
			return err
		}
	}
	c.Consumer.PresenceOnline = false
	c.Consumer.State = "disconnected"
	c.Consumer.ObservedAt = Now()
	c.Consumer.Detail = "explicit disconnect"
	return nil
}

// observeConsumer is called under the operation lane even for disconnected
// registrations, whose previously journaled departure still needs fanout.
func (d *busDaemon) observeConsumer(c *ConnectionState) error {
	if c.State == "detached" {
		return nil
	}
	owns, err := d.ownership(c)
	if err != nil {
		return err
	}
	if !owns {
		// Disconnected connections do not run delivery, so fence their removed
		// registrations here too. Leave/Unregister already emitted departure.
		next := cloneConnection(c)
		next.State = "detached"
		next.Error = "registration removed or replaced; delivery stopped"
		if err := d.save(next); err != nil {
			return err
		}
		*c = *next
		d.closeQueue(c.ID)
		return nil
	}
	if c.State != "disconnected" && c.State != "binding-required" {
		last := time.Time{}
		if c.Consumer != nil {
			last, _ = time.Parse(time.RFC3339, c.Consumer.ObservedAt)
		}
		if elapsed := time.Since(last); elapsed < 0 || elapsed >= 5*time.Second {
			next := cloneConnection(c)
			if next.Consumer == nil {
				next.Consumer = &consumerObservation{State: "unknown"}
			}
			p, err := d.consumerProbe(next)
			if err != nil {
				p = consumerProbe{State: "unknown", Detail: err.Error()}
			}
			// Pre-presence journals establish a baseline without replaying the old join.
			if err := d.applyConsumerProbe(next, p, c.Consumer != nil); err != nil {
				return err
			}
			if consumerChanged(c.Consumer, next.Consumer) || c.PresenceSequence != next.PresenceSequence {
				if err := d.save(next); err != nil {
					return err
				}
			}
			*c = *next
			if c.Relay != nil && c.Consumer.State == "online" {
				if err := d.writeRemoteIdentity(c); err != nil {
					return err
				}
			}
		}
	}
	return d.flushPresence(c)
}

func consumerChanged(a, b *consumerObservation) bool {
	if a == nil || b == nil {
		return a != b
	}
	return a.State != b.State || a.PID != b.PID || a.StartToken != b.StartToken || a.Detail != b.Detail || a.PresenceOnline != b.PresenceOnline
}

func (d *busDaemon) applyConsumerProbe(c *ConnectionState, p consumerProbe, announce bool) error {
	if p.State != "online" && p.State != "exited" {
		p.State = "unknown"
	}
	if p.State == "online" && !c.Consumer.PresenceOnline {
		if announce {
			if err := d.preparePresence(c, "join", "CLI session connected (or resumed)"); err != nil {
				return err
			}
		}
		c.Consumer.PresenceOnline = true
	}
	if p.State == "exited" && c.Consumer.PresenceOnline {
		if err := d.preparePresence(c, "departed", "CLI session exited; durable inbox and alias retained for resume"); err != nil {
			return err
		}
		c.Consumer.PresenceOnline = false
	}
	c.Consumer.State, c.Consumer.Detail, c.Consumer.ObservedAt = p.State, p.Detail, Now()
	if p.PID > 0 {
		c.Consumer.PID, c.Consumer.StartToken = p.PID, p.StartToken
	}
	return nil
}

func (d *busDaemon) preparePresence(c *ConnectionState, event, text string) error {
	if len(c.PresenceOutbox) >= 128 {
		return errors.New("presence outbox is full; resolve pending fanout before recording another transition")
	}
	p := presenceTransition{ID: fmt.Sprintf("cbus-presence-%s-%d", c.ID, c.PresenceSequence+1), Event: event, Text: text, TS: Now()}
	if c.Relay != nil {
		// Remote peers never fan out into a similarly named local channel. The
		// relay worker publishes this exact event ID and removes it only on ACK.
		c.PresenceSequence++
		c.PresenceOutbox = append(c.PresenceOutbox, p)
		return nil
	}
	es, err := os.ReadDir(filepath.Join(CBUSDir(), c.Channel))
	if err != nil {
		return err
	}
	for _, e := range es {
		if !e.IsDir() || e.Name() == c.Alias || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(CBUSDir(), c.Channel, e.Name())
		m, ok := ReadPeerMeta(filepath.Join(dir, "meta.json"))
		if !ok || PeerDead(filepath.Join(dir, "meta.json")) {
			continue
		}
		dev, ino, _, ok := fileIdentity(filepath.Join(dir, "inbox.jsonl"))
		if !ok {
			return fmt.Errorf("snapshot presence recipient %s/%s inbox", c.Channel, e.Name())
		}
		p.Recipients = append(p.Recipients, presenceRecipient{Alias: e.Name(), SessionID: m.SessionID, ConnectionID: m.ConnectionID, Dev: dev, Ino: ino})
	}
	c.PresenceSequence++
	c.PresenceOutbox = append(c.PresenceOutbox, p)
	return nil
}

// flushPresence journals fanout progress after each durable append. A replay
// checks the stable event ID in the exact recipient inbox epoch before appending.
// It never creates a removed recipient. Source+recipient locks fence unregister.
func (d *busDaemon) flushPresence(c *ConnectionState) error {
	if c.Relay != nil {
		return nil // daemon_relay owns durable server ACK handling.
	}
	for work := 0; work < 8 && len(c.PresenceOutbox) > 0; work++ {
		p := c.PresenceOutbox[0]
		index := -1
		for i, r := range p.Recipients {
			if !r.Done {
				index = i
				break
			}
		}
		if index >= 0 {
			r := p.Recipients[index]
			unlock, err := d.lockPeers(c.Channel, c.Alias, r.Alias)
			if err != nil {
				return err
			}
			owns, err := d.ownership(c)
			if err == nil && owns {
				err = appendManagedPresence(c.Channel, c.Alias, p, r)
			}
			unlock()
			if err != nil {
				return err
			}
			if !owns {
				return nil
			}
		}
		next := cloneConnection(c)
		if index < 0 {
			next.PresenceOutbox = next.PresenceOutbox[1:]
		} else {
			next.PresenceOutbox[0].Recipients[index].Done = true
		}
		if err := d.save(next); err != nil {
			return err
		}
		*c = *next
	}
	return nil
}

func appendManagedPresence(channel, from string, p presenceTransition, r presenceRecipient) (err error) {
	dir := filepath.Join(CBUSDir(), channel, r.Alias)
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if os.IsNotExist(err) {
		return nil // recipient registration was explicitly removed
	}
	if err != nil {
		return err
	}
	var m peerMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.SessionID != r.SessionID || m.ConnectionID != r.ConnectionID {
		return nil // never deliver an old event to a replacement registration
	}
	f, err := os.OpenFile(filepath.Join(dir, "inbox.jsonl"), os.O_RDWR|os.O_APPEND, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	dev, ino, _, ok := fileIdentityOf(f)
	if !ok || dev != r.Dev || ino != r.Ino {
		return nil
	}
	scan := bufio.NewReaderSize(f, 64<<10)
	for {
		line, e := readDaemonLine(scan)
		if errors.Is(e, io.EOF) {
			if len(line) != 0 {
				return errors.New("presence fanout inbox has an incomplete trailing record")
			}
			break
		}
		if e != nil {
			return fmt.Errorf("inspect presence fanout inbox: %w", e)
		}
		var seen struct {
			EventID string `json:"eventId"`
		}
		if err := json.Unmarshal(line, &seen); err != nil {
			return fmt.Errorf("presence fanout inbox contains invalid JSON: %w", err)
		}
		if seen.EventID == p.ID {
			return f.Sync() // prior append may have preceded a failed Sync/ack.
		}
	}
	b, err = json.Marshal(core.Message{From: channel + "/" + from, To: channel + "/" + r.Alias, TS: p.TS, Kind: "presence", Event: p.Event, Text: p.Text, EventID: p.ID})
	if err != nil {
		return err
	}
	b = append(b, '\n')
	n, err := f.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return f.Sync()
}

func nativeBusPayload(line []byte, msg core.Message) string {
	frame := string(core.LocalEmit(line))
	if msg.Kind == "presence" {
		return frame + "\nThis is a cbus presence notification. Update your peer awareness; do not reply or send an acknowledgment solely for this event. No follow-up or monitor re-arming is needed."
	}
	return frame
}
