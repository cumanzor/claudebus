//go:build !windows

package main

// the portable syscall errnos already cover unix.
func socketAbsent(error) bool { return false }

func socketReset(error) bool { return false }
