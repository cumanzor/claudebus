//go:build darwin

package client

import "testing"

func TestCodexLsofAccessAndProcessBoundaries(t *testing.T) {
	b := []byte("p12\x00\nf3\x00ar\x00n/tmp/rollout.jsonl\x00\nf4\x00au\x00n/tmp/queue_1.sqlite\x00\np15\x00\nf8\x00aw\x00n/tmp/rollout.jsonl\x00\n")
	files := parseCodexLsof(b)
	if len(files) != 3 || files[0].PID != 12 || files[0].Access != "r" || files[1].Access != "u" || files[2].PID != 15 || files[2].Access != "w" {
		t.Fatalf("files=%+v", files)
	}
}
