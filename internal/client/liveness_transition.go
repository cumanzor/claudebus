package client

import (
	"path/filepath"
	"strings"
)

// TRANSITION(P3T2) — argv-grep listener identity, READ SIDE ONLY.
//
// This file is the entire surviving remnant of the argv-fingerprint liveness the P3
// structural-identity work replaced (former COMPAT(P3 #1/#2), port-map D1). It is
// fenced here so the sweep `grep -rn 'COMPAT(P3' internal/ cmd/` stays empty and the
// one thing still to delete is one file.
//
// WHY IT SURVIVES AT ALL. A follower armed by a PRE-P3 binary recorded a listenerPid
// but no listenerStart, and its `--inbox <path>` argv is the only ground truth about
// it. Deleting this read path in the same release that stops writing argv identity
// would make every already-running listener read dead the moment a machine upgraded:
// every live peer reaped, every session forced to re-arm mid-work. The no-stranding
// posture that justified the compat layer through cutover applies to this last step
// too (R3).
//
// WRITE SIDE IS ALREADY GONE. The current binary never puts an inbox path in a
// follower's argv, so nothing here describes how new peers are judged. This code only
// ever reads metas written before the upgrade.
//
// ENTERS: the first release after v0.3.0 (the last release that wrote argv identity).
// REMOVAL TRIGGER: the release AFTER that one — i.e. one full release of dual
// support, by which point no pre-P3 binary can still be arming a tail anywhere in the
// fleet. On removal, an armed meta with no listenerStart simply reads dead, which is
// the same posture R1 already takes for a stampless meta.
// TRACKED: cbus-8k9.4 (P3 structural liveness); the removal is recorded on the task
// alongside the register/peers drop, and rides that same dual-support release.
//
// DO NOT extend this file. Anything new about listener identity belongs in
// liveness.go on the structural branch.

// transitionArgvIdentity reports whether pid's argv still references THIS peer's
// inbox — the pre-P3 way of asking "is this process my follower". A read error
// (ESRCH/EPERM, or a darwin zombie whose args EINVAL out of KERN_PROCARGS2) returns
// false, so the clause reads DEAD with no invented leniency, matching `ps -o args=`
// going empty or "<defunct>" (Decision 1 condition iii, edge D1).
func transitionArgvIdentity(pid int, metaPath string) bool {
	return argvContains(pid, metaInboxNeedle(metaPath))
}

// argvContains reports whether pid's argv contains needle, read via the platform
// procArgs (sysctl KERN_PROCARGS2 on darwin, /proc/<pid>/cmdline on linux — no ps
// spawn).
func argvContains(pid int, needle string) bool {
	args, err := procArgs(pid)
	if err != nil {
		return false
	}
	return strings.Contains(args, needle)
}

// metaInboxNeedle is the inbox path argvContains greps for. It MUST stay the RAW bash
// inbox_path() spelling — dir + "/" + ch + "/" + al + "/inbox.jsonl", no
// filepath.Clean — because that is byte-for-byte what a pre-P3 follower put in its
// argv. A cleaned filepath.Join needle would miss a live pre-P3 follower under a
// trailing-slash CBUS_DIR (the F1 hazard, now running in reverse: the writer is gone
// but its output is still on disk and in argv).
//
// Rebuilt from the RAW CBUS_DIR plus the peer's subpath, recovered by stripping the
// cleaned CBUS_DIR prefix off the cleaned meta dir, NOT from the already-cleaned
// metaPath — so the CBUS_DIR spelling is preserved. Handles legacy v1
// ($CBUS_DIR/<ch>/meta.json) and v2 ($CBUS_DIR/<ch>/<al>/meta.json) alike.
func metaInboxNeedle(metaPath string) string {
	dir := filepath.Dir(metaPath)
	rel := strings.TrimPrefix(dir, filepath.Clean(CBUSDir()))
	if rel == dir { // metaPath not under CBUS_DIR (shouldn't happen) — best-effort raw
		return dir + "/inbox.jsonl"
	}
	return CBUSDir() + rel + "/inbox.jsonl"
}
