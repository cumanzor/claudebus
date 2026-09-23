package client

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// systemLabel is what HostLabel returns with no override on this machine.
func systemLabel(t *testing.T) string {
	t.Helper()
	t.Setenv("CBUS_HOST", "")
	h, err := HostLabel()
	if err != nil {
		t.Fatalf("HostLabel with CBUS_HOST unset: %v", err)
	}
	return h
}

// thisHost is HostLabel for fixtures; a test env with an invalid CBUS_HOST is a setup bug.
func thisHost() string {
	h, err := HostLabel()
	if err != nil {
		panic(err)
	}
	return h
}

func mustHost(t *testing.T) string {
	t.Helper()
	h, err := HostLabel()
	if err != nil {
		t.Fatalf("HostLabel: %v", err)
	}
	return h
}

func TestHostLabelOverride(t *testing.T) {
	sys := systemLabel(t)
	for in, want := range map[string]string{
		"laptop-" + sys: "laptop-" + sys,
		"build.box":     "build", // machine-hostname rule: the part before the first dot
		"a_b-c":         "a_b-c",
	} {
		t.Setenv("CBUS_HOST", in)
		if got, err := HostLabel(); err != nil || got != want {
			t.Errorf("CBUS_HOST=%q: HostLabel() = %q, %v; want %q", in, got, err, want)
		}
	}
	t.Setenv("CBUS_HOST", "")
	if got := mustHost(t); got != sys {
		t.Errorf("empty CBUS_HOST must mean unset: got %q, want %q", got, sys)
	}
}

func TestInvalidHostLabelIsAnErrorNeverAFallback(t *testing.T) {
	sys := systemLabel(t)
	// ValidStoreName (D12): a leading '-' reads as a flag wherever the label is passed;
	// "box." is refused on the raw value even though its short form would be valid.
	for _, bad := range []string{"bad/host", "two words", "..", "host\n", ".hidden", "é", "-box", "box."} {
		t.Setenv("CBUS_HOST", bad)
		got, err := HostLabel()
		if err == nil || !errors.Is(err, ErrBadHostLabel) {
			t.Errorf("CBUS_HOST=%q: want ErrBadHostLabel, got %q, %v", bad, got, err)
			continue
		}
		if got != "" || got == sys {
			t.Errorf("CBUS_HOST=%q: an invalid override returned label %q; it must return none", bad, got)
		}
		msg := err.Error()
		for _, want := range []string{"CBUS_HOST", strings.TrimSuffix(strings.TrimPrefix(quote(bad), `"`), `"`), "letters, digits", "not starting with '.' or '-'", "unset it"} {
			if !strings.Contains(msg, want) {
				t.Errorf("CBUS_HOST=%q: error %q does not tell the user how to fix it (missing %q)", bad, msg, want)
			}
		}
	}
}

func quote(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "\\", `\\`), "\n", `\n`) }

func TestHostOverrideRecordedInMetaLedgerAndFormation(t *testing.T) {
	if systemLabel(t) == "laptop" {
		t.Skip("system hostname is already laptop; the override would be indistinguishable")
	}
	root := setupStore(t)
	t.Setenv("CBUS_HOST", "laptop")
	if _, _, err := Join("cc", "coder"); err != nil {
		t.Fatalf("join: %v", err)
	}
	m, ok := ReadPeerMeta(filepath.Join(root, "cc", "coder", "meta.json"))
	if !ok || m.Host != "laptop" {
		t.Errorf("meta.host = %q (ok=%v), want the override laptop", m.Host, ok)
	}
	evs := ReadLedger("cc")
	if len(evs) == 0 {
		t.Fatal("join wrote no ledger event")
	}
	for _, ev := range evs {
		if ev.Host != "laptop" {
			t.Errorf("ledger %s event host = %q, want laptop", ev.Event, ev.Host)
		}
	}
	f, _, err := SaveFormation("hostpin", "cc", nil)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(f.Peers) != 1 || f.Peers[0].Machine != "laptop" {
		t.Fatalf("formation peers = %+v, want one peer on machine laptop", f.Peers)
	}
	p := f.Peers[0]
	p.SessionID = "no-such-session"
	if _, why := p.SidState(); strings.HasPrefix(why, "recorded on") {
		t.Errorf("peer recorded on laptop judged foreign under CBUS_HOST=laptop: %q", why)
	}
	t.Setenv("CBUS_HOST", "elsewhere")
	if _, why := p.SidState(); why != "recorded on laptop" {
		t.Errorf("under a different label the peer should read as recorded elsewhere, got %q", why)
	}
	t.Setenv("CBUS_HOST", "bad/host")
	if st, why := p.SidState(); st != SidUnchecked || !strings.Contains(why, "CBUS_HOST") {
		t.Errorf("invalid label must surface in the sid check, got %v %q", st, why)
	}
}

