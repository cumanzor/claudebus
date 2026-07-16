package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// designExample is the envelope from cbus-zmv design r2 §2, verbatim in shape and
// key naming (the mixed drift_anchors/anchorAlias casing included). It is the
// fixture the round-trip and preservation tests run on, so a schema drift away
// from the written spec fails here first.
const designExample = `{
  "schema": "cbus-formation/v1",
  "name": "roles",
  "channel": "roles",
  "host": null,
  "anchorAlias": "orchestrator",
  "savedAt": "2026-07-16T07:50:00Z",
  "savedBy": "roles/orchestrator",
  "drift_anchors": { "git_head": "e844702", "notes": "apply verifies and reports; bd is re-queried, never trusted from the file" },
  "payload": {
    "work_state": "bd cbus-vj9",
    "blockers": "bd cbus-vj9 notes",
    "_comment": "opaque to the tool; orchestrator-authored; see section 4"
  },
  "peers": [
    {
      "alias": "orchestrator",
      "model": "fable",
      "rolefile": "roles/orchestrator.md@b3a806e",
      "role": null,
      "origin": "joined",
      "mode": "template",
      "sessionId": "c5463763-40d6-463c-9fcc-407c6000cd47",
      "onStale": "template",
      "profile": "personal",
      "cwd": "/Users/dev/repos/AI/claudebus",
      "target": "tab",
      "machine": "mbp",
      "addresses": []
    },
    {
      "alias": "coder",
      "model": "opus",
      "rolefile": "roles/coder.md@b3a806e",
      "role": null,
      "origin": "fresh",
      "mode": "template",
      "sessionId": "a26d120e-4d73-4d91-8550-498ab65a5107",
      "onStale": "template",
      "profile": "personal",
      "cwd": "/Users/dev/repos/AI/claudebus",
      "target": "tab",
      "machine": "mbp",
      "addresses": []
    }
  ]
}`

func loadExample(t *testing.T) *Formation {
	t.Helper()
	var f Formation
	if err := json.Unmarshal([]byte(designExample), &f); err != nil {
		t.Fatalf("unmarshal design example: %v", err)
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("design example must validate: %v", err)
	}
	return &f
}

// TestFormationRoundTripConvergence is the envelope's core contract: save, load
// back, save again — the second write is byte-identical to the first, so a
// re-save never churns the file.
func TestFormationRoundTripConvergence(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := loadExample(t)
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	path, _ := FormationPath("roles")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	back, err := LoadFormation("roles")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := back.Save(); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("save is not convergent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !reflect.DeepEqual(f.Peers, back.Peers) {
		t.Errorf("peers changed across a round-trip:\n got %+v\nwant %+v", back.Peers, f.Peers)
	}
	if back.AnchorAlias != "orchestrator" || back.Channel != "roles" || back.SavedBy != "roles/orchestrator" {
		t.Errorf("envelope fields changed across a round-trip: %+v", back)
	}
	if back.Host != nil {
		t.Errorf("host: want nil (null = local), got %q", *back.Host)
	}
}

