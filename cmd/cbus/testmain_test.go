package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Tests set identity deliberately. A suite run from Codex must not inherit the
	// real session as a fallback in cases exercising sessionless behavior.
	_ = os.Unsetenv("CODEX_THREAD_ID")
	os.Exit(m.Run())
}
