package main

// phase1Refusal returns why verb cannot run on this build, or "" when it can. Phase 1
// on windows is intercom and relay: everything that drives a terminal surface or the
// codex app-server is excluded, and every excluded verb answers here.
//
// The excluded verbs stay REGISTERED in dispatch and route to this rather than
// disappearing under a build tag. A missing case label falls to the unknown-command
// path, which also exits 1 — so a check reading only the exit status cannot tell "not
// supported on this platform yet" from "you mistyped it", and the user on windows is
// told the wrong one.
//
// Each string names the verb, the platform and the phase LITERALLY, because the only
// refusal a user reads is the printed one. A phase recorded in a doc comment above the
// function satisfies a reviewer and nobody standing at the terminal.
func phase1Refusal(verb string) string {
	const forkPhase = "is not available on windows in phase 1 (terminal forking lands in phase 2): "
	switch verb {
	case "branch":
		return "branch " + forkPhase +
			"it places the forked child in an iTerm2 window or a tmux pane, and neither backend exists here"
	case "spawn":
		return "spawn " + forkPhase +
			"it launches the fresh session into an iTerm2 window or a tmux pane, and neither backend exists here"
	case "formation apply":
		return "formation apply " + forkPhase +
			"it launches every peer through that same forker. --dry-run is refused with it, because a launch plan " +
			"for a host that can start no peer is a promise rather than a preview. formation save, list, show, rm " +
			"and bootstrap do work here"
	case "close":
		return "close is not available on windows in phase 1: it locates the peer's terminal surface by " +
			"controlling tty, and there is none to read"
	case "codex":
		return "codex is not available on windows in phase 1: the wrapper rendezvouses with the codex app-server " +
			"over a unix domain socket and tears it down by process group"
	case "codex-bridge":
		return "codex-bridge is not available on windows in phase 1: it follows the codex app-server over the " +
			"same unix domain socket the wrapper dials"
	case "codex-stop-hook":
		return "codex-stop-hook is not available on windows in phase 1: it is the delivery fallback for a codex " +
			"exec worker, and the whole codex subsystem is excluded from this build"
	}
	return ""
}
