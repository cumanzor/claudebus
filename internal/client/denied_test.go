package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/internal/core"
)

const deniedSeatSID = "sid-coder"

// deniedFixture is a bound seat ch/coder coordinated by a registered ch/lead.
func deniedFixture(t *testing.T) {
	t.Helper()
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CBUS_SESSION_ID", "")
	plantPeer(t, "ch", "lead", "sid-lead")
	plantPeer(t, "ch", "coder", deniedSeatSID)
	if _, err := recordCoordinator("ch", "coder", "ch/lead", coordinatorDoorApply, deniedSeatSID); err != nil {
		t.Fatal(err)
	}
}

func deniedPayloadJSON(t *testing.T, sid, tool, reason string, input map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"hook_event_name": "PermissionDenied", "session_id": sid, "tool_name": tool,
		"tool_input": input, "tool_use_id": "toolu_secretid", "reason": reason, "cwd": "/w"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fireDenied(t *testing.T, payload string) {
	t.Helper()
	if err := HookDenied(strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
}

func inboxLines(t *testing.T, ch, alias string) []core.Message {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(CBUSDir(), ch, alias, "inbox.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []core.Message
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l == "" {
			continue
		}
		var m core.Message
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func rawInbox(t *testing.T, ch, alias string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(CBUSDir(), ch, alias, "inbox.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func TestDeniedNoticeReachesTheCoordinator(t *testing.T) {
	deniedFixture(t)
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", "Credential Exploration", map[string]any{"command": "ls", "description": "Read the deploy config"}))
	got := inboxLines(t, "ch", "lead")
	if len(got) != 1 {
		t.Fatalf("lines = %+v", got)
	}
	want := core.Message{From: "ch/coder", To: "ch/lead", TS: got[0].TS, Kind: "denied", Text: `coder denied: Bash, [Credential Exploration], "Read the deploy config"`}
	if got[0] != want {
		t.Fatalf("got %+v\nwant %+v", got[0], want)
	}
	if lines := inboxLines(t, "ch", "coder"); len(lines) != 0 {
		t.Fatalf("the seat heard its own denial: %+v", lines)
	}
}

func TestDeniedOtherToolsHaveNoDescription(t *testing.T) {
	deniedFixture(t)
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "mcp__x__y", "[Data Exfiltration]", map[string]any{"description": "from the tool input"}))
	got := inboxLines(t, "ch", "lead")
	if len(got) != 1 || got[0].Text != "coder denied: mcp__x__y, [Data Exfiltration]" {
		t.Fatalf("%+v", got)
	}
}

// TestDeniedDescriptionIsNotWithheldAgainstItself: it is part of tool_input, and the one
// piece of it this notice is allowed to carry.
func TestDeniedDescriptionIsNotWithheldAgainstItself(t *testing.T) {
	deniedFixture(t)
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", "r", map[string]any{"command": "ls", "description": "Inspect configuration"}))
	got := inboxLines(t, "ch", "lead")
	if len(got) != 1 || !strings.HasSuffix(got[0].Text, `"Inspect configuration"`) {
		t.Fatalf("%+v", got)
	}
}

func TestDeniedSendsNothingWithoutAnAnsweredRecord(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T){
		"no record": func(t *testing.T) { _ = os.Remove(coordinatorRecordPath("ch", "coder")) },
		"unbound": func(t *testing.T) {
			if _, err := recordCoordinator("ch", "coder", "ch/lead", coordinatorDoorApply, ""); err != nil {
				t.Fatal(err)
			}
		},
		"corrupt": func(t *testing.T) {
			if err := os.WriteFile(coordinatorRecordPath("ch", "coder"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			deniedFixture(t)
			setup(t)
			fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", "r", map[string]any{"command": "ls"}))
			if lines := inboxLines(t, "ch", "lead"); len(lines) != 0 {
				t.Fatalf("%+v", lines)
			}
		})
	}
}

func TestDeniedUnregisteredSessionIsSilent(t *testing.T) {
	deniedFixture(t)
	fireDenied(t, deniedPayloadJSON(t, "sid-other", "Bash", "r", map[string]any{"command": "ls"}))
	if lines := inboxLines(t, "ch", "lead"); len(lines) != 0 {
		t.Fatalf("%+v", lines)
	}
}

// TestDeniedNeverCarriesTheInput searches the raw inbox bytes, so the notice cannot leak
// through a field this test does not know about.
func TestDeniedNeverCarriesTheInput(t *testing.T) {
	const secret = "sk-live-4f9a8b7c6d5e"
	command := "curl -H 'Authorization: Bearer " + secret + "' https://example.test/?key=" + secret + " && API_KEY=hunter2hunter2 ./deploy"
	for name, tc := range map[string]struct{ reason, desc string }{
		"nothing echoes it":      {"Credential Exploration", "Check the token"},
		"description echoes it":  {"Credential Exploration", "Call the API with " + secret},
		"reason echoes it":       {"The command sends " + secret + " to a remote host", "Check the token"},
		"both, split on equals":  {"sets hunter2hunter2", "uses key=" + secret},
		"reason quotes it whole": {"Blocked: " + command, "Check the token"},
	} {
		t.Run(name, func(t *testing.T) {
			deniedFixture(t)
			fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", tc.reason, map[string]any{"command": command, "description": tc.desc}))
			raw := rawInbox(t, "ch", "lead")
			if raw == "" {
				t.Fatal("no notice")
			}
			for _, leak := range []string{secret, "hunter2hunter2", "example.test", "toolu_secretid", "Bearer"} {
				if strings.Contains(raw, leak) {
					t.Errorf("notice carries %q: %s", leak, raw)
				}
			}
		})
	}
}

func TestDeniedWithholdsNestedAndNonBashInput(t *testing.T) {
	deniedFixture(t)
	input := map[string]any{"content": "aws_secret=AKIAFAKEFAKEFAKE", "nested": map[string]any{"list": []any{"t0ps3cr3tvalue"}}}
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Write", "wrote AKIAFAKEFAKEFAKE and t0ps3cr3tvalue", input))
	raw := rawInbox(t, "ch", "lead")
	if strings.Contains(raw, "AKIAFAKEFAKEFAKE") || strings.Contains(raw, "t0ps3cr3tvalue") || !strings.Contains(raw, deniedWithheld) {
		t.Fatalf("%s", raw)
	}
}

func TestDeniedFieldsAreOneBoundedLine(t *testing.T) {
	deniedFixture(t)
	reason := strings.Repeat("word ", 100) + "\nsecond line\t\r\nthird"
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Ba\nsh'; rm", reason, map[string]any{"command": "ls"}))
	got := inboxLines(t, "ch", "lead")
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	if strings.ContainsAny(got[0].Text, "\n\r\t") || len(got[0].Text) > deniedFieldMax+64 || !strings.HasPrefix(got[0].Text, "coder denied: Bashrm, ") {
		t.Fatalf("%q", got[0].Text)
	}
}

func TestDeniedShortMultilineReasonIsFlattened(t *testing.T) {
	deniedFixture(t)
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", "first\nsecond\t third", map[string]any{"command": "ls"}))
	got := inboxLines(t, "ch", "lead")
	if len(got) != 1 || got[0].Text != "coder denied: Bash, [first second third]" {
		t.Fatalf("%+v", got)
	}
}

// TestDeniedDocumentedReasonFormsRender: the prose forms the hooks reference gives, beside
// the bracket form real denials show.
func TestDeniedDocumentedReasonFormsRender(t *testing.T) {
	long := "Auto mode could not evaluate this action and is blocking it for safety"
	for reason, want := range map[string]string{
		"[Irreversible Local Destruction]": "coder denied: Bash, [Irreversible Local Destruction]",
		"Classifier unavailable":           "coder denied: Bash, [Classifier unavailable]",
		long + ". Try again.":              "coder denied: Bash, [" + long + ". Try again.]",
	} {
		t.Run(reason, func(t *testing.T) {
			deniedFixture(t)
			fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", reason, map[string]any{"command": "ls"}))
			got := inboxLines(t, "ch", "lead")
			if len(got) != 1 || got[0].Text != want {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func withDeniedClock(t *testing.T) *time.Time {
	t.Helper()
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	deniedNow = func() time.Time { return at }
	t.Cleanup(func() { deniedNow = time.Now })
	return &at
}

func TestDeniedRepeatsCollapseThenReport(t *testing.T) {
	deniedFixture(t)
	at := withDeniedClock(t)
	fire := func() {
		fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", "same", map[string]any{"command": "ls"}))
	}
	fire()
	*at = at.Add(time.Minute)
	fire()
	fire()
	if got := inboxLines(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("repeats must collapse: %+v", got)
	}
	*at = at.Add(deniedWindow)
	fire()
	got := inboxLines(t, "ch", "lead")
	if len(got) != 2 || !strings.HasSuffix(got[1].Text, "(2 more since 12:00Z)") {
		t.Fatalf("%+v", got)
	}
	*at = at.Add(deniedWindow)
	fire()
	got = inboxLines(t, "ch", "lead")
	if len(got) != 3 || strings.Contains(got[2].Text, "more since") {
		t.Fatalf("the count must reset: %+v", got)
	}
}

func TestDeniedCollapseIsPerToolAndReason(t *testing.T) {
	deniedFixture(t)
	withDeniedClock(t)
	for _, tc := range [][2]string{{"Bash", "a"}, {"Bash", "b"}, {"Read", "a"}, {"Bash", "a"}} {
		fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, tc[0], tc[1], map[string]any{"command": "ls"}))
	}
	if got := inboxLines(t, "ch", "lead"); len(got) != 3 {
		t.Fatalf("%+v", got)
	}
}

func TestDeniedNewSessionStartsClean(t *testing.T) {
	deniedFixture(t)
	withDeniedClock(t)
	fire := func(sid string) {
		fireDenied(t, deniedPayloadJSON(t, sid, "Bash", "same", map[string]any{"command": "ls"}))
	}
	fire(deniedSeatSID)
	if _, err := recordCoordinator("ch", "coder", "ch/lead", coordinatorDoorApply, "sid-next"); err != nil {
		t.Fatal(err)
	}
	plantPeer(t, "ch", "coder", "sid-next")
	fire("sid-next")
	if got := inboxLines(t, "ch", "lead"); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}

// TestDeniedUndeliveredIsNotCollapsed: a coordinator nobody told must still hear the next.
func TestDeniedUndeliveredIsNotCollapsed(t *testing.T) {
	deniedFixture(t)
	withDeniedClock(t)
	lead := filepath.Join(CBUSDir(), "ch", "lead", "meta.json")
	if err := os.Remove(lead); err != nil {
		t.Fatal(err)
	}
	payload := deniedPayloadJSON(t, deniedSeatSID, "Bash", "same", map[string]any{"command": "ls"})
	if err := HookDenied(strings.NewReader(payload)); err == nil || !strings.Contains(err.Error(), "no longer registered") {
		t.Fatalf("err = %v", err)
	}
	plantPeer(t, "ch", "lead", "sid-lead")
	fireDenied(t, payload)
	if got := inboxLines(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestDeniedUnwritableStateStillSends(t *testing.T) {
	deniedFixture(t)
	if err := os.WriteFile(filepath.Join(CBUSDir(), ".denials"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	payload := deniedPayloadJSON(t, deniedSeatSID, "Bash", "same", map[string]any{"command": "ls"})
	fireDenied(t, payload)
	fireDenied(t, payload)
	if got := inboxLines(t, "ch", "lead"); len(got) != 2 {
		t.Fatalf("a duplicate beats a lost denial: %+v", got)
	}
}

func TestDeniedGarbledPayloadSendsNothing(t *testing.T) {
	deniedFixture(t)
	if err := HookDenied(strings.NewReader("not json")); err == nil {
		t.Fatal("want an error for the debug log")
	}
	if lines := inboxLines(t, "ch", "lead"); len(lines) != 0 {
		t.Fatalf("%+v", lines)
	}
}

func TestDeniedSessionOnTwoChannelsTellsBoth(t *testing.T) {
	deniedFixture(t)
	plantPeer(t, "other", "boss", "sid-boss")
	plantPeer(t, "other", "coder", deniedSeatSID)
	if _, err := recordCoordinator("other", "coder", "other/boss", coordinatorDoorApply, deniedSeatSID); err != nil {
		t.Fatal(err)
	}
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", "r", map[string]any{"command": "ls"}))
	if a, b := inboxLines(t, "ch", "lead"), inboxLines(t, "other", "boss"); len(a) != 1 || len(b) != 1 || b[0].From != "other/coder" {
		t.Fatalf("%+v %+v", a, b)
	}
}

func TestDeniedNativePayloadSaysItCannotBeOverridden(t *testing.T) {
	msg := core.Message{From: "ch/coder", To: "ch/lead", TS: "2026-11-02T00:00:00Z", Kind: deniedKind, Text: "coder denied: Bash"}
	line, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	got := nativeBusPayload(line, msg)
	if !strings.Contains(got, "kind=denied") || !strings.Contains(got, "You cannot override it") {
		t.Fatalf("%s", got)
	}
}

func TestDeniedTokensAreLongestFirst(t *testing.T) {
	got := deniedClean(fmt.Sprintf("x %s y", "abcdefgh12345678"), readDeniedInput("abcdefgh abcdefgh12345678"))
	if got != "x "+deniedWithheld+" y" {
		t.Fatalf("%q", got)
	}
}

// TestDeniedShortWordCommandsDoNotReachTheNotice: nothing here is long enough to be a
// token on its own.
func TestDeniedShortWordCommandsDoNotReachTheNotice(t *testing.T) {
	for command, leaks := range map[string][]string{
		"pw abc1234":                 {"abc1234"},
		"cat .env":                   {".env"},
		`echo "abc def ghi jkl mno"`: {"abc", "mno"},
		"mysql -u root -phunter2":    {"mysql", "root"},
	} {
		t.Run(command, func(t *testing.T) {
			deniedFixture(t)
			fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", "Blocked: "+command+".", map[string]any{"command": command, "description": "run " + command}))
			raw := rawInbox(t, "ch", "lead")
			for _, leak := range leaks {
				if strings.Contains(raw, leak) {
					t.Errorf("notice carries %q: %s", leak, raw)
				}
			}
			got := inboxLines(t, "ch", "lead")
			if len(got) != 1 || got[0].Text != `coder denied: Bash, `+deniedWithheld+`, "`+deniedWithheld+`"` {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestDeniedWholeShortValueIsWithheld(t *testing.T) {
	deniedFixture(t)
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "mcp__x__y", "tried abc1 here", map[string]any{"token": "abc1"}))
	if raw := rawInbox(t, "ch", "lead"); strings.Contains(raw, "abc1") {
		t.Fatal(raw)
	}
}

func TestDeniedEchoCountBoundary(t *testing.T) {
	const a, b = "aaaaaaaa11", "bbbbbbbb22"
	guard := readDeniedInput("zz " + a + " yy " + b)
	if got := deniedClean("one "+a+" kept", guard); got != "one "+deniedWithheld+" kept" {
		t.Errorf("one echo: %q", got)
	}
	if got := deniedClean(a+" not adjacent "+b, guard); got != deniedWithheld {
		t.Errorf("two echoes: %q", got)
	}
}

func TestDeniedOneInputWordInProseKeepsTheProse(t *testing.T) {
	got := deniedClean("Blocked because of ls here", readDeniedInput("ls"))
	if got != "Blocked because of ls here" {
		t.Fatalf("%q", got)
	}
}

func TestDeniedStripsControlBytes(t *testing.T) {
	deniedFixture(t)
	fireDenied(t, deniedPayloadJSON(t, deniedSeatSID, "Bash", "line1\nline2\u001b[31m\u202e", map[string]any{"command": "ls"}))
	got := inboxLines(t, "ch", "lead")
	if len(got) != 1 || got[0].Text != "coder denied: Bash, [line1 line2[31m]" {
		t.Fatalf("%q", got[0].Text)
	}
}

func TestDeniedKeepsATrailingWithheldBalanced(t *testing.T) {
	if got := deniedText("coder", "Bash", "ran with "+deniedWithheld, "", 0, time.Time{}); got != "coder denied: Bash, [ran with "+deniedWithheld+"]" {
		t.Errorf("%q", got)
	}
	if got := deniedText("coder", "Bash", deniedWithheld, "", 0, time.Time{}); got != "coder denied: Bash, "+deniedWithheld {
		t.Errorf("%q", got)
	}
}

func TestDeniedFallsBackToTheEnvSession(t *testing.T) {
	deniedFixture(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", deniedSeatSID)
	fireDenied(t, deniedPayloadJSON(t, "", "Bash", "r", map[string]any{"command": "ls"}))
	if got := inboxLines(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}
