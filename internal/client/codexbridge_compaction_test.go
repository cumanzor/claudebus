package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBridgeCompactionCursorExactThreadPartialTailAndResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const meta = "{\"type\":\"session_meta\",\"payload\":{\"id\":\"T\"}}\n"
	const completed = "{\"type\":\"compacted\",\"payload\":{\"summary\":\"PRIVATE\"}}\n"
	write(meta + completed)
	cursor, count, err := bridgeCompactionStep(path, "T", nil)
	if err != nil || count != 0 {
		t.Fatalf("historical baseline: %d %v", count, err)
	}
	write(meta + completed + completed[:len(completed)-1])
	next, count, err := bridgeCompactionStep(path, "T", cursor)
	if err != nil || count != 0 || next.Offset != cursor.Offset {
		t.Fatalf("partial tail: %+v %d %v", next, count, err)
	}
	write(meta + completed + completed)
	cursor, count, err = bridgeCompactionStep(path, "T", cursor)
	if err != nil || count != 1 {
		t.Fatalf("completion: %d %v", count, err)
	}
	_, count, err = bridgeCompactionStep(path, "T", cursor)
	if err != nil || count != 0 {
		t.Fatalf("repeat: %d %v", count, err)
	}
	_, count, err = bridgeCompactionStep(path, "T", nil)
	if err != nil || count != 0 {
		t.Fatalf("new wrapper replayed history: %d %v", count, err)
	}
	if _, _, err := bridgeCompactionStep(path, "OTHER", cursor); err == nil {
		t.Fatal("accepted another thread")
	}
	write(meta)
	_, count, err = bridgeCompactionStep(path, "T", cursor)
	if err != nil || count != 0 {
		t.Fatalf("truncation baseline: %d %v", count, err)
	}
}
