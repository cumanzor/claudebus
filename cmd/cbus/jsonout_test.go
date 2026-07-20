package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// decodeList parses a --json document. Every assertion below reads DECODED fields
// rather than matching strings, so a formatting change cannot pass for a schema
// change and vice versa.
func decodeList(t *testing.T, s string) listJSON {
	t.Helper()
	var doc listJSON
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("list --json is not valid JSON: %v\n%s", err, s)
	}
	if doc.SchemaVersion != jsonSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", doc.SchemaVersion, jsonSchemaVersion)
	}
	return doc
}

func jsonStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-json")
	return root
}

func TestListJSONShape(t *testing.T) {
	jsonStore(t)
	for _, a := range []string{"one", "two"} {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-"+a)
		if rc := captureRC(t, func() int { return run([]string{"join", "alpha", a}) }); rc != 0 {
			t.Fatalf("join %s rc=%d", a, rc)
		}
	}
	out := captureStdout(t, func() { run([]string{"list", "--json"}) })
	doc := decodeList(t, out)

	if doc.Host == "" {
		t.Error("host is empty")
	}
	if len(doc.Channels) != 1 || doc.Channels[0].Name != "alpha" {
		t.Fatalf("channels = %+v", doc.Channels)
	}
	peers := doc.Channels[0].Peers
	if len(peers) != 2 {
		t.Fatalf("peers = %+v", peers)
	}
	for _, p := range peers {
		if p.Scope != "local" {
			t.Errorf("peer %s scope = %q, want local (the key must exist before remote does)", p.Alias, p.Scope)
		}
		if p.Listening {
			t.Errorf("peer %s reports listening with no armed tail", p.Alias)
		}
		if p.ListenerPid != 0 {
			t.Errorf("peer %s listenerPid = %d, want the key absent for a never-armed peer", p.Alias, p.ListenerPid)
		}
		if p.SessionID != "sid-"+p.Alias {
			t.Errorf("peer %s sessionId = %q", p.Alias, p.SessionID)
		}
		if p.Cwd == "" || p.Host == "" {
			t.Errorf("peer %s lost cwd/host: %+v", p.Alias, p)
		}
	}
	// a never-armed peer must not carry a listenerPid key at all
	if strings.Contains(out, "listenerPid") {
		t.Errorf("listenerPid is present for never-armed peers:\n%s", out)
	}
}

// The pre-existing trap: runList's flag loop took the last non-flag token as the
// channel filter, so `cbus list --json` filtered on a channel literally named
// "--json" and printed "no peers registered" with exit 0. A silent wrong answer.
func TestListJSONIsNotReadAsAChannelFilter(t *testing.T) {
	jsonStore(t)
	if rc := captureRC(t, func() int { return run([]string{"join", "alpha", "one"}) }); rc != 0 {
		t.Fatal("setup join failed")
	}
	out := captureStdout(t, func() { run([]string{"list", "--json"}) })
	doc := decodeList(t, out)
	if len(doc.Channels) != 1 {
		t.Fatalf("--json was swallowed as a channel filter: %s", out)
	}
	// and the filter itself still works when one is actually passed
	filtered := decodeList(t, captureStdout(t, func() { run([]string{"list", "--json", "nosuch"}) }))
	if len(filtered.Channels) != 0 {
		t.Errorf("channel filter ignored under --json: %+v", filtered.Channels)
	}
}

// R15: --json cannot serve a relay target in M5, and BOTH flag orders were silent
// wrong answers before this gate — one dropped the flag, the other turned the
// @-target into a local channel filter.
func TestRemoteJSONFailsLoudlyInBothFlagOrders(t *testing.T) {
	jsonStore(t)
	for _, args := range [][]string{
		{"list", "@nuc", "--json"},
		{"list", "--json", "@nuc"},
		{"list", "ops@nuc", "--json"},
		{"list", "--json", "ops@nuc"},
		{"active", "@nuc", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var rc int
			errOut := captureStderr(t, func() {
				_ = captureStdout(t, func() { rc = run(args) })
			})
			if rc == 0 {
				t.Errorf("rc = 0, want non-zero")
			}
			if !strings.Contains(errOut, "--json is local-only") {
				t.Errorf("stderr = %q, want the local-only refusal", errOut)
			}
		})
	}
}

