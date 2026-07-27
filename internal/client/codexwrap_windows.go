package client

import "errors"

// RunCodexWrap refuses on windows rather than degrading. The wrapper is the largest
// unix-bound surface in the client: it rendezvouses with the app-server over a unix
// domain socket, puts that server in its own process group, and tears the group down
// with a group-directed signal. None of those three exist here, and a version built
// without them would not be `cbus codex` with a caveat — it would be a different
// program wearing the same verb.
//
// Phase 1 carries no codex peers on this platform, so the refusal costs nothing that
// is currently reachable.
func RunCodexWrap(channel, alias string, passthrough []string) error {
	return errors.New("cbus codex is not available on windows: the wrapper rendezvouses with the codex app-server over a unix domain socket and tears it down by process group")
}
