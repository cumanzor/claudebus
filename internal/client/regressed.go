package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claudebus/internal/core"
)

const (
	regressedKind     = "regressed"
	regressedSubtype  = "model_refusal_fallback"
	regressedScanMax  = 4 << 20
	regressedLock     = 250 * time.Millisecond
	regressedFieldMax = 64
)

var regressedDeliver = deliverNoticeWithin

// regressionEntry is the undocumented transcript line a classifier fallback leaves. Its
// content and explanation are never decoded.
type regressionEntry struct {
	Type          string `json:"type"`
	Subtype       string `json:"subtype"`
	IsSidechain   bool   `json:"isSidechain"`
	SessionID     string `json:"sessionId"`
	OriginalModel string `json:"originalModel"`
	FallbackModel string `json:"fallbackModel"`
	Category      string `json:"apiRefusalCategory"`
	Timestamp     string `json:"timestamp"`
	// --fork-session copies the parent's rows under the fork's own sessionId
	ForkedFrom json.RawMessage `json:"forkedFrom"`
}

type regressionMark struct {
	SessionID string `json:"sessionId"`
	Offset    int64  `json:"offset"`
}

type regressionFound struct {
	entry      regressionEntry
	start, end int64
}

// HookRegressed runs the Stop hook. It fires on every session on the machine, so it does
// nothing for a session with nobody to tell, reads a bounded slice of the transcript per
// Stop, and gives up on a busy lock instead of holding the stop.
func HookRegressed(stdin io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return err
	}
	var in struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return fmt.Errorf("unreadable payload: %w", err)
	}
	if in.SessionID == "" || in.TranscriptPath == "" || filepath.Base(in.TranscriptPath) != in.SessionID+".jsonl" {
		return nil
	}
	restore := OverrideSessionID(in.SessionID)
	regs := ResolveSelf()
	restore()
	var errs []error
	for _, reg := range regs {
		to := regressionRecipient(reg)
		if to == "" {
			continue
		}
		if err := reportRegressions(reg, to, in.SessionID, in.TranscriptPath); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", reg.Channel, reg.Alias, err))
		}
	}
	return errors.Join(errs...)
}

// regressionRecipient is the seat's coordinator, or the seat itself when it coordinates
// others, or nobody: a plain session gets no extra turn for this.
func regressionRecipient(reg LocalReg) string {
	if coord, ok := CoordinatorOf(reg.Channel, reg.Alias); ok {
		return coord
	}
	self := reg.Channel + "/" + reg.Alias
	if coordinatesAnySeat(reg.Channel, self) {
		return self
	}
	return ""
}

func regressionMarkPath(ch, alias string) string {
	return filepath.Join(CBUSDir(), ".regressions", ch, alias+".json")
}

func reportRegressions(reg LocalReg, to, sid, transcript string) error {
	markPath := regressionMarkPath(reg.Channel, reg.Alias)
	offset := readRegressionMark(markPath, sid)
	found, next, err := scanRegressions(transcript, sid, offset)
	if err != nil {
		return err
	}
	ch, alias, ok := strings.Cut(to, "/")
	if !ok {
		return fmt.Errorf("bad recipient %q", to)
	}
	var sendErr error
	for _, f := range found {
		sendErr = regressedDeliver(ch, alias, core.Message{From: reg.Channel + "/" + reg.Alias, To: to, TS: Now(),
			Kind: regressedKind, Text: regressedText(reg.Alias, f.entry)}, regressedLock)
		if sendErr != nil {
			next = f.start // the next Stop tries this one again
			break
		}
	}
	if next == offset {
		return sendErr
	}
	return errors.Join(sendErr, writeRegressionMark(reg.Channel, reg.Alias, markPath, regressionMark{SessionID: sid, Offset: next}))
}

// moveRegressionMark follows a legacy rename, so the new name does not rescan and report
// again. The caller holds both peer locks.
func moveRegressionMark(ch, oldAlias, newAlias string) error {
	oldPath, newPath := regressionMarkPath(ch, oldAlias), regressionMarkPath(ch, newAlias)
	if err := os.Rename(oldPath, newPath); errors.Is(err, os.ErrNotExist) {
		return removeCoordinatorFile(newPath)
	} else {
		return err
	}
}

func readRegressionMark(path, sid string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var m regressionMark
	if json.Unmarshal(b, &m) != nil || m.SessionID != sid || m.Offset < 0 {
		return 0
	}
	return m.Offset
}

func writeRegressionMark(ch, alias, path string, m regressionMark) error {
	unlock, err := lockPeerFor(ch, alias, regressedLock)
	if err != nil {
		return err
	}
	defer unlock()
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeCoordinatorBytes(path, b)
}

// scanRegressions reads at most regressedScanMax from offset. next never lands inside a
// line that may still be growing; a full slice with no newline is one oversized line,
// which is skipped because a fallback entry is never that long.
func scanRegressions(path, sid string, offset int64) (found []regressionFound, next int64, err error) {
	f, err := openSharedRead(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}
	if offset > info.Size() {
		offset = 0
	}
	buf := make([]byte, min(info.Size()-offset, regressedScanMax))
	if _, err := f.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
		return nil, offset, err
	}
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 {
		if len(buf) == regressedScanMax {
			return nil, offset + regressedScanMax, nil
		}
		return nil, offset, nil
	}
	marker := []byte(`"` + regressedSubtype + `"`)
	pos := offset
	for _, line := range bytes.SplitAfter(buf[:last+1], []byte{'\n'}) {
		start := pos
		pos += int64(len(line))
		if !bytes.Contains(line, marker) {
			continue
		}
		var e regressionEntry
		if json.Unmarshal(line, &e) != nil || e.Type != "system" || e.Subtype != regressedSubtype || e.IsSidechain || e.SessionID != sid || regressionInherited(e) {
			continue
		}
		found = append(found, regressionFound{entry: e, start: start, end: pos})
	}
	return found, pos, nil
}

func regressionInherited(e regressionEntry) bool {
	return len(e.ForkedFrom) > 0 && string(e.ForkedFrom) != "null"
}

// regressedText dates the entry and leaves the present open: the mark starts at 0, so a
// seat resumed into a formation reports a fallback someone may already have cleared.
func regressedText(alias string, e regressionEntry) string {
	at := "an unknown time"
	if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
		at = t.UTC().Format(time.RFC3339)
	}
	to := regressedField(e.FallbackModel)
	return fmt.Sprintf("%s fell back from %s to %s at %s, [%s]; /model in that pane if it is still on %s",
		alias, regressedField(e.OriginalModel), to, at, regressedField(e.Category), to)
}

func regressedField(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 128 && (r == '.' || r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return r
		}
		return -1
	}, s)
	if len(s) > regressedFieldMax {
		s = s[:regressedFieldMax]
	}
	if s == "" {
		return "unknown"
	}
	return s
}
