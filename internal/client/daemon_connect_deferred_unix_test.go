//go:build darwin || linux

package client

import (
	"context"
	"testing"
)

func TestClaudeReconnectDefersPostRegistrationFailure(t *testing.T) {
	d, c, req := admittedClaude(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		return consumerProbe{State: "exited"}, nil
	}
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil || c.Consumer.PresenceOnline {
		t.Fatalf("exit not observed: %v", err)
	}
	d.probeConsumer = nil
	brokenPresencePeer(t, req.Channel)
	got, err := d.connectWithCredential(req, admissionTestToken)
	if err != nil {
		t.Fatalf("registered reconnect must succeed: %v", err)
	}
	if got.ID != c.ID || got.Deferred != brokenPeerDeferred {
		t.Fatalf("reconnect deferred=%q", got.Deferred)
	}
	if stored := d.connections[c.ID]; stored.Deferred != "" || stored.Error != "connected; retrying after connect: "+brokenPeerDeferred {
		t.Fatalf("stored: deferred=%q error=%q", stored.Deferred, stored.Error)
	}
}
