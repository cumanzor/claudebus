package client

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// joinOrigin keeps a fork-born session fork when it joins under an alias it did not
// reserve: "joined" would let a later save default it to resume
func joinOrigin(origin, sid string) string {
	if origin == OriginJoined && bornForkElsewhere(sid) {
		return OriginFork
	}
	return origin
}

// bornForkElsewhere reports whether any meta or ledger on this machine records sid as
// fork-born. The ledger outlives a pruned meta; only self-recorded join/resume lines count.
func bornForkElsewhere(sid string) bool {
	if sid == "" || sid == "reserved" {
		return false
	}
	root := CBUSDir()
	if chans, err := os.ReadDir(root); err == nil {
		for _, ch := range chans {
			if !ch.IsDir() || strings.HasPrefix(ch.Name(), ".") {
				continue
			}
			peers, err := os.ReadDir(filepath.Join(root, ch.Name()))
			if err != nil {
				continue
			}
			for _, p := range peers {
				if !p.IsDir() || strings.HasPrefix(p.Name(), ".") {
					continue
				}
				m, ok := ReadPeerMeta(filepath.Join(root, ch.Name(), p.Name(), "meta.json"))
				if ok && m.SessionID == sid && m.Origin == OriginFork {
					return true
				}
			}
		}
	}
	files, _ := filepath.Glob(filepath.Join(ledgerRoot(), "*.jsonl"))
	needle := []byte(sid)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil || !bytes.Contains(b, needle) {
			continue
		}
		for _, line := range bytes.Split(b, []byte("\n")) {
			if !bytes.Contains(line, needle) {
				continue
			}
			var ev LedgerEvent
			if json.Unmarshal(line, &ev) != nil || ev.SessionID != sid || ev.Origin != OriginFork {
				continue
			}
			if ev.Event == LedgerJoin || ev.Event == LedgerResume {
				return true
			}
		}
	}
	return false
}
