package client

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

func maxInboxRecord(t *testing.T) string {
	t.Helper()
	line := `{"text":"` + strings.Repeat("x", daemonMaxInboxLine-len(`{"text":""}`)) + "\"}\n"
	if _, err := readDaemonLine(bufio.NewReader(strings.NewReader(line))); err != nil || len(line) != daemonMaxInboxRecord {
		t.Fatalf("fixture is not the largest supported record: %d bytes, %v", len(line), err)
	}
	return line
}

func recordFile(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "inbox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestInboxAnchorAcceptsLargestRecordAfterAnother(t *testing.T) {
	prefix, line := "{}\n", maxInboxRecord(t)
	f := recordFile(t, prefix+line)
	if !inboxRecordMatches(f, int64(len(prefix)+len(line)), fmt.Sprintf("%x", sha256.Sum256([]byte(line))), 0) {
		t.Fatal("largest supported record at a nonzero start was refused")
	}
}

func TestInboxRecordRefusesOversizedPendingRecord(t *testing.T) {
	prefix, line := "{}\n", "x"+maxInboxRecord(t)
	f := recordFile(t, prefix+line)
	if inboxRecordMatches(f, int64(len(prefix)+len(line)), fmt.Sprintf("%x", sha256.Sum256([]byte(line))), int64(len(prefix))) {
		t.Fatal("a record longer than any inbox line was matched")
	}
}
