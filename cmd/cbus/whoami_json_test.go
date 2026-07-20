package main

import (
	"encoding/json"
	"strings"
	"testing"

	"claudebus/internal/client"
)

func decodeWhoami(t *testing.T, s string) whoamiJSON {
	t.Helper()
	var doc whoamiJSON
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("whoami --json is not valid JSON: %v\n%s", err, s)
	}
	if doc.SchemaVersion != jsonSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", doc.SchemaVersion, jsonSchemaVersion)
	}
	return doc
}

// R16: ONE document shape for both states. An unjoined session gets the same keys
// with empty arrays, so a consumer parses one thing and never the sentence.
func TestWhoamiJSONSameShapeJoinedAndNot(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-who")

	var rc int
	out := captureStdout(t, func() { rc = run([]string{"whoami", "--json"}) })
	if rc != 1 {
		t.Errorf("unjoined rc = %d, want the frozen 1", rc)
	}
	if strings.Contains(out, "not joined in this session") {
		t.Errorf("the text sentence leaked into --json output: %q", out)
	}
	empty := decodeWhoami(t, out)
	if empty.Joined {
		t.Error("joined = true with no registrations")
	}
	if empty.Local == nil || empty.Remote == nil {
		t.Errorf("empty collections encoded as null, not []: %q", out)
	}
	if len(empty.Local) != 0 || len(empty.Remote) != 0 {
		t.Errorf("unjoined doc = %+v", empty)
	}
	if empty.SessionID != "SID-who" {
		t.Errorf("sessionId = %q", empty.SessionID)
	}

	if rc := captureRC(t, func() int { return run([]string{"join", "alpha", "me"}) }); rc != 0 {
		t.Fatal("setup join failed")
	}
	out = captureStdout(t, func() { rc = run([]string{"whoami", "--json"}) })
	if rc != 0 {
		t.Errorf("joined rc = %d, want 0", rc)
	}
	joined := decodeWhoami(t, out)
	if !joined.Joined {
		t.Error("joined = false after a join")
	}
	if len(joined.Local) != 1 || joined.Local[0].Channel != "alpha" || joined.Local[0].Alias != "me" {
		t.Errorf("local = %+v", joined.Local)
	}
	if joined.Remote == nil || len(joined.Remote) != 0 {
		t.Errorf("remote = %+v, want an empty array", joined.Remote)
	}

	// the two states differ only in values: same keys, both times
	if keysOf(t, out) != keysOf(t, captureStdout(t, func() { run([]string{"whoami", "--json"}) })) {
		t.Error("the key set moved between calls")
	}
}

// A remote from-default marker is a distinguishable kind, carrying the host a local
// registration cannot have. The marker is written by the REAL writer, the one
// `cbus tail <ch>@<host>` calls, not by hand-staging a file into .remote.
func TestWhoamiJSONSeparatesLocalFromRemote(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-both")
	if rc := captureRC(t, func() int { return run([]string{"join", "alpha", "me"}) }); rc != 0 {
		t.Fatal("setup join failed")
	}
	if err := client.WriteRemoteMarker("nuc", "ops", "mbp"); err != nil {
		t.Fatal(err)
	}
	var rc int
	doc := decodeWhoami(t, captureStdout(t, func() { rc = run([]string{"whoami", "--json"}) }))
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if len(doc.Local) != 1 || doc.Local[0].Alias != "me" {
		t.Errorf("local = %+v", doc.Local)
	}
	if len(doc.Remote) != 1 {
		t.Fatalf("remote = %+v, want the marker", doc.Remote)
	}
	r := doc.Remote[0]
	if r.Channel != "ops" || r.Host != "nuc" || r.Alias != "mbp" {
		t.Errorf("remote entry = %+v", r)
	}
}

// A marker alone, with no local registration, still counts as joined — the exit code
// and the flag must agree with each other in every combination, not just the common one.
func TestWhoamiJSONRemoteOnlyCountsAsJoined(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-remote-only")
	if err := client.WriteRemoteMarker("nuc", "ops", "mbp"); err != nil {
		t.Fatal(err)
	}
	var rc int
	doc := decodeWhoami(t, captureStdout(t, func() { rc = run([]string{"whoami", "--json"}) }))
	if rc != 0 {
		t.Errorf("rc = %d, want 0 — a remote marker is a registration", rc)
	}
	if !doc.Joined || len(doc.Local) != 0 || len(doc.Remote) != 1 {
		t.Errorf("doc = %+v", doc)
	}
}

func TestWhoamiJSONStillRejectsTrailingJunk(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SID-junk")
	var rc int
	errOut := captureStderr(t, func() {
		_ = captureStdout(t, func() { rc = run([]string{"whoami", "--json", "extra"}) })
	})
	if rc == 0 {
		t.Error("trailing junk accepted under --json")
	}
	if !strings.Contains(errOut, "usage: cbus whoami") {
		t.Errorf("stderr = %q", errOut)
	}
}

// keysOf returns the sorted top-level key set, so a shape comparison does not depend
// on the values underneath it.
func keysOf(t *testing.T, s string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return strings.Join(keys, ",")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
