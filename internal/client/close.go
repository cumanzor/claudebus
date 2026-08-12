package client

import (
	"context"
	"os/exec"
	"time"
)

// boundedWaitDelay is how long after the deadline-kill we still wait for inherited
// pipes to close before abandoning them. Short: by this point the kill has landed and
// anything still holding the pipe has escaped the process group.
const boundedWaitDelay = 200 * time.Millisecond

// boundedCmd is exec.CommandContext with a deadline that actually binds.
//
// CommandContext alone kills only the DIRECT child. A forked descendant inherits the
// stdout pipe, so Wait blocks reading it until that descendant exits on its own and
// the budget buys nothing. It surfaces per-platform because of the shell: darwin's
// /bin/sh execs the last command of a script (the direct child IS the long-running
// process, so the kill lands on it) while debian's dash forks it (verified in a
// bookworm container: direct child dash with a sleep child, and the sweep ran the
// full 30s).
//
// Setpgid plus a group-directed kill takes the descendants with it. WaitDelay is the
// backstop for anything that escapes the group with setsid(): it stops waiting and
// closes the inherited pipes rather than trusting the kill to have reached everyone.
// On windows there is no group to lead and WaitDelay is the whole mechanism (see
// killProcGroup).
func boundedCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	setProcGroup(cmd)
	cmd.Cancel = func() error { return killProcGroup(cmd.Process) }
	cmd.WaitDelay = boundedWaitDelay
	return cmd
}

// CloseReport is one target's outcome. Ok covers "closed" AND "already gone" —
// a teardown that finds nothing to tear down succeeded (scripted sweeps depend
// on that; only a live peer we could not end is a failure).
type CloseReport struct {
	Target string
	Ok     bool
	Detail string
}
