package client

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"claudebus/internal/core"
)

// LocalSend appends a message to a local peer's inbox after the send gate.
//
// Send gate (bin/cbus:462-469): a never-armed peer (null listenerPid) is accepted
// unconditionally (its first arm replays the inbox); a live listener is accepted;
// a dead ex-listener is refused unless force (then queued best-effort, warn=true).
//
// from resolution when `from` is empty (bin/cbus:470-478): this session's
// registration in the TARGET channel, else its first registration anywhere, else
// $CBUS_ALIAS, else the unroutable <shorthost>-<ppid>.
//
// The message is rejected (never truncated) if it exceeds core.MaxMessageBytes.
func LocalSend(target, from string, force bool, text string) (resolved, fromOut string, warn bool, err error) {
	ch, al, err := ParseLocal(target)
	if err != nil {
		return "", "", false, err
	}
	if ch == "" {
		rc, ok := FindPeerChannel(al)
		if !ok {
			return "", "", false, fmt.Errorf("no peer %q in your channels — use <channel>/<alias> (cbus list)", al)
		}
		ch = rc
	}
	root := CBUSDir()
	metaPath := filepath.Join(root, ch, al, "meta.json")
	if !fileExists(metaPath) {
		return "", "", false, fmt.Errorf("no such peer %q (cbus list)", ch+"/"+al)
	}
	// gate: only refuse an armed-then-dead listener
	if m, _ := ReadPeerMeta(metaPath); m.ListenerPid != 0 && !MetaListenerAlive(metaPath) {
		if !force {
			return "", "", false, fmt.Errorf("%q is not listening; use --force to queue anyway", ch+"/"+al)
		}
		warn = true
	}
	if from == "" {
		var first string
		for _, reg := range ResolveSelf() {
			if first == "" {
				first = reg.Channel + "/" + reg.Alias
			}
			if reg.Channel == ch {
				first = reg.Channel + "/" + reg.Alias
				break
			}
		}
		from = first
	}
	if from == "" {
		if a := os.Getenv("CBUS_ALIAS"); a != "" {
			from = a
		} else {
			from = fmt.Sprintf("%s-%d", ShortHostname(), os.Getppid())
		}
	}
	line, err := json.Marshal(core.Message{From: from, To: ch + "/" + al, TS: Now(), Text: text})
	if err != nil {
		return "", "", false, err
	}
	if len(line) > core.MaxMessageBytes {
		return "", "", false, fmt.Errorf("message exceeds %dMiB", core.MaxMessageBytes>>20)
	}
	appendInbox(filepath.Join(root, ch, al, "inbox.jsonl"), line)
	return ch + "/" + al, from, warn, nil
}
