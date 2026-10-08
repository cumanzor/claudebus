package client

import "testing"

func TestTargetForSurface(t *testing.T) {
	for _, c := range []struct{ target, surface, want string }{
		{"tab", "tmux", "tmux"},
		{"window", "tmux", "tmux"},
		{"", "tmux", "tmux"},
		{"pane", "tmux", "pane"},
		{"tmux", "tmux", "tmux"},
		{"tmux", "iterm2", "tab"},
		{"window", "iterm2", "window"},
		{"pane", "iterm2", "pane"},
		{"tab", "", "tab"},
		{"tmux", "", "tmux"},
	} {
		if got := targetForSurface(c.target, c.surface); got != c.want {
			t.Errorf("targetForSurface(%q, %q) = %q, want %q", c.target, c.surface, got, c.want)
		}
	}
}

func TestObservedSurfaceTmuxWinsOverITerm(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("ITERM_SESSION_ID", "w0t0p0:DEAD-BEEF")
	if got := observedSurface(); got != "tmux" {
		t.Errorf("inside tmux = %q, want tmux", got)
	}
	t.Setenv("TMUX", "")
	if got := observedSurface(); got != "iterm2" {
		t.Errorf("iTerm2 only = %q, want iterm2", got)
	}
	t.Setenv("ITERM_SESSION_ID", "")
	if got := observedSurface(); got != "" {
		t.Errorf("neither = %q, want empty", got)
	}
}

// The saver's own peer follows its terminal; every other peer keeps what the file
// says, since save cannot see their terminals.
func TestSaveFormationRetargetsOnlyTheSaver(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sid-orch")
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("ITERM_SESSION_ID", "w0t0p0:DEAD-BEEF")
	plantPeer(t, "roles", "coder", "sid-coder")
	plantPeer(t, "roles", "orchestrator", "sid-orch")

	f, rep, err := SaveFormation("roles", "roles", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f.Peers {
		want := "tab"
		if p.Alias == "orchestrator" {
			want = "tmux"
		}
		if p.Target != want {
			t.Errorf("%s target = %q, want %q", p.Alias, p.Target, want)
		}
	}
	if len(rep.Retargeted) != 1 || rep.Retargeted[0] != "orchestrator: tab -> tmux" {
		t.Errorf("Retargeted = %q", rep.Retargeted)
	}
	back, err := LoadFormation("roles")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range back.Peers {
		if p.Alias == "orchestrator" && p.Target != "tmux" {
			t.Errorf("on disk orchestrator target = %q, want tmux", p.Target)
		}
	}

	// the same seat back in a plain iTerm2 tab: the tmux record no longer holds
	t.Setenv("TMUX", "")
	f2, rep2, err := SaveFormation("roles", "roles", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f2.Peers {
		if p.Alias == "orchestrator" && p.Target != "tab" {
			t.Errorf("after an iTerm2 save orchestrator target = %q, want tab", p.Target)
		}
	}
	if len(rep2.Retargeted) != 1 || rep2.Retargeted[0] != "orchestrator: tmux -> tab" {
		t.Errorf("Retargeted = %q", rep2.Retargeted)
	}
}

func TestResumeAnchorTargetOverride(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	f := resumeFixture()
	f.RetargetAnchor("tmux")
	fk := &recForker{ids: []string{"surface-1"}}
	if _, _, err := resumeAnchorWorld(f, "", fk, resumeWorld()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(fk.specs) != 1 || fk.specs[0].Target != "tmux" {
		t.Fatalf("specs = %+v, want one launch on tmux", fk.specs)
	}
	for _, p := range f.Peers {
		if p.Alias != f.AnchorAlias && p.Target == "tmux" {
			t.Errorf("override leaked onto non-anchor %s", p.Alias)
		}
	}
}

func TestValidForkTarget(t *testing.T) {
	for _, ok := range []string{"window", "tab", "pane", "tmux"} {
		if !ValidForkTarget(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "iterm2", "TAB", "-x"} {
		if ValidForkTarget(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
