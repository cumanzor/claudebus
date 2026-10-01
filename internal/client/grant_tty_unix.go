//go:build darwin || linux

package client

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// OpenGrantTerminal opens the controlling terminal for the operator's confirmation.
// A model's shell tool has none (ENXIO), but a model can make one with a pty, so this
// is friction, not proof of a human.
func OpenGrantTerminal() (*os.File, string, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, "", err
	}
	return f, terminalName(os.Stdin), nil
}

// terminalName names the terminal behind f (stdin, which the gate requires to be
// one): /dev/tty itself always reports the same device, so it cannot say which.
func terminalName(f *os.File) string {
	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
		return "unknown"
	}
	for _, pattern := range []string{"/dev/ttys*", "/dev/pts/*", "/dev/tty[0-9]*"} {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			var c syscall.Stat_t
			if syscall.Stat(m, &c) == nil && c.Rdev == st.Rdev && c.Mode&syscall.S_IFMT == syscall.S_IFCHR {
				return m
			}
		}
	}
	return fmt.Sprintf("dev=%#x", uint64(st.Rdev))
}

// IsTerminal reports whether f is a terminal (a termios read succeeds).
func IsTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
