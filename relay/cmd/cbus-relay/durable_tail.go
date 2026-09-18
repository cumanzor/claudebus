package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"claudebus/internal/core"
	"claudebus/relay/internal/wire"
)

const durableTailProtocol = "cbus-relay-durable/v1"

// A stable per-peer gate fences owner replacement against ACK commits without
// holding the whole hub mutex across filesystem work.
func (h *hub) tailGate(key string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tailLocks[key] == nil {
		h.tailLocks[key] = &sync.Mutex{}
	}
	return h.tailLocks[key]
}

func (h *hub) attachTail(key, consumer string, durable bool) (*tail, bool, error) {
	gate := h.tailGate(key)
	gate.Lock()
	defer gate.Unlock()
	return h.attachTailWithGate(key, consumer, durable)
}

// Caller holds the stable peer gate.
func (h *hub) attachTailWithGate(key, consumer string, durable bool) (*tail, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old := h.tails[key]; old != nil {
		if old.durable != durable || (durable && old.consumer != consumer) {
			return nil, false, errors.New("alias has an active different consumer; disconnect it before replacing this subscription")
		}
		close(old.done)
	}
	t := &tail{consumer: consumer, durable: durable, notify: make(chan struct{}, 1), done: make(chan struct{})}
	h.tails[key] = t
	h.seen[key] = time.Now()
	delete(h.departWait, key)
	joined := false
	if durable && h.present[key] {
		// Settle a legacy transport's pending departure before switching to
		// consumer-reported presence. Its grace timer must not survive the handoff.
		delete(h.present, key)
		ch, al, _ := strings.Cut(key, "/")
		h.enqueue(ch, al, "departed")
	}
	if !durable {
		joined = !h.present[key]
		h.present[key] = true
		if joined {
			ch, al, _ := strings.Cut(key, "/")
			h.enqueue(ch, al, "join")
		}
	}
	return t, joined, nil
}

func (s *server) upgradeOwnedTail(w http.ResponseWriter, r *http.Request, key, consumer string, durable bool, proto string) (*wire.Conn, *tail, error) {
	gate := s.hub.tailGate(key)
	gate.Lock()
	defer gate.Unlock()
	s.hub.mu.Lock()
	old := s.hub.tails[key]
	conflict := old != nil && (old.durable != durable || (durable && old.consumer != consumer))
	s.hub.mu.Unlock()
	if conflict {
		err := errors.New("alias has an active different consumer; disconnect it before replacing this subscription")
		http.Error(w, err.Error(), http.StatusConflict)
		return nil, nil, err
	}
	conn, err := wire.Upgrade(w, r, proto)
	if err != nil {
		return nil, nil, err
	}
	if err := s.recordTailOwner(key, consumer, durable); err != nil {
		conn.Close()
		return nil, nil, err
	}
	t, _, err := s.hub.attachTailWithGate(key, consumer, durable)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, t, nil
}

func (h *hub) currentTail(key string, t *tail) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tails[key] == t
}

func (s *server) markDelivered(key string, t *tail, ch, al, name string) error {
	gate := s.hub.tailGate(key)
	gate.Lock()
	defer gate.Unlock()
	if !s.hub.currentTail(key, t) {
		return errors.New("subscription displaced before acknowledgment")
	}
	return s.store.MarkDelivered(ch, al, name)
}

func validTailUpgrade(r *http.Request) bool {
	if r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	upgrade := false
	for _, token := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			upgrade = true
		}
	}
	key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	return upgrade && err == nil && len(key) == 16
}

type durableClientFrame struct {
	Type    string `json:"type"`
	SpoolID string `json:"spoolId,omitempty"`
	EventID string `json:"eventId,omitempty"`
	Event   string `json:"event,omitempty"`
	Text    string `json:"text,omitempty"`
	TS      string `json:"ts,omitempty"`
}

type tailFrame struct {
	op      byte
	payload []byte
}