// R18: a legacy v1 entry is represented explicitly and can never choke a consumer
// that iterates channels[].peers[] — the array is present and empty, never null.
func TestListJSONLegacyV1IsExplicitAndHasAnEmptyPeerArray(t *testing.T) {
	root := jsonStore(t)
	// hand-staged, historically reachable: only the retired bash v1 client wrote a
	// channel-level meta.json (docs/architecture/protocol.md:139). No Go path does.
	if err := os.MkdirAll(filepath.Join(root, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "old", "meta.json"), []byte(`{"alias":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { run([]string{"list", "--json"}) })
	if strings.Contains(out, "null") {
		t.Errorf("a null slipped into the document — a consumer iterating peers would choke:\n%s", out)
	}
	doc := decodeList(t, out)
	if len(doc.Channels) != 1 || !doc.Channels[0].LegacyV1 {
		t.Fatalf("legacy v1 entry not marked: %+v", doc.Channels)
	}
	if doc.Channels[0].Peers == nil || len(doc.Channels[0].Peers) != 0 {
		t.Errorf("legacy peers = %+v, want an empty array", doc.Channels[0].Peers)
	}
	// --active drops it, matching the text path
	act := decodeList(t, captureStdout(t, func() { run([]string{"list", "--active", "--json"}) }))
	if len(act.Channels) != 0 {
		t.Errorf("--active kept the legacy entry: %+v", act.Channels)
	}
}

// An empty store is a valid document, not a sentence: stdout carries exactly the JSON.
func TestListAndChannelsJSONOnAnEmptyStore(t *testing.T) {
	jsonStore(t)
	out := captureStdout(t, func() {
		if rc := run([]string{"list", "--json"}); rc != 0 {
			t.Errorf("rc = %d, want 0", rc)
		}
	})
	if strings.Contains(out, "no peers registered") {
		t.Errorf("the text sentence leaked into --json output: %q", out)
	}
	doc := decodeList(t, out)
	if doc.Channels == nil || len(doc.Channels) != 0 {
		t.Errorf("channels = %+v, want an empty array", doc.Channels)
	}
	cOut := captureStdout(t, func() { run([]string{"channels", "--json"}) })
	var cDoc channelsJSON
	if err := json.Unmarshal([]byte(cOut), &cDoc); err != nil {
		t.Fatalf("channels --json invalid: %v\n%s", err, cOut)
	}
	if cDoc.Channels == nil || len(cDoc.Channels) != 0 {
		t.Errorf("channels = %+v, want an empty array", cDoc.Channels)
	}
}

func TestChannelsJSONCounts(t *testing.T) {
	jsonStore(t)
	for _, p := range [][2]string{{"alpha", "one"}, {"alpha", "two"}, {"beta", "solo"}} {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-"+p[0]+p[1])
		if rc := captureRC(t, func() int { return run([]string{"join", p[0], p[1]}) }); rc != 0 {
			t.Fatalf("join %v rc=%d", p, rc)
		}
	}
	var doc channelsJSON
	out := captureStdout(t, func() { run([]string{"channels", "--json"}) })
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid: %v\n%s", err, out)
	}
	want := map[string][2]int{"alpha": {2, 0}, "beta": {1, 0}}
	if len(doc.Channels) != len(want) {
		t.Fatalf("channels = %+v", doc.Channels)
	}
	for _, c := range doc.Channels {
		w, ok := want[c.Name]
		if !ok {
			t.Errorf("unexpected channel %q", c.Name)
			continue
		}
		if c.Peers != w[0] || c.Listening != w[1] {
			t.Errorf("%s = %d peers / %d listening, want %d / %d", c.Name, c.Peers, c.Listening, w[0], w[1])
		}
	}
}

// A torn meta.json keeps its peer, with blank fields — the semantic that separates
// ScanStore from ChannelRoster, which drops it. Reachable history rather than
// invention: the bash client rewrote meta.json in place with a non-atomic json.dump,
// so a reader could see it truncated (docs/architecture/port-map.md row 5), and a
// damaged file survives from that era. Hiding such a peer is how a user loses track
// of a session, so list has always shown it with "?" columns.
func TestListJSONKeepsATornMetaPeer(t *testing.T) {
	root := jsonStore(t)
	if rc := captureRC(t, func() int { return run([]string{"join", "alpha", "good"}) }); rc != 0 {
		t.Fatal("setup join failed")
	}
	torn := filepath.Join(root, "alpha", "torn")
	if err := os.MkdirAll(torn, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(torn, "meta.json"), []byte(`{"alias":"torn","cw`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := decodeList(t, captureStdout(t, func() { run([]string{"list", "--json"}) }))
	var aliases []string
	for _, p := range doc.Channels[0].Peers {
		aliases = append(aliases, p.Alias)
	}
	if len(aliases) != 2 || aliases[0] != "good" || aliases[1] != "torn" {
		t.Fatalf("peers = %v, want both good and torn kept", aliases)
	}
	for _, p := range doc.Channels[0].Peers {
		if p.Alias == "torn" && (p.Cwd != "" || p.Listening) {
			t.Errorf("torn peer invented fields: %+v", p)
		}
	}
}

// The anti-drift assertion, and the reason text and JSON share one traversal: for a
// REAL armed listener, the JSON `listening` flag and the text `listen` prefix must
// agree peer for peer.
//
// HARNESS EXCEPTION to the never-run-`cbus tail`-under-Bash doctrine, as in
// TestListRenderingGolden: the follower is a CHILD process killed on cleanup, so it
// cannot wedge the test. A live listener is unreachable any other way — the predicate
// wants a live pid whose start time matches the recorded witness.
func TestListJSONLivenessAgreesWithText(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cbus")
	if out, err := exec.Command("go", "build", "-o", bin, "claudebus/cmd/cbus").CombinedOutput(); err != nil {
		t.Fatalf("build cbus: %v\n%s", err, out)
	}
	root := t.TempDir()
	env := func(sid string) []string {
		e := []string{"CBUS_DIR=" + root, "CLAUDE_CODE_SESSION_ID=" + sid}
		for _, kv := range os.Environ() {
			switch strings.SplitN(kv, "=", 2)[0] {
			case "CBUS_DIR", "CLAUDE_CODE_SESSION_ID", "CBUS_UPDATE_CHECK":
			default:
				e = append(e, kv)
			}
		}
		return e
	}
	cbus := func(sid string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env(sid)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cbus %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	cbus("sid-live", "join", "alpha", "live")
	cbus("sid-dark", "join", "alpha", "dark")

	tail := exec.Command(bin, "tail", "alpha/live")
	tail.Env = env("sid-live")
	if err := tail.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tail.Process.Kill()
		_, _ = tail.Process.Wait()
	})
	waitArmed(t, filepath.Join(root, "alpha", "live", "meta.json"))

	text := cbus("sid-dark", "list")
	doc := decodeList(t, cbus("sid-dark", "list", "--json"))
	for _, p := range doc.Channels[0].Peers {
		textListening := false
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "alpha/"+p.Alias+" ") {
				textListening = strings.HasPrefix(line, "listen")
			}
		}
		if p.Listening != textListening {
			t.Errorf("peer %s: json listening=%v, text listening=%v — the two renderings disagree",
				p.Alias, p.Listening, textListening)
		}
	}
	if !doc.Channels[0].Peers[1].Listening { // "live" sorts after "dark"
		t.Errorf("the armed peer is not listening in JSON: %+v", doc.Channels[0].Peers)
	}
	if doc.Channels[0].Peers[1].ListenerPid != tail.Process.Pid {
		t.Errorf("listenerPid = %d, want the tail process %d",
			doc.Channels[0].Peers[1].ListenerPid, tail.Process.Pid)
	}
	// --active under --json keeps only the armed one
	act := decodeList(t, cbus("sid-dark", "list", "--active", "--json"))
	if len(act.Channels) != 1 || len(act.Channels[0].Peers) != 1 || act.Channels[0].Peers[0].Alias != "live" {
		t.Errorf("--active --json = %+v", act.Channels)
	}
}
