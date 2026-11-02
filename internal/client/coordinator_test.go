package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var namedCoordinatorRe = regexp.MustCompile(`Your coordinator is ([^ ,]+),`)

func rawCoordinatorRecord(t *testing.T, ch, alias string) (coordinatorRecord, bool) {
	t.Helper()
	b, err := os.ReadFile(coordinatorRecordPath(ch, alias))
	if errors.Is(err, os.ErrNotExist) {
		return coordinatorRecord{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var rec coordinatorRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec, true
}

func lastPrompt(argv []string) string { return argv[len(argv)-1] }

// TestEveryDoorRecordsTheCoordinatorItsPromptNames: the record is compared with the
// prompt the door actually handed the forker, not with an expected constant alone.
func TestEveryDoorRecordsTheCoordinatorItsPromptNames(t *testing.T) {
	orchestrator := func(p *FormationPeer) { p.Rolefile = "orchestrator" }
	role := func(p *FormationPeer) { p.Role = strptr("You implement things.") }
	launch := func(t *testing.T, f *Formation, alias string, action PeerAction) string {
		t.Helper()
		for i := range f.Peers {
			if f.Peers[i].Alias == alias {
				fk := &recForker{}
				if _, err := launchPeer(f, PeerPlan{Peer: &f.Peers[i], Action: action}, "ch/applier", "cbus-ok-x-1", "", fk, "", false); err != nil {
					t.Fatal(err)
				}
				return lastPrompt(fk.specs[0].Argv)
			}
		}
		t.Fatalf("no peer %s", alias)
		return ""
	}
	spawn := func(t *testing.T, addr, name, roleName string) string {
		t.Helper()
		fk := &fakeForker{}
		if _, _, err := Spawn("window", addr, "", name, roleName, fk); err != nil {
			t.Fatal(err)
		}
		return lastPrompt(fk.spec.Argv)
	}
	for _, tc := range []struct {
		name       string
		ch, alias  string
		coord, sid string
		run        func(t *testing.T) string
	}{
		{name: "apply template, applier coordinates", ch: "ch", alias: "coder", coord: "ch/applier", run: func(t *testing.T) string {
			return launch(t, applyFixture(peer("coder", role)), "coder", ActionTemplate)
		}},
		{name: "apply fork, applier coordinates", ch: "ch", alias: "coder", coord: "ch/applier", run: func(t *testing.T) string {
			return launch(t, applyFixture(peer("coder", role)), "coder", ActionFork)
		}},
		{name: "apply resume binds the recorded session", ch: "ch", alias: "coder", coord: "ch/applier", sid: "sid-1", run: func(t *testing.T) string {
			return launch(t, applyFixture(peer("coder", func(p *FormationPeer) { p.SessionID = "sid-1"; role(p) })), "coder", ActionResume)
		}},
		{name: "apply, an orchestrator seat someone else applied coordinates", ch: "ch", alias: "coder", coord: "ch/lead", run: func(t *testing.T) string {
			return launch(t, applyFixture(peer("lead", orchestrator), peer("coder", role)), "coder", ActionTemplate)
		}},
		{name: "apply, the orchestrator seat itself has none", ch: "ch", alias: "lead", run: func(t *testing.T) string {
			return launch(t, applyFixture(peer("lead", orchestrator), peer("coder", role)), "lead", ActionTemplate)
		}},
		{name: "apply, two orchestrator seats leave the applier", ch: "ch", alias: "coder", coord: "ch/applier", run: func(t *testing.T) string {
			return launch(t, applyFixture(peer("a", orchestrator), peer("b", orchestrator), peer("coder", role)), "coder", ActionTemplate)
		}},
		{name: "formation bootstrap off the channel names the anchor", ch: "ch", alias: "coder", coord: "ch/orchestrator", run: func(t *testing.T) string {
			f := applyFixture(peer("coder", role))
			prompt, err := BootstrapPeer(f, "coder", "")
			if err != nil {
				t.Fatal(err)
			}
			if wrote, err := RecordBootstrapCoordinator(f, "coder"); err != nil || !wrote {
				t.Fatalf("wrote=%v err=%v", wrote, err)
			}
			return prompt
		}},
		{name: "formation bootstrap, the orchestrator seat itself has none", ch: "ch", alias: "lead", run: func(t *testing.T) string {
			f := applyFixture(peer("lead", orchestrator))
			prompt, err := BootstrapPeer(f, "lead", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RecordBootstrapCoordinator(f, "lead"); err != nil {
				t.Fatal(err)
			}
			return prompt
		}},
		{name: "spawn --role, joined spawner", ch: "dev", alias: "worker", coord: "dev/lead", run: func(t *testing.T) string {
			plantPeer(t, "dev", "lead", "sid-spawner")
			return spawn(t, "dev", "worker", "documenter")
		}},
		{name: "spawn --role, spawner off the channel", ch: "dev", alias: "worker", run: func(t *testing.T) string {
			return spawn(t, "dev", "worker", "documenter")
		}},
		{name: "spawn --role orchestrator", ch: "dev", alias: "worker", run: func(t *testing.T) string {
			plantPeer(t, "dev", "lead", "sid-spawner")
			return spawn(t, "dev", "worker", "orchestrator")
		}},
		{name: "spawn with no role", ch: "dev", alias: "worker", run: func(t *testing.T) string {
			plantPeer(t, "dev", "lead", "sid-spawner")
			return spawn(t, "dev", "worker", "")
		}},
		{name: "branch names the parent", ch: "dev", alias: "kid", coord: "dev/lead", run: func(t *testing.T) string {
			plantPeer(t, "dev", "lead", "sid-spawner")
			fk := &fakeForker{}
			if _, _, _, err := Branch("window", "dev", "", "kid", fk); err != nil {
				t.Fatal(err)
			}
			return lastPrompt(fk.spec.Argv)
		}},
		{name: "anchor resume has none", ch: "dd", alias: "orchestrator", run: func(t *testing.T) string {
			fk := &recForker{}
			if _, _, err := resumeAnchorWorld(resumeFixture(), "", fk, resumeWorld()); err != nil {
				t.Fatal(err)
			}
			return lastPrompt(fk.specs[0].Argv)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CBUS_DIR", root)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
			t.Chdir(t.TempDir())
			writeRoleFile(t, root, "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
			writeRoleFile(t, root, "orchestrator", "# orchestrator\nMODEL: opus\n\n## Mission\nrun it.\n")
			// a stale record at the name: a launch naming nobody must clear it
			if _, err := recordCoordinator(tc.ch, tc.alias, "stale/seat", "apply", "sid-old"); err != nil {
				t.Fatal(err)
			}
			prompt := tc.run(t)
			named := ""
			if m := namedCoordinatorRe.FindStringSubmatch(prompt); m != nil {
				named = m[1]
			}
			if named != tc.coord {
				t.Fatalf("prompt names %q, want %q:\n%s", named, tc.coord, prompt)
			}
			rec, ok := rawCoordinatorRecord(t, tc.ch, tc.alias)
			if named == "" {
				if ok {
					t.Fatalf("no coordinator named, but a record exists: %+v", rec)
				}
				return
			}
			if !ok || rec.Coordinator != named {
				t.Fatalf("record %+v (present=%v) disagrees with the prompt's %q", rec, ok, named)
			}
			if rec.SessionID != tc.sid {
				t.Fatalf("sessionId = %q, want %q", rec.SessionID, tc.sid)
			}
		})
	}
}

func TestLowLevelBootstrapIsPrintOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	if got := BootstrapPromptAliased("dev", "lead", "kid"); !namedCoordinatorRe.MatchString(got) {
		t.Fatalf("prompt: %s", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".coordinators")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rendering wrote to the store: %v", err)
	}
}

func TestApplyDryRunWritesNoRecord(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	applierOn(t, "ch", "applier")
	f := applyFixture(peer("coder", func(p *FormationPeer) { p.Role = strptr("You implement things.") }))
	if _, err := Apply(f, ApplyOptions{DryRun: true}, &recForker{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".coordinators")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run wrote to the store: %v", err)
	}
}

// spawnedChild launches ch/alias through spawn --role and leaves the record unanswered.
func spawnedChild(t *testing.T, ch, alias string) {
	t.Helper()
	root := CBUSDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
	t.Chdir(t.TempDir())
	writeRoleFile(t, root, "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
	plantPeer(t, ch, "lead", "sid-spawner")
	if _, _, err := Spawn("window", ch, "", alias, "documenter", &fakeForker{}); err != nil {
		t.Fatal(err)
	}
}

func sessionJoins(t *testing.T, sid, ch, alias string) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ID", sid)
	if _, _, err := Join(ch, alias); err != nil {
		t.Fatal(err)
	}
}

func wantCoordinator(t *testing.T, ch, alias, want string) {
	t.Helper()
	got, ok := CoordinatorOf(ch, alias)
	if got != want || ok != (want != "") {
		t.Fatalf("CoordinatorOf(%s/%s) = %q, %v; want %q", ch, alias, got, ok, want)
	}
}

func TestUnboundRecordReadsNoneUntilTheChildAnswers(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	spawnedChild(t, "dev", "kid")
	wantCoordinator(t, "dev", "kid", "")
	sessionJoins(t, "sid-kid", "dev", "kid")
	wantCoordinator(t, "dev", "kid", "dev/lead")
	if rec, _ := rawCoordinatorRecord(t, "dev", "kid"); rec.SessionID != "sid-kid" {
		t.Fatalf("not bound to the child: %+v", rec)
	}
}

// TestRecordOutlivesLeaveAndReadsNoneMeanwhile: nobody holds the name in the gap, so
// nobody is coordinated, but the file is what the returning session gets back.
func TestRecordOutlivesLeaveAndReadsNoneMeanwhile(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	spawnedChild(t, "dev", "kid")
	sessionJoins(t, "sid-kid", "dev", "kid")
	wantCoordinator(t, "dev", "kid", "dev/lead")
	if _, err := Leave("dev"); err != nil {
		t.Fatal(err)
	}
	wantCoordinator(t, "dev", "kid", "")
	if rec, ok := rawCoordinatorRecord(t, "dev", "kid"); !ok || rec.SessionID != "sid-kid" {
		t.Fatalf("the file must survive the gap: %+v present=%v", rec, ok)
	}
	sessionJoins(t, "sid-kid", "dev", "kid")
	wantCoordinator(t, "dev", "kid", "dev/lead")
}

func TestRecordOutlivesRealCollection(t *testing.T) {
	d, _, req := daemonFixture(t)
	if _, err := recordCoordinator(req.Channel, req.Alias, req.Channel+"/lead", coordinatorDoorSpawn, ""); err != nil {
		t.Fatal(err)
	}
	c := mustDaemonConnect(t, d, req)
	wantCoordinator(t, req.Channel, req.Alias, req.Channel+"/lead")
	c.State = "detached"
	c.Consumer = &consumerObservation{State: "online", PID: 4242, StartToken: "s"}
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	d.publish(c)
	pass, err := d.collectConnections(context.Background(), gcTestLimits, gcDaemonProbe(false))
	if err != nil || len(pass.Records) != 1 || !pass.Records[0].Collected {
		t.Fatalf("collect: %+v, %v", pass.Records, err)
	}
	if fileExists(filepath.Join(CBUSDir(), req.Channel, req.Alias, "meta.json")) {
		t.Fatal("precondition: collection removes the peer dir")
	}
	wantCoordinator(t, req.Channel, req.Alias, "")
	mustDaemonConnect(t, d, req)
	wantCoordinator(t, req.Channel, req.Alias, req.Channel+"/lead")
}

func TestAnotherSessionTakingTheAliasInheritsNothing(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	spawnedChild(t, "dev", "kid")
	sessionJoins(t, "sid-kid", "dev", "kid")
	if _, err := Leave("dev"); err != nil {
		t.Fatal(err)
	}
	sessionJoins(t, "sid-stranger", "dev", "kid")
	wantCoordinator(t, "dev", "kid", "")
	if _, ok := rawCoordinatorRecord(t, "dev", "kid"); ok {
		t.Fatal("the record must be gone, not just unread")
	}
}

// TestStaleRecordCannotSpeakForANewHolder: the reader's own check, for the case where
// settling the new holder failed and the file is still there.
func TestStaleRecordCannotSpeakForANewHolder(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	if _, err := recordCoordinator("dev", "kid", "dev/lead", coordinatorDoorApply, "sid-kid"); err != nil {
		t.Fatal(err)
	}
	plantPeer(t, "dev", "kid", "sid-kid")
	wantCoordinator(t, "dev", "kid", "dev/lead")
	plantPeer(t, "dev", "kid", "sid-stranger")
	wantCoordinator(t, "dev", "kid", "")
}

func TestUnreadableHolderReadsNone(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	if _, err := recordCoordinator("dev", "kid", "dev/lead", coordinatorDoorApply, "sid-kid"); err != nil {
		t.Fatal(err)
	}
	plantPeer(t, "dev", "kid", "sid-kid")
	if err := os.WriteFile(filepath.Join(CBUSDir(), "dev", "kid", "meta.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantCoordinator(t, "dev", "kid", "")
}

func TestOversizedRecordReadsNone(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	plantPeer(t, "dev", "kid", "sid-kid")
	path := coordinatorRecordPath("dev", "kid")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"coordinator":"dev/lead","sessionId":"sid-kid"}` + strings.Repeat(" ", coordinatorRecordMax)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	wantCoordinator(t, "dev", "kid", "")
}

func TestSettleWithNoSessionKeepsNothing(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	if _, err := recordCoordinator("dev", "kid", "dev/lead", coordinatorDoorApply, ""); err != nil {
		t.Fatal(err)
	}
	if err := settleCoordinatorRecord("dev", "kid", ""); err != nil {
		t.Fatal(err)
	}
	if rec, ok := rawCoordinatorRecord(t, "dev", "kid"); ok {
		t.Fatalf("%+v", rec)
	}
}

func TestRenameOntoANameClearsItsStaleRecord(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	sessionJoins(t, "sid-kid", "dev", "kid")
	if _, err := recordCoordinator("dev", "renamed", "dev/old", coordinatorDoorApply, "sid-kid"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Rename("renamed", "dev"); err != nil {
		t.Fatal(err)
	}
	if rec, ok := rawCoordinatorRecord(t, "dev", "renamed"); ok {
		t.Fatalf("this session never had that coordinator: %+v", rec)
	}
}

type answeringThenFailingForker struct {
	t   *testing.T
	sid string
}

func (f answeringThenFailingForker) Fork(ForkSpec) (string, error) {
	sessionJoins(f.t, f.sid, "dev", "kid")
	return "", errors.New("the terminal reported a failure after the window opened")
}

// TestLaunchErrorAfterTheChildAnsweredKeepsItsRecord: undo must not take a record from a
// seat that is up and bound.
func TestLaunchErrorAfterTheChildAnsweredKeepsItsRecord(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CBUS_DIR", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
	t.Chdir(t.TempDir())
	writeRoleFile(t, root, "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
	plantPeer(t, "dev", "lead", "sid-spawner")
	if _, _, err := Spawn("window", "dev", "", "kid", "documenter", answeringThenFailingForker{t, "sid-kid"}); err == nil {
		t.Fatal("want the fork error")
	}
	wantCoordinator(t, "dev", "kid", "dev/lead")
}

func TestUndoOfARemovalLeavesALaterWrite(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	if _, err := recordCoordinator("dev", "kid", "dev/old", coordinatorDoorApply, "sid-old"); err != nil {
		t.Fatal(err)
	}
	undo, err := recordCoordinator("dev", "kid", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recordCoordinator("dev", "kid", "dev/new", coordinatorDoorApply, "sid-new"); err != nil {
		t.Fatal(err)
	}
	undo()
	if rec, ok := rawCoordinatorRecord(t, "dev", "kid"); !ok || rec.Coordinator != "dev/new" {
		t.Fatalf("%+v present=%v", rec, ok)
	}
}

func TestLaunchThatNeverBootedExpiresBeforeBinding(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	spawnedChild(t, "dev", "kid")
	coordinatorNow = func() time.Time { return time.Now().Add(coordinatorBindWindow + time.Minute) }
	t.Cleanup(func() { coordinatorNow = time.Now })
	sessionJoins(t, "sid-stranger", "dev", "kid")
	wantCoordinator(t, "dev", "kid", "")
	if _, ok := rawCoordinatorRecord(t, "dev", "kid"); ok {
		t.Fatal("an expired launch must not stay on disk")
	}
}

func TestCorruptRecordReadsNone(t *testing.T) {
	for name, body := range map[string]string{
		"not json":    "{",
		"empty":       "",
		"no address":  `{"coordinator":"","sessionId":"sid-kid"}`,
		"remote":      `{"coordinator":"dev@server/lead","sessionId":"sid-kid"}`,
		"path shaped": `{"coordinator":"../x/lead","sessionId":"sid-kid"}`,
		"no slash":    `{"coordinator":"lead","sessionId":"sid-kid"}`,
		"wrong type":  `{"coordinator":7,"sessionId":"sid-kid"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CBUS_DIR", t.TempDir())
			path := coordinatorRecordPath("dev", "kid")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			plantPeer(t, "dev", "kid", "sid-kid")
			wantCoordinator(t, "dev", "kid", "")
			if err := os.RemoveAll(filepath.Join(CBUSDir(), "dev", "kid")); err != nil {
				t.Fatal(err)
			}
			sessionJoins(t, "sid-kid", "dev", "kid")
			wantCoordinator(t, "dev", "kid", "")
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("settling must clear a corrupt record: %v", err)
			}
		})
	}
}

// TestReaderRefusesBadNames: each name reaches a valid bound record if the path is built.
func TestReaderRefusesBadNames(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	for _, tc := range [][2]string{{"dev", "../kid"}, {".x", "kid"}, {"dev", ".kid"}} {
		path := coordinatorRecordPath(tc[0], tc[1])
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeCoordinatorRecord(path, coordinatorRecord{Coordinator: "dev/lead", SessionID: "sid-kid"}); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(CBUSDir(), tc[0], tc[1])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeMeta(dir, peerMeta{Alias: tc[1], Channel: tc[0], SessionID: "sid-kid", ListenerPid: jsonNull, OwnerPid: jsonNull}); err != nil {
			t.Fatal(err)
		}
		wantCoordinator(t, tc[0], tc[1], "")
	}
}

// TestBootstrappedSeatReadsNoneUntilItArrives: no reservation here, so nothing but the
// empty session stands between an unanswered record and the reader.
func TestBootstrappedSeatReadsNoneUntilItArrives(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-elsewhere")
	f := applyFixture(peer("coder", func(p *FormationPeer) { p.Role = strptr("You implement things.") }))
	if wrote, err := RecordBootstrapCoordinator(f, "coder"); err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	wantCoordinator(t, "ch", "coder", "")
	sessionJoins(t, "sid-coder", "ch", "coder")
	wantCoordinator(t, "ch", "coder", "ch/orchestrator")
}

func TestFailedLaunchLeavesNoRecord(t *testing.T) {
	boom := errors.New("no terminal")
	role := func(p *FormationPeer) { p.Role = strptr("You implement things.") }
	for name, run := range map[string]func() error{
		"spawn": func() error {
			_, _, err := Spawn("window", "dev", "", "kid", "documenter", &recForker{err: boom})
			return err
		},
		"branch": func() error {
			_, _, _, err := Branch("window", "dev", "", "kid", &recForker{err: boom})
			return err
		},
		"apply": func() error {
			f := applyFixture(peer("kid", role))
			f.Channel = "dev"
			_, err := launchPeer(f, PeerPlan{Peer: &f.Peers[0], Action: ActionTemplate}, "dev/lead", "cbus-ok-x-1", "", &recForker{err: boom}, "", false)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CBUS_DIR", root)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
			t.Chdir(t.TempDir())
			writeRoleFile(t, root, "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
			plantPeer(t, "dev", "lead", "sid-spawner")
			if err := run(); !errors.Is(err, boom) {
				t.Fatalf("err = %v", err)
			}
			if rec, ok := rawCoordinatorRecord(t, "dev", "kid"); ok {
				t.Fatalf("a launch that did not happen left %+v", rec)
			}
		})
	}
}

// TestUnwritableRecordStopsTheLaunch: a seat told it has a coordinator that nothing
// recorded would hold forever at a permit check, so the door refuses before any window.
func TestUnwritableRecordStopsTheLaunch(t *testing.T) {
	role := func(p *FormationPeer) { p.Role = strptr("You implement things.") }
	for name, run := range map[string]func(fk TerminalForker) error{
		"spawn": func(fk TerminalForker) error {
			_, _, err := Spawn("window", "dev", "", "kid", "documenter", fk)
			return err
		},
		"branch": func(fk TerminalForker) error {
			_, _, _, err := Branch("window", "dev", "", "kid", fk)
			return err
		},
		"apply": func(fk TerminalForker) error {
			f := applyFixture(peer("kid", role))
			f.Channel = "dev"
			_, err := launchPeer(f, PeerPlan{Peer: &f.Peers[0], Action: ActionTemplate}, "dev/lead", "cbus-ok-x-1", "", fk, "", false)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CBUS_DIR", root)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-spawner")
			t.Chdir(t.TempDir())
			writeRoleFile(t, root, "documenter", "# documenter\nMODEL: sonnet\n\n## Mission\nwrite entries.\n")
			plantPeer(t, "dev", "lead", "sid-spawner")
			if err := os.WriteFile(filepath.Join(root, ".coordinators"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			fk := &recForker{}
			if err := run(fk); err == nil {
				t.Fatal("want an error")
			}
			if len(fk.specs) != 0 {
				t.Fatal("a window opened for a seat with no record")
			}
			if fileExists(filepath.Join(root, "dev", "kid", "meta.json")) {
				t.Fatal("the reservation outlived the refused launch")
			}
		})
	}
}

// TestFailedResumeRestoresThePriorRecord: resume does not own the name, so a launch
// that failed must not cost the seat what it already had.
func TestFailedResumeRestoresThePriorRecord(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if _, err := recordCoordinator("ch", "coder", "ch/previous", coordinatorDoorApply, "sid-1"); err != nil {
		t.Fatal(err)
	}
	f := applyFixture(peer("coder", func(p *FormationPeer) { p.SessionID = "sid-1"; p.Role = strptr("You implement things.") }))
	if _, err := launchPeer(f, PeerPlan{Peer: &f.Peers[0], Action: ActionResume}, "ch/applier", "cbus-ok-x-1", "", &recForker{err: errors.New("no terminal")}, "", false); err == nil {
		t.Fatal("want the fork error")
	}
	if rec, ok := rawCoordinatorRecord(t, "ch", "coder"); !ok || rec.Coordinator != "ch/previous" {
		t.Fatalf("prior record not restored: %+v present=%v", rec, ok)
	}
}

func TestFailedAnchorResumeRestoresThePriorRecord(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	if _, err := recordCoordinator("dd", "orchestrator", "dd/previous", coordinatorDoorApply, "sid-anchor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resumeAnchorWorld(resumeFixture(), "", &recForker{err: errors.New("no terminal")}, resumeWorld()); err == nil {
		t.Fatal("want the fork error")
	}
	if rec, ok := rawCoordinatorRecord(t, "dd", "orchestrator"); !ok || rec.Coordinator != "dd/previous" {
		t.Fatalf("%+v present=%v", rec, ok)
	}
}

// TestBootstrapLeavesAHeldNameAlone: bootstrap is also how someone reads what a peer
// would be told, and doing that for a running seat must not unbind it.
func TestBootstrapLeavesAHeldNameAlone(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-elsewhere")
	if _, err := recordCoordinator("ch", "coder", "ch/lead", coordinatorDoorApply, "sid-coder"); err != nil {
		t.Fatal(err)
	}
	plantPeer(t, "ch", "coder", "sid-coder")
	f := applyFixture(peer("coder", func(p *FormationPeer) { p.Role = strptr("You implement things.") }))
	wrote, err := RecordBootstrapCoordinator(f, "coder")
	if err != nil || wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	wantCoordinator(t, "ch", "coder", "ch/lead")
}

func TestRecordFollowsALegacyRename(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	spawnedChild(t, "dev", "kid")
	sessionJoins(t, "sid-kid", "dev", "kid")
	if _, _, _, err := Rename("renamed", "dev"); err != nil {
		t.Fatal(err)
	}
	wantCoordinator(t, "dev", "renamed", "dev/lead")
	if _, ok := rawCoordinatorRecord(t, "dev", "kid"); ok {
		t.Fatal("the old name kept a record")
	}
}

func TestDaemonConnectSettlesTheRecord(t *testing.T) {
	d, q, req := daemonFixture(t)
	_ = q
	if _, err := recordCoordinator(req.Channel, req.Alias, req.Channel+"/lead", coordinatorDoorSpawn, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.connect(req); err != nil {
		t.Fatal(err)
	}
	wantCoordinator(t, req.Channel, req.Alias, req.Channel+"/lead")
	if rec, _ := rawCoordinatorRecord(t, req.Channel, req.Alias); rec.SessionID != req.ThreadID {
		t.Fatalf("connect did not bind the thread: %+v", rec)
	}
	// the same session reconnecting keeps it
	if _, err := d.connect(req); err != nil {
		t.Fatal(err)
	}
	wantCoordinator(t, req.Channel, req.Alias, req.Channel+"/lead")
}

func TestDaemonConnectDropsAnotherSessionsRecord(t *testing.T) {
	d, _, req := daemonFixture(t)
	if _, err := recordCoordinator(req.Channel, req.Alias, req.Channel+"/lead", coordinatorDoorApply, "sid-gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.connect(req); err != nil {
		t.Fatal(err)
	}
	wantCoordinator(t, req.Channel, req.Alias, "")
	if _, ok := rawCoordinatorRecord(t, req.Channel, req.Alias); ok {
		t.Fatal("record survived a different session taking the name")
	}
}

// TestRelayConnectLeavesTheLocalRecordAlone: a relay peer's channel/alias names a seat
// in another store, and it can share the spelling of a local seat.
func TestRelayConnectLeavesTheLocalRecordAlone(t *testing.T) {
	d, _, req := daemonFixture(t)
	q := &schedulerQueue{thread: req.ThreadID, entered: make(chan daemonQueueCall, 16), closed: make(chan struct{})}
	d.openQueue = func(CodexQueueConfig) (nativeQueue, error) { return q, nil }
	d.dialRelay = func(context.Context, *ConnectionState) (relaySocket, error) { return newRelayTestSocket(), nil }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := d.shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := recordCoordinator(req.Channel, req.Alias, req.Channel+"/lead", coordinatorDoorApply, "sid-local"); err != nil {
		t.Fatal(err)
	}
	req.Relay = &RelayConfig{Host: "test-relay", Base: "http://127.0.0.1:1234", CredentialDir: filepath.Join(req.Config.UserHome, "credentials")}
	if _, err := d.connect(req); err != nil {
		t.Fatal(err)
	}
	if rec, ok := rawCoordinatorRecord(t, req.Channel, req.Alias); !ok || rec.SessionID != "sid-local" {
		t.Fatalf("relay connect changed the local record: %+v present=%v", rec, ok)
	}
}
