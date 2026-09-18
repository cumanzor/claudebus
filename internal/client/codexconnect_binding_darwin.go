//go:build darwin

package client

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func currentCodexRuntimeBinding() (codexRuntimeBinding, error) {
	return codexRuntimeWitness(syscall.Getppid(), procLookup(), inspectCodexRuntime)
}

func inspectCodexRuntime(pid int) (codexRuntimeBinding, error) {
	before, err := procStartTime(pid)
	if err != nil {
		return codexRuntimeBinding{}, fmt.Errorf("inspect current Codex process: %w", err)
	}
	// KERN_PROCARGS2 starts with argc, then the kernel's executable path. Unlike
	// space-joined argv, that path preserves spaces and does not parse user flags.
	mib := [3]int32{_CTL_KERN, _KERN_PROCARGS2, int32(pid)}
	var size uintptr
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), 3, 0, uintptr(unsafe.Pointer(&size)), 0, 0); errno != 0 {
		return codexRuntimeBinding{}, errno
	}
	if size < 5 {
		return codexRuntimeBinding{}, syscall.ESRCH
	}
	buf := make([]byte, size)
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0); errno != 0 {
		return codexRuntimeBinding{}, errno
	}
	path := buf[4:size]
	end := bytes.IndexByte(path, 0)
	if end < 1 {
		return codexRuntimeBinding{}, fmt.Errorf("current Codex executable path is unavailable")
	}
	binary := string(path[:end])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := boundedCmd(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-F0fn")
	out, err := cmd.Output()
	if err != nil {
		return codexRuntimeBinding{}, fmt.Errorf("inspect current Codex open queue store (retry this command with normal process-inspection approval): %w", err)
	}
	var paths []string
	for _, field := range bytes.Split(out, []byte{0}) {
		value := strings.TrimPrefix(string(field), "\n")
		if strings.HasPrefix(value, "n") {
			paths = append(paths, value[1:])
		}
	}
	home, err := codexQueueHome(paths)
	if err != nil {
		return codexRuntimeBinding{}, err
	}
	after, err := procStartTime(pid)
	if err != nil || after != before {
		return codexRuntimeBinding{}, fmt.Errorf("current Codex process changed during queue inspection")
	}
	return codexRuntimeBinding{Binary: binary, SQLiteHome: home, PID: pid, StartToken: before}, nil
}
