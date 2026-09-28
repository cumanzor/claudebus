package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func scanPeer(t *testing.T, ch, alias string) PeerView {
	t.Helper()
	for _, c := range ScanStore().Channels {
		for _, p := range c.Peers {
			if c.Name == ch && p.Alias == alias {
				return p
			}
		}
	}
	t.Fatalf("%s/%s not listed", ch, alias)
	return PeerView{}
}

func nativeCodexPeer(t *testing.T, probe consumerProbe, probeErr error) (*busDaemon, *ConnectionState) {
	t.Helper()
	d, _, req := daemonFixture(t)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) { return probe, probeErr }
	return d, mustDaemonConnect(t, d, req)
}

func TestListShowsOnlineCodexConsumerNotDaemon(t *testing.T) {
	_, c := nativeCodexPeer(t, consumerProbe{State: "online", PID: 4321, StartToken: "codex-cli"}, nil)
	p := scanPeer(t, c.Channel, c.Alias)
	if !p.Native || !p.Listening || p.ListenerPid != os.Getpid() {
		t.Fatalf("native row lost its daemon listener identity: %+v", p)
	}
	if p.ConsumerState != "online" || p.ConsumerPid != 4321 {
		t.Fatalf("online consumer pid not shown: %+v", p)
	}
}

func TestListHidesPidOfExitedConsumer(t *testing.T) {
	d, c := nativeCodexPeer(t, consumerProbe{State: "online", PID: 4321, StartToken: "codex-cli"}, nil)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		return consumerProbe{State: "exited", Detail: "gone"}, nil
	}
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if c.Consumer.State != "exited" || c.Consumer.PID != 4321 {
		t.Fatalf("precondition: the journal keeps the last pid after an exit: %+v", c.Consumer)
	}
	if p := scanPeer(t, c.Channel, c.Alias); p.ConsumerState != "exited" || p.ConsumerPid != 0 {
		t.Fatalf("exited consumer showed a stale pid: %+v", p)
	}
}

func TestListHidesPidWhenConsumerProbeFails(t *testing.T) {
	d, c := nativeCodexPeer(t, consumerProbe{State: "online", PID: 4321, StartToken: "codex-cli"}, nil)
	d.probeConsumer = func(context.Context, *ConnectionState) (consumerProbe, error) {
		return consumerProbe{}, errors.New("probe unavailable")
	}
	c.Consumer.ObservedAt = ""
	if err := d.observeConsumer(c); err != nil {
		t.Fatal(err)
	}
	if c.Consumer.State != "unknown" || c.Consumer.PID != 4321 {
		t.Fatalf("precondition: a failed probe keeps the last pid: %+v", c.Consumer)
	}
	if p := scanPeer(t, c.Channel, c.Alias); !p.Native || p.ConsumerState != "unknown" || p.ConsumerPid != 0 {
		t.Fatalf("unknown consumer showed a stale pid: %+v", p)
	}
}

func TestListConsumerPidNeedsALiveDaemon(t *testing.T) {
	t.Run("disconnected", func(t *testing.T) {
		d, c := nativeCodexPeer(t, consumerProbe{State: "online", PID: 4321, StartToken: "codex-cli"}, nil)
		if err := d.disconnect(ConnectionTarget(c)); err != nil {
			t.Fatal(err)
		}
		if p := scanPeer(t, c.Channel, c.Alias); p.Listening || p.ConsumerState != "disconnected" || p.ConsumerPid != 0 {
			t.Fatalf("disconnected peer showed a consumer pid: %+v", p)
		}
	})
	t.Run("daemon gone", func(t *testing.T) {
		_, c := nativeCodexPeer(t, consumerProbe{State: "online", PID: 4321, StartToken: "codex-cli"}, nil)
		metaPath := filepath.Join(CBUSDir(), c.Channel, c.Alias, "meta.json")
		m, err := readStartupMeta(metaPath)
		if err != nil {
			t.Fatal(err)
		}
		// Stands in for a daemon that exited: its recorded start token no longer matches.
		m.ListenerStart = "a-daemon-that-exited"
		if err := writeDaemonMeta(filepath.Dir(metaPath), m); err != nil {
			t.Fatal(err)
		}
		if p := scanPeer(t, c.Channel, c.Alias); p.Listening || p.ConsumerState != "unknown" || p.ConsumerPid != 0 {
			t.Fatalf("stale journal pid shown without a live daemon: %+v", p)
		}
	})
}

func TestListSurvivesUnreadableJournal(t *testing.T) {
	_, c := nativeCodexPeer(t, consumerProbe{State: "online", PID: 4321, StartToken: "codex-cli"}, nil)
	journal := filepath.Join(DaemonDir(), "connections", c.ID+".json")
	for name, content := range map[string]string{"unparsable": "{not json", "foreign": `{"id":"` + c.ID + `","channel":"other","alias":"` + c.Alias + `","threadId":"` + c.ThreadID + `","consumer":{"state":"online","pid":4321}}`} {
		if err := os.WriteFile(journal, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if p := scanPeer(t, c.Channel, c.Alias); p.ConsumerState != "unknown" || p.ConsumerPid != 0 {
			t.Fatalf("%s journal was trusted: %+v", name, p)
		}
	}
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	if p := scanPeer(t, c.Channel, c.Alias); p.ConsumerState != "unknown" || p.ConsumerPid != 0 {
		t.Fatalf("missing journal was trusted: %+v", p)
	}
}

func TestListLegacyPeerHasNoConsumer(t *testing.T) {
	_, meta, _ := mustJoinPeer(t, "cx", "peer")
	claimListenerAtJoin("cx", "peer")
	p := scanPeer(t, "cx", "peer")
	if p.Native || p.ConsumerState != "" || p.ConsumerPid != 0 || p.ListenerPid != os.Getpid() {
		t.Fatalf("legacy row changed: %+v (meta %s)", p, meta)
	}
}
