package main

import (
	"fmt"
	"os"

	"claudebus/internal/client"
)

// warnIfSessionless prints one stderr line when the harness gave us no session id.
//
// Sessionless operation is a supported MODE, not an error (port-map D7): joins record
// sessionId "" and sends never fail on identity, so a shell with no CLAUDE_CODE_SESSION_ID
// can still drive the bus. What it silently loses is SELF-RESOLUTION — `cbus list` cannot
// tell which peer is you, `leave`/`rename` cannot find your registration, and a send's
// from-address falls down the chain to an unroutable host-pid pair, so replies die with
// "no such peer".
//
// D7 ruled that both halves ship: keep the mode AND say so once. Only the verbs that
// record or resolve identity call this — a read-only `list` or `inbox` has nothing to
// warn about, and a warning printed on every invocation is a warning nobody reads.
//
// stderr, never stdout: `cbus inbox` is consumed by scripts and the follower's stdout is
// a frame stream. A warning on stdout would corrupt both.
func warnIfSessionless() {
	if client.SessionID() != "" {
		return
	}
	fmt.Fprintln(os.Stderr,
		"cbus: no CLAUDE_CODE_SESSION_ID — running sessionless; this session cannot be "+
			"resolved by list/leave/rename and replies to it may be unroutable")
}
