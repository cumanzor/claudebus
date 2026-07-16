package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/client"
)

// saveFixture plants an envelope directly, so the cmd tests do not depend on the
// save verb (a later milestone).
func saveFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	fdir := filepath.Join(dir, ".formations")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, name+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// thisMachine / otherMachine build fixtures against the host the test is RUNNING
// on. A literal hostname would make the suite pass on one developer's laptop and
// fail everywhere else — including the NUC, where a release rebuild runs it.
// otherMachine is foreign by construction (strictly longer than any hostname it is
// derived from), so the "recorded elsewhere" case cannot invert on a host that
// happens to be named like the fixture.
func thisMachine() string  { return client.ShortHostname() }
func otherMachine() string { return client.ShortHostname() + "-elsewhere" }

func fixtureRoles() string {
	return `{
  "schema": "cbus-formation/v1",
  "name": "roles",
  "channel": "roles",
  "host": null,
  "anchorAlias": "orchestrator",
  "savedAt": "2026-07-16T07:50:00Z",
  "savedBy": "roles/orchestrator",
  "drift_anchors": { "git_head": "e844702" },
  "payload": { "work_state": "see the tracker item" },
  "peers": [
    { "alias": "orchestrator", "model": "fable", "rolefile": "roles/orchestrator.md@b3a806e",
      "origin": "joined", "mode": "template", "target": "tab", "machine": "` + thisMachine() + `" },
    { "alias": "coder", "model": "opus", "origin": "fresh", "mode": "resume",
      "sessionId": "deadbeef-0000-0000-0000-000000000000", "onStale": "template",
      "target": "tab", "machine": "` + thisMachine() + `" }
  ]
}`
}

