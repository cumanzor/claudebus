//go:build linux

package client

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// linux process inspection via procfs (no ps spawn, Decision 1a).

var errBadStat = errors.New("unparseable /proc stat")

// procStartTime reads pid's /proc stat line and the boot id, and hands both to the
// shared composer. Syscall wrapper only: the token's format lives in starttime.go so
// the writer and the prober cannot drift. An error makes the caller read the listener
// DEAD — a probe that cannot answer never answers alive.
func procStartTime(pid int) (string, error) {
	if pid <= 0 {
		return "", syscall.ESRCH
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	return linuxStartToken(b, bootID())
}

// bootID is the kernel's per-boot uuid; "" when unreadable. Trimmed at the read (the
// file ends in a newline) and again in the composer, so no caller can leak a raw \n
// into a token or into meta.json.
func bootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// procArgs returns pid's argv space-joined (as `ps -o args=`), from
// /proc/<pid>/cmdline (NUL-separated). A read error — ESRCH, or a zombie whose
// cmdline is empty — makes the argv liveness clause read DEAD (edge D1).
func procArgs(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	return strings.Join(parts, " "), nil
}

// procParent returns pid's comm (`ps -o comm=`) and parent pid from
// /proc/<pid>/stat. comm is field 2 in parens and may itself contain spaces or
// parens, so it is delimited by the FIRST '(' and the LAST ')'.
func procParent(pid int) (comm string, ppid int, err error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, err
	}
	s := string(b)
	open := strings.IndexByte(s, '(')
	closeParen := strings.LastIndexByte(s, ')')
	if open < 0 || closeParen < 0 || closeParen < open {
		return "", 0, errBadStat
	}
	comm = s[open+1 : closeParen]
	rest := strings.Fields(s[closeParen+1:]) // state ppid pgrp ...
	if len(rest) < 2 {
		return "", 0, errBadStat
	}
	ppid, err = strconv.Atoi(rest[1])
	return comm, ppid, err
}

// procZombie reports whether pid is a zombie: state field 'Z' in /proc/<pid>/stat
// (the field right after the parenthesized comm). Unreadable => false, matching
// the darwin impl's posture (a doubt is not a zombie).
func procZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	closeParen := strings.LastIndexByte(s, ')')
	if closeParen < 0 {
		return false
	}
	rest := strings.Fields(s[closeParen+1:])
	return len(rest) > 0 && rest[0] == "Z"
}
