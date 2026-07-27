package client

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
)

// Structural identity tokens: the (pid, starttime) witness that replaces argv-grep
// liveness. Composition lives HERE, platform-neutral and pure over injected bytes,
// for two reasons. The writer that records a token and the prober that checks one
// must compose it the same way forever, so there is exactly one function per
// platform format and both sides call it. And a pure composer can be tested from any
// host, so the linux token's behavior is provable on a darwin laptop instead of
// waiting on a linux box. The procinfo_<os>.go files are syscall wrappers that feed
// these; they hold no format knowledge.
//
// A token is OPAQUE to callers: compared by byte equality, never parsed as a clock
// (R2). Nothing here does date math, and nothing here reads a file.

var (
	errShortProcInfo  = errors.New("short proc_bsdinfo buffer")
	errNoStartField   = errors.New("no starttime field in /proc stat")
	errNoCreationTime = errors.New("zero process creation time")
)

// darwin proc_bsdinfo start-time offsets, verified against `ps -o lstart` on live
// pids rather than trusted from the header layout (returned struct size is 136).
const (
	offStartTvsec  = 120 // uint64 pbi_start_tvsec
	offStartTvusec = 128 // uint64 pbi_start_tvusec
)

// darwinStartToken composes "<tvsec>.<tvusec>" from a proc_bsdinfo buffer. The
// buffer is little-endian on both darwin architectures.
func darwinStartToken(info []byte) (string, error) {
	if len(info) < offStartTvusec+8 {
		return "", errShortProcInfo
	}
	sec := binary.LittleEndian.Uint64(info[offStartTvsec:])
	usec := binary.LittleEndian.Uint64(info[offStartTvusec:])
	return strconv.FormatUint(sec, 10) + "." + strconv.FormatUint(usec, 10), nil
}

// statStartTimeField indexes starttime in the whitespace-split remainder of
// /proc/<pid>/stat AFTER the parenthesized comm. starttime is field 22 overall and
// the split consumes fields 1-2 (pid, comm), so 22-3 = 19. Anchored by rest[1] being
// ppid, which procParent already depends on.
const statStartTimeField = 19

// linuxStartToken composes "<boot_id>:<jiffies>" from /proc/<pid>/stat bytes and a
// boot id. comm is delimited by the LAST ')' because a process can name itself
// "((eve(il)" and the kernel does not escape it.
//
// The boot id is injected rather than read here so this stays pure and testable off
// linux. It is trimmed defensively even though the reader trims too: a trailing
// newline would poison byte-equality on every comparison AND leak a raw \n into
// meta.json, and that guarantee should not depend on which caller composed it.
//
// An empty boot id degrades the token to bare jiffies: weaker (jiffies are
// boot-relative while $CBUS_DIR outlives a reboot) but never falser. Asymmetric
// readability across arm and probe yields a mismatch, which reads dead.
func linuxStartToken(stat []byte, bootID string) (string, error) {
	s := string(stat)
	closeParen := strings.LastIndexByte(s, ')')
	if closeParen < 0 {
		return "", errNoStartField
	}
	rest := strings.Fields(s[closeParen+1:])
	if len(rest) <= statStartTimeField {
		return "", errNoStartField
	}
	jiffies := rest[statStartTimeField]
	if id := strings.TrimSpace(bootID); id != "" {
		return id + ":" + jiffies, nil
	}
	return jiffies, nil
}

// windowsStartToken composes the two halves of a process creation FILETIME into one
// decimal. It needs no boot id, unlike the linux token: a FILETIME counts 100ns ticks
// from 1601 UTC and is absolute, so it stays comparable across the reboot that makes
// jiffies meaningless.
//
// A zero creation time is rejected rather than composed. Zero is what a failed or
// partial read leaves behind, and two such reads would byte-match each other — the
// one way an opaque token can answer "same process" about processes it never saw.
func windowsStartToken(creationHigh, creationLow uint32) (string, error) {
	ticks := uint64(creationHigh)<<32 | uint64(creationLow)
	if ticks == 0 {
		return "", errNoCreationTime
	}
	return strconv.FormatUint(ticks, 10), nil
}
