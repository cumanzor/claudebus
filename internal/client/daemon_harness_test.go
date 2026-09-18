package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDaemonHarnessOmittedAndExplicitCodexRequests(t *testing.T) {
	for _, harness := range []string{"", "codex"} {
		t.Run("harness="+harness, func(t *testing.T) {
			d, _, req := daemonFixture(t)
			req.Harness = harness
			// Exercise the wire format used by both old and new callers.
			var decoded ConnectRequest
			if err := json.Unmarshal(mustBindingJSON(t, req), &decoded); err != nil {
				t.Fatal(err)
			}
			c := mustDaemonConnect(t, d, decoded)
			if c.Harness != "codex" || d.snapshot(c.ID).Harness != "codex" {
				t.Fatal("connection/status did not identify its Codex adapter")
			}
			var saved ConnectionState
			b, err := os.ReadFile(filepath.Join(d.root, "connections", c.ID+".json"))
			if err != nil || json.Unmarshal(b, &saved) != nil || saved.Harness != "codex" {
				t.Fatalf("journal lost explicit harness: %s, %v", b, err)
			}
			m, ok := ReadPeerMeta(filepath.Join(d.peerDir(c), "meta.json"))
			if !ok || m.Harness != "codex" {
				t.Fatal("peer metadata lost adapter identity")
			}
		})
	}
}

func TestDaemonHarnessRejectsUnsupportedRequestBeforeMutation(t *testing.T) {
	for _, harness := range []string{"claude", "opencode", "future-adapter", "Codex", " "} {
		t.Run(harness, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			req.Harness = harness
			if _, err := d.connect(req); err == nil || !strings.Contains(err.Error(), "unsupported daemon harness") {
				t.Fatalf("unsupported adapter was not rejected: %v", err)
			}
			if q.opens != 0 || len(d.statusSnapshots()) != 0 || dirExists(filepath.Join(CBUSDir(), req.Channel)) {
				t.Fatal("unsupported adapter opened a queue or claimed peer state")
			}
			entries, err := os.ReadDir(filepath.Join(d.root, "connections"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("unsupported adapter wrote a journal: %v, %v", entries, err)
			}
		})
	}
}

func TestDaemonHarnessLegacyJournalPreservesPendingAcrossLoadAndReconnect(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	appendDaemonMessage(t, c, "", "uncertain delivery must not be replayed")
	q.enqueueErr = errors.New("response lost")
	if err := d.deliver(c); err == nil || c.Pending == nil {
		t.Fatal("fixture did not retain pending delivery")
	}
	before := cloneConnection(c)
	journal := filepath.Join(d.root, "connections", c.ID+".json")
	var old map[string]json.RawMessage
	if err := json.Unmarshal(mustBindingJSON(t, c), &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "harness")
	legacy := mustBindingJSON(t, old)
	if err := os.WriteFile(journal, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	d = reloadDaemonFixture(t, q)
	c = d.connections[c.ID]
	if c.Harness != "codex" {
		t.Fatal("old journal was not interpreted as Codex")
	}
	loaded, err := os.ReadFile(journal)
	if err != nil || string(loaded) != string(legacy) {
		t.Fatal("loading rewrote the old journal")
	}
	before.Harness = "codex"
	if !reflect.DeepEqual(c, before) || len(q.calls) != 1 {
		t.Fatal("load changed connection identity, progress, or attempted delivery")
	}
	wrong := req
	wrong.Harness = "claude"
	if _, err := d.connect(wrong); err == nil {
		t.Fatal("unsupported reconnect was admitted")
	}
	unchanged, err := os.ReadFile(journal)
	if err != nil || string(unchanged) != string(legacy) || !reflect.DeepEqual(c, before) {
		t.Fatal("unsupported reconnect changed pending state")
	}
	req.Harness = "codex"
	got, err := d.connect(req)
	if err != nil || got.ID != before.ID || got.Dev != before.Dev || got.Ino != before.Ino || got.Offset != before.Offset || got.Accepted != before.Accepted || !reflect.DeepEqual(got.Pending, before.Pending) || len(q.calls) != 1 {
		t.Fatalf("explicit reconnect lost the legacy epoch or replayed pending mail: %+v, %v", got, err)
	}
}

func TestDaemonHarnessRejectsUnsupportedJournalWithoutRewriting(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	c.Harness = "future-adapter"
	b := mustBindingJSON(t, c)
	journal := filepath.Join(d.root, "connections", c.ID+".json")
	if err := os.WriteFile(journal, b, 0600); err != nil {
		t.Fatal(err)
	}
	reloaded := newBusDaemon()
	defer reloaded.cancel()
	if err := reloaded.load(); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported persisted adapter fell back to Codex: %v", err)
	}
	after, err := os.ReadFile(journal)
	if err != nil || string(after) != string(b) || len(reloaded.statusSnapshots()) != 0 || q.opens != 1 {
		t.Fatal("failed load modified the journal or admitted an unsupported connection")
	}
}

func TestDaemonHarnessRemoteAliasCannotMatchAnotherAdapter(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	// Model a later adapter in the registry to pin the cross-harness collision
	// boundary now. Neither request admission nor journal loading enables it.
	other := cloneConnection(c)
	other.Harness = "future-adapter"
	other.Relay = &RelayConfig{Host: "relay", Base: "https://example.invalid", CredentialDir: filepath.Join(req.Config.UserHome, "credentials")}
	d.register(other)
	before := d.snapshot(c.ID)
	req.Relay = cloneRelay(other.Relay)
	if _, err := d.connect(req); err == nil || !strings.Contains(err.Error(), "remote alias is already managed") {
		t.Fatalf("matching UUID/store crossed the harness boundary: %v", err)
	}
	if !reflect.DeepEqual(d.snapshot(c.ID), before) || q.opens != 1 || q.inspects != 1 {
		t.Fatal("alias collision inspected a backend or changed its existing binding")
	}
}

func TestDaemonHarnessGuardsCodexEntryPoints(t *testing.T) {
	d, q, _ := daemonFixture(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		t.Fatal("unsupported harness reached Codex consumer probe")
		return consumerProbe{}, nil
	}
	c := &ConnectionState{Harness: "future-adapter"}
	checks := map[string]func() error{
		"queue":      func() error { _, err := d.queue(c); return err },
		"admission":  func() error { return d.requireCLIConsumer(c) },
		"consumer":   func() error { _, err := d.consumerProbe(c); return err },
		"compaction": func() error { return d.observeCompaction(c) },
		"relay":      func() error { return d.writeRemoteIdentity(c) },
		"delivery":   func() error { return d.deliver(c) },
		"scheduler":  func() error { return d.scheduledOperation(c) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil || !strings.Contains(err.Error(), "unsupported") {
				t.Fatalf("unsupported harness reached Codex implementation: %v", err)
			}
		})
	}
	if q.opens != 0 || !reflect.DeepEqual(c, &ConnectionState{Harness: "future-adapter"}) {
		t.Fatal("rejected adapter changed connection state or opened a queue")
	}
}
