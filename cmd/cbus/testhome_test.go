package main

import "testing"

// testHome points os.UserHomeDir at dir on either OS: HOME is read on unix, USERPROFILE on
// windows. Both are set so a callee, child env, or direct os.Getenv that consults the other
// still lands in the sandbox. Twin of internal/client setHome.
func testHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
