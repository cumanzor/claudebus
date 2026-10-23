//go:build darwin || linux

package client

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type opencodeDeliveryFixture struct {
	d       *busDaemon
	c       *ConnectionState
	ctl     string
	log     string
	session string
}

func newOpencodeDeliveryFixture(t *testing.T) *opencodeDeliveryFixture {
	t.Helper()
	opencodeTestStore(t)
	dir := t.TempDir()
	f := &opencodeDeliveryFixture{ctl: filepath.Join(dir, "ctl.json"), log: filepath.Join(dir, "requests.jsonl"), session: ocS1}
	f.set(t, "idle", ocS1, "ok")
	startOpencodeHelper(t, ocS1, "CBUS_OC_HELPER_CTL="+f.ctl, "CBUS_OC_HELPER_LOG="+f.log)
	f.d = opencodeDaemon(t)
	state, err := f.d.connect(opencodeConnectRequest(t, "oc", ocS1))
	if err != nil {
		t.Fatal(err)
	}
	f.c = f.d.connections[state.ID]
	return f
}

func (f *opencodeDeliveryFixture) set(t *testing.T, state, shown, deliver string) {
	t.Helper()
	b, _ := json.Marshal(opencodeHelperControl{State: state, Shown: shown, Deliver: deliver})
	if err := os.WriteFile(f.ctl, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

type helperRequest struct{ Op, SessionID, MessageID, Text string }

func (f *opencodeDeliveryFixture) requests(t *testing.T, op string) []helperRequest {
	t.Helper()
	file, err := os.Open(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out []helperRequest
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var r helperRequest
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Op == op {
			out = append(out, r)
		}
	}
	return out
}

func TestDaemonOpencodeDeliversWhenIdleAndAcceptsTheStoredMessage(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	end := appendDaemonMessage(t, f.c, "", "hello bus")
	before := time.Now()
	if err := f.d.deliver(f.c); err != nil {
		t.Fatal(err)
	}
	if f.c.Accepted != 1 || f.c.Offset != end || f.c.Pending != nil || f.c.LastAccepted == nil || f.c.LastAccepted.State != codexMessageReceived {
		t.Fatalf("accepted=%d offset=%d pending=%v last=%+v", f.c.Accepted, f.c.Offset, f.c.Pending, f.c.LastAccepted)
	}
	sent := f.requests(t, "deliver")
	id := f.c.LastAccepted.Attempt.MessageID
	if len(sent) != 1 || sent[0].MessageID != id || sent[0].SessionID != ocS1 || !strings.Contains(sent[0].Text, "hello bus") {
		t.Fatalf("plugin saw %+v, accepted %s", sent, id)
	}
	if !validOpencodeMessageID(id) || id < newOpencodeMessageID(before.Add(-time.Millisecond))[:16] {
		t.Fatalf("message ID %q is not in OpenCode's ascending form for this moment", id)
	}
	if f.c.LastAccepted.ItemID != id {
		t.Fatalf("receipt names %q, not the attempt's own message", f.c.LastAccepted.ItemID)
	}
}

func TestDaemonOpencodeHoldsMailWhileBusyOrShowingAnotherSession(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	appendDaemonMessage(t, f.c, "", "wait for idle")
	for _, tc := range []struct{ state, shown string }{{"busy", ocS1}, {"retry", ocS1}, {"idle", ocS2}, {"idle", ""}} {
		f.set(t, tc.state, tc.shown, "ok")
		if err := f.d.deliver(f.c); err != nil {
			t.Fatalf("%v: %v", tc, err)
		}
		if f.c.Pending != nil || f.c.Offset != 0 || len(f.requests(t, "deliver")) != 0 {
			t.Fatalf("%v: delivered or started an attempt while not ready", tc)
		}
	}
	f.set(t, "idle", ocS1, "ok")
	if err := f.d.deliver(f.c); err != nil || f.c.Accepted != 1 {
		t.Fatalf("idle again: accepted=%d %v", f.c.Accepted, err)
	}
}

func TestDaemonOpencodeUnconfirmedSubmitIsReconciledByLookupNotResent(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	appendDaemonMessage(t, f.c, "", "confirm me")
	f.set(t, "idle", ocS1, "unconfirmed")
	if err := f.d.deliver(f.c); err == nil || f.c.Pending == nil || f.c.Pending.MessageID == "" {
		t.Fatalf("unconfirmed submit released or accepted: %v %+v", err, f.c.Pending)
	}
	id := f.c.Pending.MessageID
	f.set(t, "busy", ocS1, "ok") // a pending lookup is not held behind the idle gate
	if err := f.d.deliver(f.c); err != nil || f.c.Accepted != 1 || f.c.LastAccepted.Attempt.MessageID != id {
		t.Fatalf("lookup did not accept the stored message: %v accepted=%d", err, f.c.Accepted)
	}
	if n := len(f.requests(t, "deliver")); n != 1 {
		t.Fatalf("message submitted %d times", n)
	}
}

func TestDaemonOpencodeProvedNotSubmittedReleasesTheAttempt(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	appendDaemonMessage(t, f.c, "", "try again")
	f.set(t, "idle", ocS1, "notsubmitted")
	if err := f.d.deliver(f.c); err == nil || f.c.Pending != nil || f.c.Offset != 0 {
		t.Fatalf("not-submitted proof kept the attempt or lost the message: %v %+v", err, f.c.Pending)
	}
	f.set(t, "idle", ocS1, "ok")
	if err := f.d.deliver(f.c); err != nil || f.c.Accepted != 1 {
		t.Fatalf("retry after proof: %v", err)
	}
	sent := f.requests(t, "deliver")
	if len(sent) != 2 || sent[0].MessageID == sent[1].MessageID {
		t.Fatalf("a released attempt should get a new attempt and ID: %+v", sent)
	}
}

func TestDaemonOpencodeUnknownOutcomeIsNeverResent(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	appendDaemonMessage(t, f.c, "", "maybe")
	f.set(t, "idle", ocS1, "unknown")
	if err := f.d.deliver(f.c); err == nil || f.c.Pending == nil {
		t.Fatalf("unknown outcome released the attempt: %v", err)
	}
	f.set(t, "idle", ocS1, "ok")
	err := f.d.deliver(f.c)
	if err == nil || !strings.Contains(err.Error(), "reconciliation") || f.c.Pending == nil {
		t.Fatalf("absent message after an unknown submit: %v", err)
	}
	if n := len(f.requests(t, "deliver")); n != 1 {
		t.Fatalf("uncertain message submitted %d times", n)
	}
}

func TestDaemonOpencodePendingMessageIDSurvivesReload(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	appendDaemonMessage(t, f.c, "", "across a restart")
	f.set(t, "idle", ocS1, "unconfirmed")
	_ = f.d.deliver(f.c)
	if f.c.Pending == nil {
		t.Fatal("no pending attempt")
	}
	id := f.c.Pending.MessageID
	f.d.closeQueue(f.c.ID)
	restarted := opencodeDaemon(t)
	c := restarted.connections[f.c.ID]
	if c == nil || c.Pending == nil || c.Pending.MessageID != id {
		t.Fatalf("reloaded attempt lost its message ID: %+v", c)
	}
	f.set(t, "idle", ocS1, "ok")
	if err := restarted.deliver(c); err != nil || c.Accepted != 1 || len(f.requests(t, "deliver")) != 1 {
		t.Fatalf("restart resent or failed to reconcile: %v", err)
	}
}

func TestDaemonOpencodeAcceptRequiresTheAttemptsOwnMessage(t *testing.T) {
	f := newOpencodeDeliveryFixture(t)
	f.c.Pending = &queueAttempt{ClientID: "cbus-x-0-y", MessageID: newOpencodeMessageID(time.Now())}
	for _, ev := range []*codexMessageLookup{nil, {State: codexMessageQueued, ItemID: f.c.Pending.MessageID}, {State: codexMessageReceived, ItemID: newOpencodeMessageID(time.Now())}} {
		if err := f.d.accept(f.c, ev); err == nil {
			t.Fatalf("accepted on %+v", ev)
		}
	}
}

func TestNewOpencodeMessageIDIsAscendingAndValid(t *testing.T) {
	t0 := time.UnixMilli(1792685049303)
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, newOpencodeMessageID(t0))
	}
	ids = append(ids, newOpencodeMessageID(t0.Add(time.Millisecond)))
	for i, id := range ids {
		if !validOpencodeMessageID(id) || len(id) != 30 {
			t.Fatalf("id %q", id)
		}
		// milliseconds*4096 for this instant is 0x1645b05d7000
		if i < 5 && !strings.HasPrefix(id, "msg_1645b05d7") {
			t.Fatalf("id %q does not encode its millisecond", id)
		}
	}
	if !strings.HasPrefix(ids[5], "msg_1645b05d8") || ids[5][:16] <= ids[0][:16] {
		t.Fatalf("a later millisecond did not sort later: %s <= %s", ids[5], ids[0])
	}
}