func TestInvalidHostLabelLeavesNoTrace(t *testing.T) {
	root := setupStore(t)
	t.Setenv("CBUS_HOST", "bad/host")
	if _, _, err := Join("cc", "coder"); !errors.Is(err, ErrBadHostLabel) {
		t.Errorf("Join under an invalid CBUS_HOST: want ErrBadHostLabel, got %v", err)
	}
	if _, err := ReserveAlias("cc", "coder", OriginFresh, ""); !errors.Is(err, ErrBadHostLabel) {
		t.Errorf("ReserveAlias under an invalid CBUS_HOST: want ErrBadHostLabel, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cc")); !os.IsNotExist(err) {
		t.Errorf("an invalid CBUS_HOST left a channel dir behind: %v", err)
	}
	if evs := ReadLedger("cc"); len(evs) != 0 {
		t.Errorf("an invalid CBUS_HOST still wrote %d ledger events", len(evs))
	}
	HookJoin(strings.NewReader(`{"session_id":"hook-sid"}`), "cc", "coder", "")
	if _, err := os.Stat(filepath.Join(root, "cc")); !os.IsNotExist(err) {
		t.Errorf("hook-join registered a peer under an invalid CBUS_HOST: %v", err)
	}
}

func TestDaemonRecordsTheClientHostLabel(t *testing.T) {
	t.Setenv("CBUS_HOST", "daemon-label")
	for _, tc := range []struct{ sent, want string }{
		{"client-label", "client-label"},
		{"", "daemon-label"}, // an older client sends none
	} {
		d, _, req := daemonFixture(t)
		req.Host = tc.sent
		c := mustDaemonConnect(t, d, req)
		m, ok := ReadPeerMeta(filepath.Join(d.peerDir(c), "meta.json"))
		if !ok || m.Host != tc.want {
			t.Errorf("request host %q: meta.host = %q (ok=%v), want %q", tc.sent, m.Host, ok, tc.want)
		}
	}
}

func TestDaemonRefusesAnInvalidRequestHostBeforeMutation(t *testing.T) {
	for _, bad := range []string{"bad/host", "dotted.name", "..", "two words", "-box", "box."} {
		t.Run(bad, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			req.Host = bad
			if _, err := d.connect(req); !errors.Is(err, ErrBadHostLabel) {
				t.Fatalf("request host %q was not refused: %v", bad, err)
			}
			if q.opens != 0 || len(d.statusSnapshots()) != 0 || dirExists(filepath.Join(CBUSDir(), req.Channel)) {
				t.Fatal("refused request still opened a queue or claimed peer state")
			}
		})
	}
}

func TestChildLaunchEnvCarriesTheHostLabel(t *testing.T) {
	t.Setenv("CBUS_HOST", "")
	env, err := forkReplicatedEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := env["CBUS_HOST"]; ok {
		t.Error("CBUS_HOST unset in the parent must not appear in the child env")
	}
	t.Setenv("CBUS_HOST", "laptop")
	if env, err = forkReplicatedEnv(); err != nil || env["CBUS_HOST"] != "laptop" {
		t.Errorf("claude child env CBUS_HOST = %q, %v; want laptop", env["CBUS_HOST"], err)
	}
	if env, err = peerEnv("beta"); err != nil || env["CBUS_HOST"] != "laptop" {
		t.Errorf("formation apply child env CBUS_HOST = %q, %v; want laptop", env["CBUS_HOST"], err)
	}
}
