package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// presenceMsg is a presence event line (bin/cbus:341-343). Field order
// from,to,ts,kind,event,text matches the bash json.dumps insertion order; the
// bytes are canonical-Go (compact) — the follower decodes then reframes, and the
// P0 cross-parse proved emit() frames Go-marshaled and python-marshaled lines
// identically (D8), so a canonical-Go line is delivered byte-identically.
type presenceMsg struct {
	From  string `json:"from"`
	To    string `json:"to"`
	TS    string `json:"ts"`
	Kind  string `json:"kind"`
	Event string `json:"event"`
	Text  string `json:"text"`
}

// BroadcastPresence appends a presence event to every non-dead peer in ch except
// skip (bin/cbus:332-348): the SAME !PeerDead recipient rule as the send gate, so
// a joined-but-unarmed peer still receives it (replayed on first arm). One shared
// ts per broadcast; a vanishing target is skipped, not fatal.
func BroadcastPresence(ch, from, event, text, skip string) {
	chDir := filepath.Join(CBUSDir(), ch)
	entries, err := os.ReadDir(chDir)
	if err != nil {
		return
	}
	ts := Now()
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		peer := e.Name()
		if peer == skip {
			continue
		}
		metaPath := filepath.Join(chDir, peer, "meta.json")
		if !fileExists(metaPath) || PeerDead(metaPath) {
			continue
		}
		b, err := json.Marshal(presenceMsg{
			From: ch + "/" + from, To: ch + "/" + peer, TS: ts,
			Kind: "presence", Event: event, Text: text,
		})
		if err != nil {
			continue
		}
		appendInbox(filepath.Join(chDir, peer, "inbox.jsonl"), b)
	}
}

// appendInbox atomically appends one line as a single O_APPEND write — concurrent
// appenders interleave line-atomically. A vanished target dir is skipped (bash
// `>> … 2>/dev/null || continue`).
func appendInbox(path string, line []byte) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(append([]byte{}, line...), '\n'))
}

// fileExists / dirExists are lightweight stat helpers used across the store.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
