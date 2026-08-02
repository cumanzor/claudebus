//go:build darwin || linux

package main

// phase1Refusal has nothing to refuse here: every verb the windows build excludes in
// phase 1 runs natively on darwin and linux.
func phase1Refusal(string) string { return "" }
