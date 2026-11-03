package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claudebus/internal/core"
)

const (
	regressedCanaryContent = "CANARY-content-of-the-refused-request"
	regressedCanaryExplain = "CANARY-explanation"
)

// regressedLine carries the key set measured from real entries, with placeholder values.
func regressedLine(t *testing.T, sid string, edit func(map[string]any)) string {
	t.Helper()
	m := map[string]any{
		"parentUuid": "u0", "isSidechain": false, "type": "system", "subtype": regressedSubtype,
		"content": regressedCanaryContent, "level": "warning", "trigger": "refusal", "direction": "retry", "scope": "session",
		"originalModel": "claude-fable-5-1", "fallbackModel": "claude-opus-4-8", "requestId": "req_CANARY",
		"apiRefusalCategory": "cyber", "apiRefusalExplanation": regressedCanaryExplain,
		"retractedMessageUuids": []string{"uCANARY"}, "refusedUserMessageUuid": "uCANARY", "isMeta": false, "uuid": "uCANARY",
		"timestamp": "2026-11-02T00:00:00Z", "userType": "external", "entrypoint": "cli", "cwd": "/w", "sessionId": sid,
		"version": "2.1.273", "gitBranch": "main",
	}
	if edit != nil {
		edit(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func seatTranscript(t *testing.T, sid, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), sid+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendTranscript(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

func stopPayload(t *testing.T, sid, path string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"hook_event_name": "Stop", "session_id": sid, "transcript_path": path, "stop_hook_active": false, "cwd": "/w"})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fireStop(t *testing.T, sid, path string) {
	t.Helper()
	if err := HookRegressed(strings.NewReader(stopPayload(t, sid, path))); err != nil {
		t.Fatal(err)
	}
}

func regressedNotices(t *testing.T, ch, alias string) []core.Message {
	t.Helper()
	var out []core.Message
	for _, m := range inboxLines(t, ch, alias) {
		if m.Kind == regressedKind {
			out = append(out, m)
		}
	}
	return out
}

func TestRegressedNoticeReachesTheCoordinatorOnce(t *testing.T) {
	deniedFixture(t)
	path := seatTranscript(t, deniedSeatSID, `{"type":"user"}`+"\n"+regressedLine(t, deniedSeatSID, nil)+`{"type":"assistant"}`+"\n")
	fireStop(t, deniedSeatSID, path)
	fireStop(t, deniedSeatSID, path)
	got := inboxLines(t, "ch", "lead")
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	want := core.Message{From: "ch/coder", To: "ch/lead", TS: got[0].TS, Kind: "regressed",
		Text: "coder fell back from claude-fable-5-1 to claude-opus-4-8 at 2026-11-02T00:00:00Z, [cyber]; /model in that pane if it is still on claude-opus-4-8"}
	if got[0] != want {
		t.Fatalf("got %+v", got[0])
	}
	appendTranscript(t, path, regressedLine(t, deniedSeatSID, nil))
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 2 {
		t.Fatalf("a second fallback is a second notice: %+v", got)
	}
}

func TestRegressedCarriesOnlyTheThreeFields(t *testing.T) {
	deniedFixture(t)
	fireStop(t, deniedSeatSID, seatTranscript(t, deniedSeatSID, regressedLine(t, deniedSeatSID, func(m map[string]any) {
		m["originalModel"] = "a b\n<script>"
		m["fallbackModel"] = strings.Repeat("x", 500)
		delete(m, "apiRefusalCategory")
	})))
	raw := rawInbox(t, "ch", "lead")
	if strings.Contains(raw, "CANARY") {
		t.Fatalf("%s", raw)
	}
	got := regressedNotices(t, "ch", "lead")
	if len(got) != 1 || !strings.HasPrefix(got[0].Text, "coder fell back from abscript to "+strings.Repeat("x", regressedFieldMax)+" at 2026-11-02T00:00:00Z, [unknown];") {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedIgnoresWhatIsNotThisSessionsFallback(t *testing.T) {
	for name, line := range map[string]func(t *testing.T) string{
		"a fork's inherited entry, under the fork's own id": func(t *testing.T) string {
			return regressedLine(t, deniedSeatSID, func(m map[string]any) {
				m["forkedFrom"] = map[string]any{"sessionId": "sid-parent", "messageUuid": "u0"}
			})
		},
		"another session's entry": func(t *testing.T) string {
			return regressedLine(t, "sid-parent", nil)
		},
		"a sidechain": func(t *testing.T) string {
			return regressedLine(t, deniedSeatSID, func(m map[string]any) { m["isSidechain"] = true })
		},
		"another type": func(t *testing.T) string {
			return regressedLine(t, deniedSeatSID, func(m map[string]any) { m["type"] = "user" })
		},
		"another subtype quoting this one": func(t *testing.T) string {
			return regressedLine(t, deniedSeatSID, func(m map[string]any) { m["subtype"] = "other"; m["note"] = regressedSubtype })
		},
		"a garbled line": func(t *testing.T) string { return `{"subtype":"` + regressedSubtype + `",` + "\n" },
	} {
		t.Run(name, func(t *testing.T) {
			deniedFixture(t)
			fireStop(t, deniedSeatSID, seatTranscript(t, deniedSeatSID, line(t)))
			if got := inboxLines(t, "ch", "lead"); len(got) != 0 {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestRegressedRefusesAMismatchedTranscriptPath(t *testing.T) {
	deniedFixture(t)
	fireStop(t, deniedSeatSID, seatTranscript(t, "another-session", regressedLine(t, deniedSeatSID, nil)))
	if got := inboxLines(t, "ch", "lead"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedATornTailWaitsForItsNewline(t *testing.T) {
	deniedFixture(t)
	line := regressedLine(t, deniedSeatSID, nil)
	path := seatTranscript(t, deniedSeatSID, line[:len(line)-1])
	fireStop(t, deniedSeatSID, path)
	if got := inboxLines(t, "ch", "lead"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	appendTranscript(t, path, "\n")
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedPlainSessionIsNotReadOrTold(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	t.Setenv("CBUS_SESSION_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	plantPeer(t, "ch", "solo", "sid-solo")
	if _, err := recordCoordinator("ch", "other", "ch/boss", coordinatorDoorApply, ""); err != nil {
		t.Fatal(err)
	}
	fireStop(t, "sid-solo", seatTranscript(t, "sid-solo", regressedLine(t, "sid-solo", nil)))
	if got := inboxLines(t, "ch", "solo"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(regressionMarkPath("ch", "solo")); !os.IsNotExist(err) {
		t.Fatalf("a plain session must not grow a mark: %v", err)
	}
}

func TestRegressedUnregisteredSessionIsSilent(t *testing.T) {
	deniedFixture(t)
	fireStop(t, "sid-other", seatTranscript(t, "sid-other", regressedLine(t, "sid-other", nil)))
	if got := inboxLines(t, "ch", "lead"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedCoordinatorNotifiesItself(t *testing.T) {
	deniedFixture(t)
	fireStop(t, "sid-lead", seatTranscript(t, "sid-lead", regressedLine(t, "sid-lead", nil)))
	got := regressedNotices(t, "ch", "lead")
	if len(got) != 1 || got[0].From != "ch/lead" || got[0].To != "ch/lead" {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedUndeliveredRetriesOnTheNextStop(t *testing.T) {
	deniedFixture(t)
	path := seatTranscript(t, deniedSeatSID, regressedLine(t, deniedSeatSID, nil)+regressedLine(t, deniedSeatSID, nil))
	if err := os.Remove(filepath.Join(CBUSDir(), "ch", "lead", "meta.json")); err != nil {
		t.Fatal(err)
	}
	if err := HookRegressed(strings.NewReader(stopPayload(t, deniedSeatSID, path))); err == nil {
		t.Fatal("want the delivery error for the debug log")
	}
	plantPeer(t, "ch", "lead", "sid-lead")
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}

// TestRegressedPartialDeliveryKeepsItsPlace: the first of two landed and the second did
// not, so only the second goes again.
func TestRegressedPartialDeliveryKeepsItsPlace(t *testing.T) {
	deniedFixture(t)
	line := regressedLine(t, deniedSeatSID, nil)
	path := seatTranscript(t, deniedSeatSID, line+line)
	sends := 0
	regressedDeliver = func(ch, alias string, msg core.Message, wait time.Duration) error {
		if sends++; sends == 2 {
			return errors.New("second send fails")
		}
		return deliverNoticeWithin(ch, alias, msg, wait)
	}
	t.Cleanup(func() { regressedDeliver = deliverNoticeWithin })
	if err := HookRegressed(strings.NewReader(stopPayload(t, deniedSeatSID, path))); err == nil {
		t.Fatal("want the delivery error")
	}
	if got := readRegressionMark(regressionMarkPath("ch", "coder"), deniedSeatSID); got != int64(len(line)) {
		t.Fatalf("offset = %d, want the start of the second entry %d", got, len(line))
	}
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedUndatedEntrySaysSo(t *testing.T) {
	deniedFixture(t)
	fireStop(t, deniedSeatSID, seatTranscript(t, deniedSeatSID, regressedLine(t, deniedSeatSID, func(m map[string]any) { m["timestamp"] = "yesterday; rm" })))
	got := regressedNotices(t, "ch", "lead")
	if len(got) != 1 || !strings.Contains(got[0].Text, " at an unknown time, ") || strings.Contains(got[0].Text, "yesterday") {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedRenameDoesNotReportAgain(t *testing.T) {
	deniedFixture(t)
	path := seatTranscript(t, deniedSeatSID, regressedLine(t, deniedSeatSID, nil))
	fireStop(t, deniedSeatSID, path)
	t.Setenv("CLAUDE_CODE_SESSION_ID", deniedSeatSID)
	if _, _, _, err := Rename("renamed", "ch"); err != nil {
		t.Fatal(err)
	}
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(regressionMarkPath("ch", "coder")); !os.IsNotExist(err) {
		t.Fatalf("the old name kept a mark: %v", err)
	}
}

// TestRegressedRecipientScanIsBounded: past the cap the answer is nobody.
func TestRegressedRecipientScanIsBounded(t *testing.T) {
	deniedFixture(t)
	for i := 0; i < regressedPeersMax; i++ {
		if _, err := recordCoordinator("ch", fmt.Sprintf("a%03d", i), "ch/boss", coordinatorDoorApply, ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := regressionRecipient(LocalReg{Channel: "ch", Alias: "lead"}); got != "" {
		t.Fatalf("the naming record sorts past the cap, got %q", got)
	}
	if err := os.Remove(coordinatorRecordPath("ch", "a000")); err != nil {
		t.Fatal(err)
	}
	if got := regressionRecipient(LocalReg{Channel: "ch", Alias: "lead"}); got != "ch/lead" {
		t.Fatalf("got %q", got)
	}
}

func TestRegressedAnotherSessionsMarkDoesNotApply(t *testing.T) {
	deniedFixture(t)
	line := regressedLine(t, deniedSeatSID, nil)
	path := seatTranscript(t, deniedSeatSID, line)
	if err := writeRegressionMark("ch", "coder", regressionMarkPath("ch", "coder"), regressionMark{SessionID: "sid-old", Offset: int64(len(line))}); err != nil {
		t.Fatal(err)
	}
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedOffsetPastTheEndRestarts(t *testing.T) {
	deniedFixture(t)
	path := seatTranscript(t, deniedSeatSID, regressedLine(t, deniedSeatSID, nil))
	if err := writeRegressionMark("ch", "coder", regressionMarkPath("ch", "coder"), regressionMark{SessionID: deniedSeatSID, Offset: 1 << 40}); err != nil {
		t.Fatal(err)
	}
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

// TestRegressedLargeBacklogIsScannedInBoundedSlices: nothing past the cap is read in one
// Stop, and the entry past it still arrives.
func TestRegressedLargeBacklogIsScannedInBoundedSlices(t *testing.T) {
	deniedFixture(t)
	filler := strings.Repeat(`{"type":"assistant","text":"`+strings.Repeat("x", 1000)+`"}`+"\n", (regressedScanMax/1024)*5/2)
	path := seatTranscript(t, deniedSeatSID, filler+regressedLine(t, deniedSeatSID, nil))
	fireStop(t, deniedSeatSID, path)
	first := readRegressionMark(regressionMarkPath("ch", "coder"), deniedSeatSID)
	if first <= 0 || first > regressedScanMax {
		t.Fatalf("first Stop consumed %d, cap %d", first, regressedScanMax)
	}
	if got := regressedNotices(t, "ch", "lead"); len(got) != 0 {
		t.Fatalf("the entry is past the cap: %+v", got)
	}
	stops := 1
	for ; stops < 5 && len(regressedNotices(t, "ch", "lead")) == 0; stops++ {
		fireStop(t, deniedSeatSID, path)
	}
	if got := regressedNotices(t, "ch", "lead"); len(got) != 1 || stops != 3 {
		t.Fatalf("stops=%d notices=%+v", stops, got)
	}
}

func TestRegressedOversizedLineIsSkippedNotStuck(t *testing.T) {
	deniedFixture(t)
	path := seatTranscript(t, deniedSeatSID, strings.Repeat("x", regressedScanMax+100)+"\n"+regressedLine(t, deniedSeatSID, nil))
	fireStop(t, deniedSeatSID, path)
	if got := readRegressionMark(regressionMarkPath("ch", "coder"), deniedSeatSID); got != regressedScanMax {
		t.Fatalf("offset = %d", got)
	}
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

// TestRegressedBusyLockDoesNotHoldTheStop: the hook runs on every session, so a held lock
// costs its short wait and the entry waits for the next Stop.
func TestRegressedBusyLockDoesNotHoldTheStop(t *testing.T) {
	deniedFixture(t)
	path := seatTranscript(t, deniedSeatSID, regressedLine(t, deniedSeatSID, nil))
	unlock, err := lockPeer("ch", "lead")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = HookRegressed(strings.NewReader(stopPayload(t, deniedSeatSID, path)))
	elapsed := time.Since(start)
	unlock()
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err = %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the Stop waited %s", elapsed)
	}
	fireStop(t, deniedSeatSID, path)
	if got := regressedNotices(t, "ch", "lead"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestRegressedNativePayloadAsksForOneAsk(t *testing.T) {
	msg := core.Message{From: "ch/coder", To: "ch/lead", TS: "2026-11-02T00:00:00Z", Kind: regressedKind, Text: "coder fell back from a to b"}
	line, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	got := nativeBusPayload(line, msg)
	if !strings.Contains(got, "kind=regressed") || !strings.Contains(got, "File one ask") {
		t.Fatalf("%s", got)
	}
}
