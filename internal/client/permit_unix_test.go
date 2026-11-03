//go:build darwin || linux

package client

import (
	"os"
	"testing"
)

func TestPermitStoreIsOwnerOnly(t *testing.T) {
	permitFixture(t)
	writeTestPermit(t, PermitRequest{Actions: []string{"draft-pr"}, Ref: "x"})
	info, err := os.Stat(permitDir("dev"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("mode = %o", got)
	}
}
