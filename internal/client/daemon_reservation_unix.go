//go:build !windows

package client

import (
	"os"
	"syscall"
)

func reservationReadFlags() int { return os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK }
