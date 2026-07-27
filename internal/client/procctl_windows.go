package client

import (
	"os"
	"os/exec"
)

// setProcGroup is a no-op: windows has no process groups to lead, and the
// descendant-reaping job boundedCmd wants from one is done by WaitDelay instead.
func setProcGroup(cmd *exec.Cmd) {}

// killProcGroup terminates the DIRECT CHILD ONLY. This is a real semantic downgrade
// from the unix side, not a translation of it: there is no group-directed kill here,
// so a descendant that outlives its parent survives this and keeps whatever inherited
// pipes it holds until boundedCmd's WaitDelay abandons them. A job object is the
// primitive that would take the whole tree, and it stays out until a real wedge
// appears rather than being added speculatively.
func killProcGroup(p *os.Process) error {
	return p.Kill()
}
