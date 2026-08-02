package main

import (
	"strings"
	"testing"
)

// refusedVerbs is every verb phase 1 excludes on windows, driven BOTH bare and with
// its arguments satisfied. The bare form is the discriminating one: the refusal has to
// beat argument validation, or a user gets walked through fixing a --sock they were
// never going to be allowed to use.
var refusedVerbs = []struct {
	verb string
	bare []string
	full []string
}{
	{"branch", []string{"branch"}, []string{"branch", "window", "ch", "--model", "opus"}},
	{"spawn", []string{"spawn"}, []string{"spawn", "window", "ch", "--role", "coder"}},
	{"formation apply", []string{"formation", "apply"}, []string{"formation", "apply", "dev-trio", "--dry-run"}},
	{"close", []string{"close"}, []string{"close", "ch/al", "--force"}},
	{"codex", []string{"codex"}, []string{"codex", "--channel", "ch", "--alias", "cx"}},
	{"codex-bridge", []string{"codex-bridge"}, []string{"codex-bridge", "ch/al", "--sock", `\\.\pipe\x`}},
	{"codex-stop-hook", []string{"codex-stop-hook"}, []string{"codex-stop-hook", "--wait", "1s"}},
}

func TestWindowsExcludedVerbsRefuseHonestly(t *testing.T) {
	for _, c := range refusedVerbs {
		for _, argv := range [][]string{c.bare, c.full} {
			var rc int
			out := captureStderr(t, func() { rc = run(argv) })
			if rc == 0 {
				t.Errorf("%v: exit 0, want non-zero (output %q)", argv, out)
			}
			// the verb, the platform and the phase have to be in the PRINTED string. A
			// phase that lives only in a doc comment is invisible to the user the
			// refusal exists for.
			for _, want := range []string{c.verb, "windows", "phase 1"} {
				if !strings.Contains(out, want) {
					t.Errorf("%v: refusal %q does not name %q", argv, out, want)
				}
			}
			// the unknown-command path also exits 1, so exit status alone cannot tell
			// "excluded here" from "you mistyped it".
			if strings.Contains(out, "unknown command") {
				t.Errorf("%v: fell through to the unknown-command path: %q", argv, out)
			}
			// a usage line means argument validation answered first, which is the
			// dead-end walkthrough D48 forbids.
			if strings.Contains(out, "usage:") {
				t.Errorf("%v: argument validation answered before the refusal: %q", argv, out)
			}
		}
	}
}

// The excluded set is bounded by what reaches the terminal forker or the codex
// app-server. Verbs that reach neither must survive on windows, and each of these
// stops at its own usage line without touching the store.
func TestWindowsLiveVerbsAreNotRefused(t *testing.T) {
	for _, argv := range [][]string{
		{"formation"},
		{"formation", "bootstrap"},
		{"formation", "show"},
		{"bootstrap"},
	} {
		out := captureStderr(t, func() { _ = run(argv) })
		if strings.Contains(out, "phase 1") {
			t.Errorf("%v: refused, but it drives no forker and no codex socket: %q", argv, out)
		}
	}
}

// The refusal a user reads comes from the dispatch guard, not from the library seam
// that M1 landed, so the two must not drift apart on the phase claim n4 requires.
func TestWindowsRefusalTemplateCoversEveryExcludedVerb(t *testing.T) {
	for _, c := range refusedVerbs {
		if phase1Refusal(c.verb) == "" {
			t.Errorf("%q has no refusal string", c.verb)
		}
	}
	if phase1Refusal("send") != "" {
		t.Error("send is a phase 1 verb and must not refuse")
	}
}
