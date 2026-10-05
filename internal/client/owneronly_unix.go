//go:build !windows

package client

// unix callers already create these directories 0700.
func restrictToOwner(string) error { return nil }