// This distinct endpoint must never degrade to the old unacknowledged tail.
func (s *server) handleDurableTail(w http.ResponseWriter, r *http.Request) {
	ch, al, consumer := r.URL.Query().Get("channel"), r.URL.Query().Get("alias"), r.URL.Query().Get("consumer")
	if !core.ValidStoreName(ch) || !core.ValidStoreName(al) || !core.ValidStoreName(consumer) || len(consumer) > 128 {
		http.Error(w, "bad channel/alias/consumer", http.StatusBadRequest)
		return
	}
	proto := s.subprotoOK(r)
	if proto == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !validTailUpgrade(r) {
		http.Error(w, "valid WebSocket GET required", http.StatusBadRequest)
		return
	}
	key := ch + "/" + al
	conn, t, err := s.upgradeOwnedTail(w, r, key, consumer, true, proto)
	if err != nil {
		log.Printf("durable tail %s: upgrade: %v", key, err)
		return
	}
	defer s.hub.detach(key, t) // subscriber transport is not recipient CLI presence.
	conn.WriteTimeout = 10 * time.Second
	defer conn.Close()
	ready, _ := json.Marshal(map[string]string{"type": "ready", "protocol": durableTailProtocol})
	if err := conn.WriteFrame(wire.OpText, ready); err != nil {
		return
	}
	stopReader := make(chan struct{})
	defer close(stopReader)
	frames := make(chan tailFrame, 8)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(pongGrace + pingEvery))
			op, payload, err := conn.ReadFrame()
			if err != nil {
				return
			}
			select {
			case frames <- tailFrame{op, payload}:
			case <-stopReader:
				return
			}
			if op == wire.OpClose {
				return
			}
		}
	}()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	outstanding := ""
	for {
		select {
		case <-t.done:
			return
		default:
		}
		if outstanding == "" {
			names, err := s.store.ListNew(ch, al)
			if err != nil {
				log.Printf("durable tail %s: list: %v", key, err)
				return
			}
			if len(names) > 0 {
				if !presenceSpoolMatchesTail(names[0], t) {
					if err := s.markDelivered(key, t, ch, al, names[0]); err != nil {
						return
					}
					continue // superseded consumer epoch, never a model-visible delivery.
				}
				payload, err := s.store.Read(ch, al, names[0])
				if err != nil {
					return
				}
				if !json.Valid(payload) {
					log.Printf("durable tail %s: invalid stored JSON", key)
					return
				}
				frame, err := json.Marshal(struct {
					Type    string          `json:"type"`
					SpoolID string          `json:"spoolId"`
					Message json.RawMessage `json:"message"`
				}{"message", names[0], payload})
				if err != nil {
					return
				}
				if err = conn.WriteFrame(wire.OpText, frame); err != nil {
					return
				}
				outstanding = names[0]
			}
		}
		select {
		case <-t.done:
			return
		case <-readerDone:
			return
		case <-t.notify:
		case <-ping.C:
			if err := conn.WriteFrame(wire.OpPing, nil); err != nil {
				return
			}
		case frame := <-frames:
			switch frame.op {
			case wire.OpClose:
				return
			case wire.OpPing:
				if err := conn.WriteFrame(wire.OpPong, frame.payload); err != nil {
					return
				}
			case wire.OpPong:
				s.hub.touch(key)
			case wire.OpText:
				var message durableClientFrame
				decoder := json.NewDecoder(bytes.NewReader(frame.payload))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&message) != nil {
					return
				}
				if decoder.Decode(new(any)) != io.EOF {
					return
				}
				switch message.Type {
				case "ack":
					if outstanding == "" || message.SpoolID != outstanding || message.EventID != "" || message.Event != "" || message.Text != "" || message.TS != "" {
						return
					}
					if err := s.markDelivered(key, t, ch, al, outstanding); err != nil {
						log.Printf("durable tail %s: ACK commit: %v", key, err)
						return
					}
					outstanding = ""
					s.hub.touch(key)
				case "presence":
					if message.SpoolID != "" {
						return
					}
					if err := s.acceptDurablePresence(key, t, ch, al, message); err != nil {
						log.Printf("durable tail %s: presence: %v", key, err)
						return
					}
					ack, _ := json.Marshal(map[string]string{"type": "presence-ack", "eventId": message.EventID})
					if err := conn.WriteFrame(wire.OpText, ack); err != nil {
						return
					}
				default:
					return
				}
			}
		}
	}
}
