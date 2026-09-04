package main

import (
	"strings"
	"testing"
)

// TestUsageAdvertisesLayoutVerbs: the help text is where a user learns these exist,
// and the spec grammar is not guessable — an arrange that is documented as taking
// "<spec>" and nothing else is unusable. The worked example is the part that carries
// the meaning of '|' and '/'.
func TestUsageAdvertisesLayoutVerbs(t *testing.T) {
	out := captureStdout(t, func() { run([]string{"--help"}) })
	for _, want := range []string{
		"cbus arrange",
		"cbus scatter",
		"cbus focus",
		"orchestrator | (coder / reviewer)", // the grammar by example
		"alias:30%",                         // sizing is otherwise invisible
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help must mention %q:\n%s", want, out)
		}
	}
}

// TestParseLayoutArgsFlagsAfterPositional is why these two verbs parse their own
// argv instead of using splitVerbArgs: that scanner stops at the first positional,
// so a trailing --dry-run would be taken as the spec (arrange) or the channel
// (scatter) and the command would run for real.
func TestParseLayoutArgsFlagsAfterPositional(t *testing.T) {
	spec, ch, dry, rc := parseLayoutArgs([]string{"a | b", "--dry-run"}, arrangeUsage, true)
	if rc != -1 {
		t.Fatalf("rc = %d, want -1 (continue)", rc)
	}
	if spec != "a | b" || ch != "" || !dry {
		t.Errorf("got spec=%q channel=%q dry=%v, want spec=%q channel=%q dry=true", spec, ch, dry, "a | b", "")
	}
}

// TestParseLayoutArgsChannelForms pins both spellings of --channel, and scatter's
// bare positional meaning the channel rather than a spec.
func TestParseLayoutArgsChannelForms(t *testing.T) {
	if _, ch, _, rc := parseLayoutArgs([]string{"a | b", "--channel", "dev"}, arrangeUsage, true); rc != -1 || ch != "dev" {
		t.Errorf("--channel dev: rc=%d channel=%q, want -1 / dev", rc, ch)
	}
	if _, ch, _, rc := parseLayoutArgs([]string{"a | b", "--channel=dev"}, arrangeUsage, true); rc != -1 || ch != "dev" {
		t.Errorf("--channel=dev: rc=%d channel=%q, want -1 / dev", rc, ch)
	}
	if _, ch, _, rc := parseLayoutArgs([]string{"dev"}, scatterUsage, false); rc != -1 || ch != "dev" {
		t.Errorf("scatter positional: rc=%d channel=%q, want -1 / dev", rc, ch)
	}
}

// TestParseLayoutArgsRejections: neither verb has free text to protect, so anything
// unrecognised must die rather than be quietly dropped — a typo'd --dry-runn that
// parsed as "no flag" would rearrange live windows the user meant only to preview.
func TestParseLayoutArgsRejections(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown flag":       {"a | b", "--dry-runn"},
		"two positionals":    {"a | b", "extra"},
		"missing spec":       {"--dry-run"},
		"--channel no value": {"a | b", "--channel"},
	} {
		out := captureStderr(t, func() {
			if _, _, _, rc := parseLayoutArgs(args, arrangeUsage, true); rc != 1 {
				t.Errorf("%s: rc = %d, want 1", name, rc)
			}
		})
		if !strings.HasPrefix(out, "cbus: ") {
			t.Errorf("%s: want a cbus: diagnostic, got %q", name, out)
		}
	}
	out := captureStderr(t, func() {
		if _, _, _, rc := parseLayoutArgs([]string{"one", "--channel", "two"}, scatterUsage, false); rc != 1 {
			t.Errorf("channel twice: rc = %d, want 1", rc)
		}
	})
	if !strings.Contains(out, "twice") {
		t.Errorf("channel given two ways should say so, got %q", out)
	}
}
