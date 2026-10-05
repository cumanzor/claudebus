package main

import (
	"errors"
	"syscall"
)

// syscall.ECONNREFUSED and friends are invented constants on windows that no
// winsock error matches, so the real codes are checked here.
const (
	wsaeConnAborted syscall.Errno = 10053
	wsaeConnReset   syscall.Errno = 10054
	wsaeNetDown     syscall.Errno = 10050
	wsaeConnRefused syscall.Errno = 10061
)

// an AF_UNIX dial reports a missing parent directory as WSAENETDOWN and a
// missing or stale socket file as WSAECONNREFUSED.
func socketAbsent(err error) bool {
	return errors.Is(err, wsaeConnRefused) || errors.Is(err, wsaeNetDown)
}

func socketReset(err error) bool {
	return errors.Is(err, wsaeConnReset) || errors.Is(err, wsaeConnAborted)
}