func TestFormationListVerb(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)

	out := captureStdout(t, func() {
		if rc := runFormation([]string{"list"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "no formations saved") {
		t.Errorf("empty list = %q", out)
	}

	saveFixture(t, dir, "roles", fixtureRoles())
	saveFixture(t, dir, "broken", `{"schema":"cbus-workspace-snapshot/v3-draft"}`)
	out = captureStdout(t, func() {
		if rc := runFormation([]string{"list"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "roles") || !strings.Contains(out, "channel=roles") || !strings.Contains(out, "peers=2") {
		t.Errorf("list row missing: %q", out)
	}
	// an unreadable envelope is listed WITH its reason, not silently dropped
	if !strings.Contains(out, "broken") || !strings.Contains(out, "unreadable") {
		t.Errorf("broken envelope not surfaced: %q", out)
	}
}

func TestFormationShowVerb(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "cfg"))
	t.Setenv("HOME", t.TempDir())
	saveFixture(t, dir, "roles", fixtureRoles())

	out := captureStdout(t, func() {
		if rc := runFormation([]string{"show", "roles"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	for _, want := range []string{
		"formation: roles",
		"channel:   roles (local)",
		"anchor:    orchestrator",
		"[anchor]",
		"roles/orchestrator.md@b3a806e",
		"peers (2):",
		"git_head",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
	// coder: sid recorded, no transcript on this machine, machine tag == this host
	if !strings.Contains(out, "STALE") || !strings.Contains(out, "onStale=template") {
		t.Errorf("stale sid not flagged:\n%s", out)
	}
	// coder has neither rolefile nor role text
	if !strings.Contains(out, "TODO") {
		t.Errorf("role TODO not flagged:\n%s", out)
	}
	if !strings.Contains(out, "warnings: 1 stale sid(s), 1 role TODO(s)") {
		t.Errorf("warning summary missing:\n%s", out)
	}
	// the payload is rendered, and labeled as something cbus does not follow
	if !strings.Contains(out, "opaque") || !strings.Contains(out, "see the tracker item") {
		t.Errorf("payload not rendered as opaque:\n%s", out)
	}
}

// TestFormationShowUncheckedNotStale: a peer recorded on another machine must not
// be called stale by a host that cannot see its transcripts.
func TestFormationShowUncheckedNotStale(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "cfg"))
	t.Setenv("HOME", t.TempDir())
	// the name field must match the filename (a formation's identity is stated in
	// both places and they have to agree)
	body := strings.Replace(fixtureRoles(), `"name": "roles",`, `"name": "far",`, 1)
	saveFixture(t, dir, "far", strings.Replace(body,
		`"machine": "`+thisMachine()+`" }
  ]`, `"machine": "`+otherMachine()+`" }
  ]`, 1))

	out := captureStdout(t, func() {
		if rc := runFormation([]string{"show", "far"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "unchecked — recorded on "+otherMachine()) {
		t.Errorf("foreign-machine sid should read unchecked:\n%s", out)
	}
	if strings.Contains(out, "STALE") {
		t.Errorf("foreign-machine sid must not be called stale:\n%s", out)
	}
	if !strings.Contains(out, "warnings: 1 role TODO(s)") {
		t.Errorf("unchecked must not count as a stale warning:\n%s", out)
	}
}

// plantMeta writes a peer registration so the save verb reads a real roster.
func plantMeta(t *testing.T, dir, ch, alias, sid string) {
	t.Helper()
	pdir := filepath.Join(dir, ch, alias)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"alias":"` + alias + `","channel":"` + ch + `","sessionId":"` + sid + `",` +
		`"cwd":"/Users/dev/repos/AI/claudebus","listenerPid":null,"ownerPid":null,` +
		`"host":"` + thisMachine() + `","ts":"2026-07-16T18:00:00Z"}`
	if err := os.WriteFile(filepath.Join(pdir, "meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFormationSaveVerb(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	plantMeta(t, dir, "roles", "orchestrator", "sid-orch")
	plantMeta(t, dir, "roles", "coder", "sid-coder")

	// channel omitted: resolved from this session's own registration
	out := captureStdout(t, func() {
		if rc := runFormation([]string{"save", "roles"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	for _, want := range []string{`saved formation "roles"`, "new", "+2 new", "coder", "orchestrator",
		"model, rolefile/role, origin and profile are yours to fill in", "cbus formation show roles"} {
		if !strings.Contains(out, want) {
			t.Errorf("save output missing %q:\n%s", want, out)
		}
	}

	// a re-save is a refresh, and says so
	out = captureStdout(t, func() {
		if rc := runFormation([]string{"save", "roles", "roles"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "refreshed") || strings.Contains(out, "+2 new") {
		t.Errorf("re-save should refresh, not re-add:\n%s", out)
	}
}

func TestFormationSaveChannelResolution(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-me")

	// not joined anywhere and no channel given: refuse rather than guess
	if rc := runFormation([]string{"save", "x"}); rc == 0 {
		t.Error("want a refusal when the session is not joined and no channel is given")
	}
	// joined to two channels: refuse rather than silently take the first
	plantMeta(t, dir, "one", "me", "sid-me")
	plantMeta(t, dir, "two", "me", "sid-me")
	if rc := runFormation([]string{"save", "x"}); rc == 0 {
		t.Error("want a refusal when the session is joined to several channels")
	}
	// explicit channel resolves it
	if rc := runFormation([]string{"save", "x", "one"}); rc != 0 {
		t.Error("an explicit channel should save")
	}
}

func TestFormationVerbErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no subverb", []string{}},
		{"unknown subverb", []string{"frobnicate"}},
		{"apply is not built yet", []string{"apply", "x"}},
		{"save with no name", []string{"save"}},
		{"save trailing junk", []string{"save", "a", "b", "c"}},
		{"save bad name", []string{"save", "a/b", "roles"}},
		{"save unknown channel", []string{"save", "x", "ghostchannel"}},
		{"show with no name", []string{"show"}},
		{"show trailing junk", []string{"show", "a", "b"}},
		{"show missing", []string{"show", "ghost"}},
		{"rm with no name", []string{"rm"}},
		{"rm trailing junk", []string{"rm", "a", "b"}},
		{"rm missing", []string{"rm", "ghost"}},
		{"list trailing junk", []string{"list", "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rc := runFormation(tc.args); rc == 0 {
				t.Errorf("runFormation(%v): want rc!=0", tc.args)
			}
		})
	}
}

func TestFormationRmVerb(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	saveFixture(t, dir, "roles", fixtureRoles())
	out := captureStdout(t, func() {
		if rc := runFormation([]string{"rm", "roles"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, `removed formation "roles"`) {
		t.Errorf("rm output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".formations", "roles.json")); !os.IsNotExist(err) {
		t.Errorf("file survived rm: %v", err)
	}
}

// TestFormationDispatch: the verb is reachable from the top-level dispatcher, and
// the help advertises only the subverbs that exist.
func TestFormationDispatch(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	out := captureStdout(t, func() {
		if rc := run([]string{"formation", "list"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "no formations saved") {
		t.Errorf("dispatch = %q", out)
	}
	// the help must advertise what exists and nothing else
	if !strings.Contains(formationUsage, "save") {
		t.Errorf("usage omits a built verb: %q", formationUsage)
	}
	for _, unbuilt := range []string{"apply", "bootstrap"} {
		if strings.Contains(formationUsage, unbuilt) {
			t.Errorf("usage advertises an unbuilt verb %q: %s", unbuilt, formationUsage)
		}
	}
	if !strings.Contains(usage, "cbus formation list") || !strings.Contains(usage, "cbus formation show") {
		t.Error("cbus --help does not mention the formation verbs")
	}
}
