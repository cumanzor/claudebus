package client

// ClosePeer refuses on windows rather than pretending. The verb is not a signal
// wrapped in a helper: it TERMs the owning session, waits out a grace, and then
// sweeps the terminal surface the peer occupied, which it locates by the controlling
// tty read before the signal. Windows has no controlling tty to locate a surface by
// and no tmux or iTerm2 to hand it to, so the sweep half has no meaning here and a
// signal-only close would leave exactly the orphaned surface the verb exists to
// collect.
//
// It reports Ok=false: a live peer that could not be ended is a failure, and a
// refusal is that. Callers that treat "already gone" as success must not read this
// as one.
//
// This is the library answer, not the one a CLI user sees: the verb refuses at
// runClose before target resolution, because reaching here needs a live peer to
// resolve and a host with none would report a missing peer instead of a missing verb.
func ClosePeer(ch, alias string, force bool) CloseReport {
	return CloseReport{
		ch + "/" + alias,
		false,
		"cbus close is not available on windows in phase 1: it locates the peer's terminal surface by controlling tty, and there is none to read",
	}
}