// TestFormationPreservesHandEdits: the file is hand-editable, so keys this
// version does not model must survive a save refresh — top level and per peer.
func TestFormationPreservesHandEdits(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	edited := strings.Replace(designExample,
		`"savedBy": "roles/orchestrator",`,
		`"savedBy": "roles/orchestrator",
  "_hand_note": "carlos: keep this line",
  "future_field": { "nested": [1, 2, 3] },`, 1)
	edited = strings.Replace(edited,
		`"alias": "coder",`,
		`"alias": "coder",
      "notes": "owned the tree; sid is a checkpoint only",`, 1)

	var f Formation
	if err := json.Unmarshal([]byte(edited), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	path, _ := FormationPath("roles")
	b, _ := os.ReadFile(path)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if s := string(got["_hand_note"]); s != `"carlos: keep this line"` {
		t.Errorf("_hand_note lost or mangled: %s", s)
	}
	if s := string(got["future_field"]); !strings.Contains(s, `"nested"`) {
		t.Errorf("future_field lost or mangled: %s", s)
	}
	if !strings.Contains(string(b), `"notes": "owned the tree; sid is a checkpoint only"`) {
		t.Errorf("per-peer hand-edited key lost:\n%s", b)
	}
	// and it still converges with the extras present
	back, err := LoadFormation("roles")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := back.Save(); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	again, _ := os.ReadFile(path)
	if string(b) != string(again) {
		t.Errorf("hand-edited envelope does not converge:\n%s\n---\n%s", b, again)
	}
}

// TestFormationPayloadOpaque: the payload is by-reference and orchestrator-authored;
// the tool stores and re-emits it verbatim and never interprets what is inside.
func TestFormationPayloadOpaque(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := loadExample(t)
	f.Payload = json.RawMessage(`{"work_state":"see tracker item X","deep":{"a":[1,{"b":null}]},"_comment":"opaque"}`)
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := LoadFormation("roles")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var want, got any
	if err := json.Unmarshal(f.Payload, &want); err != nil {
		t.Fatalf("fixture payload: %v", err)
	}
	if err := json.Unmarshal(back.Payload, &got); err != nil {
		t.Fatalf("payload did not survive as JSON: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("payload not passed through verbatim:\n got %s\nwant %s", back.Payload, f.Payload)
	}
}

// TestFormationEmissionOrder pins the key order: known fields in spec order, then
// hand-edited unknowns sorted. Deterministic order is what makes save convergent.
func TestFormationEmissionOrder(t *testing.T) {
	f := loadExample(t)
	f.Extra = map[string]json.RawMessage{
		"zeta":  json.RawMessage(`1`),
		"alpha": json.RawMessage(`2`),
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := []string{"schema", "name", "channel", "host", "anchorAlias", "savedAt",
		"savedBy", "drift_anchors", "payload", "peers", "alpha", "zeta"}
	var got []string
	dec := json.NewDecoder(strings.NewReader(string(b)))
	tok, _ := dec.Token() // '{'
	_ = tok
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		got = append(got, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("decode value: %v", err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("emission order:\n got %v\nwant %v", got, want)
	}
}

// TestFormationFieldsCoverJSONTags guards the one drift surface in the design:
// fields() drives BOTH emission order and the known-key set, so a struct field
// added without a fields() entry would silently emit nothing AND be re-emitted as
// an "unknown" key. Reflection makes that a test failure instead of a data bug.
func TestFormationFieldsCoverJSONTags(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typ    reflect.Type
		fields []jsonField
	}{
		{"Formation", reflect.TypeOf(Formation{}), (&Formation{}).fields()},
		{"FormationPeer", reflect.TypeOf(FormationPeer{}), (&FormationPeer{}).fields()},
	} {
		emitted := knownKeys(tc.fields)
		for i := 0; i < tc.typ.NumField(); i++ {
			tag := tc.typ.Field(i).Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			key, _, _ := strings.Cut(tag, ",")
			if !emitted[key] {
				t.Errorf("%s.%s (json:%q) is not in fields() — it would be dropped on save",
					tc.name, tc.typ.Field(i).Name, key)
			}
		}
		if len(emitted) != len(tc.fields) {
			t.Errorf("%s: fields() has duplicate keys", tc.name)
		}
	}
}

func TestFormationValidate(t *testing.T) {
	valid := func() *Formation { return loadExample(t) }
	for _, tc := range []struct {
		name string
		mut  func(*Formation)
		want string
	}{
		{"ok", func(*Formation) {}, ""},
		{"bad schema", func(f *Formation) { f.Schema = "cbus-formation/v2" }, "unsupported schema"},
		{"empty schema", func(f *Formation) { f.Schema = "" }, "unsupported schema"},
		{"bad name", func(f *Formation) { f.Name = "has space" }, "formation name"},
		{"empty name", func(f *Formation) { f.Name = "" }, "formation name"},
		{"name with slash", func(f *Formation) { f.Name = "a/b" }, "formation name"},
		{"bad channel", func(f *Formation) { f.Channel = "a/b" }, "channel must be"},
		{"bad anchorAlias", func(f *Formation) { f.AnchorAlias = "a b" }, "anchorAlias must be"},
		{"anchor not a peer", func(f *Formation) { f.AnchorAlias = "ghost" }, "not one of the peers"},
		{"no anchor is fine", func(f *Formation) { f.AnchorAlias = "" }, ""},
		{"dup alias", func(f *Formation) { f.Peers[1].Alias = "orchestrator" }, "duplicate peer alias"},
		{"bad peer alias", func(f *Formation) { f.Peers[0].Alias = "bad/alias" }, "alias must be"},
		{"bad mode", func(f *Formation) { f.Peers[0].Mode = "restore" }, "mode must be one of"},
		{"empty mode ok", func(f *Formation) { f.Peers[0].Mode = "" }, ""},
		{"bad origin", func(f *Formation) { f.Peers[0].Origin = "spawned" }, "origin must be one of"},
		{"empty origin ok", func(f *Formation) { f.Peers[0].Origin = "" }, ""},
		{"bad onStale", func(f *Formation) { f.Peers[0].OnStale = "retry" }, "onStale must be one of"},
		{"bad target", func(f *Formation) { f.Peers[0].Target = "pane" }, "target must be one of"},
		{"flag-shaped model", func(f *Formation) { f.Peers[0].Model = "--dangerous" }, "bad model"},
		{"bad model chars", func(f *Formation) { f.Peers[0].Model = "opus 4" }, "bad model"},
		{"no peers is fine", func(f *Formation) { f.Peers = nil; f.AnchorAlias = "" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := valid()
			tc.mut(f)
			err := f.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tc.want != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// TestFormationSaveRefusesInvalid: a file that cannot be loaded back is not worth
// writing, so Save validates first and leaves nothing behind.
func TestFormationSaveRefusesInvalid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	f := loadExample(t)
	f.Peers[0].Mode = "restore"
	if err := f.Save(); err == nil {
		t.Fatal("want save to refuse an invalid envelope")
	}
	if _, err := os.Stat(filepath.Join(dir, formationsDir)); err == nil {
		t.Error("a refused save must not create the formations dir")
	}
}

// TestFormationSaveAtomicLeavesNoTemp: the write is temp+rename; the temp must not
// survive, and must be invisible to a name-based lookup if it ever did.
func TestFormationSaveAtomicLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	f := loadExample(t)
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, formationsDir))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{"roles.json"}) {
		t.Errorf("want only roles.json, got %v", names)
	}
}

func TestFormationPath(t *testing.T) {
	t.Setenv("CBUS_DIR", "/tmp/bus")
	got, err := FormationPath("roles")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if want := "/tmp/bus/.formations/roles.json"; got != want {
		t.Errorf("path: got %q want %q", got, want)
	}
	for _, bad := range []string{"", "a/b", "..", ".", "has space", "a\x00b"} {
		if _, err := FormationPath(bad); err == nil {
			t.Errorf("want FormationPath(%q) to refuse", bad)
		}
	}
}

func TestLoadFormationErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	if _, err := LoadFormation("nope"); err == nil || !strings.Contains(err.Error(), "no formation") {
		t.Errorf("missing formation: want a no-formation error, got %v", err)
	}
	fdir := filepath.Join(dir, formationsDir)
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, "torn.json"), []byte(`{"schema":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFormation("torn"); err == nil {
		t.Error("torn json: want an error")
	}
	if err := os.WriteFile(filepath.Join(fdir, "old.json"), []byte(`{"schema":"cbus-workspace-snapshot/v3-draft","name":"old","channel":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFormation("old")
	if err == nil || !strings.Contains(err.Error(), "unsupported schema") {
		t.Errorf("v3-draft snapshot: want an unsupported-schema error, got %v", err)
	}
}

// TestFormationMinimalFileConverges: a sparse hand-authored file (only the
// required keys) loads, and its first save normalizes to the full shape and then
// stays put.
func TestFormationMinimalFileConverges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CBUS_DIR", dir)
	fdir := filepath.Join(dir, formationsDir)
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	minimal := `{"schema":"cbus-formation/v1","name":"min","channel":"min","peers":[{"alias":"solo"}]}`
	if err := os.WriteFile(filepath.Join(fdir, "min.json"), []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFormation("min")
	if err != nil {
		t.Fatalf("load minimal: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	b1, _ := os.ReadFile(filepath.Join(fdir, "min.json"))
	if !strings.Contains(string(b1), `"addresses": []`) || !strings.Contains(string(b1), `"host": null`) {
		t.Errorf("minimal file did not normalize to the full shape:\n%s", b1)
	}
	again, err := LoadFormation("min")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := again.Save(); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	b2, _ := os.ReadFile(filepath.Join(fdir, "min.json"))
	if string(b1) != string(b2) {
		t.Errorf("normalized file does not converge:\n%s\n---\n%s", b1, b2)
	}
}
