package main

import "os/exec"

// detachProcess is a no-op: a windows child already outlives its parent, with no
// session to leave, so the update-check poll survives the primary command returning
// without any SysProcAttr at all. One residual delta against the unix side, accepted
// rather than fixed with CREATE_NEW_PROCESS_GROUP: the poll stays attached to the
// launching console, so a later Ctrl+C there reaches it. The cost is one skipped
// best-effort version check.
func detachProcess(cmd *exec.Cmd) {}
