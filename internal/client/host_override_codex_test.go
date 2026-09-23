//go:build darwin || linux

package client

import "testing"

func TestCodexChildEnvCarriesTheHostLabel(t *testing.T) {
	prepareCodexSpawn(t)
	t.Setenv("CBUS_HOST", "laptop")
	c, err := resolveCodexSpawnContext()
	if err != nil {
		t.Fatal(err)
	}
	if c.env["CBUS_HOST"] != "laptop" {
		t.Errorf("codex child env CBUS_HOST = %q, want laptop", c.env["CBUS_HOST"])
	}
	t.Setenv("CBUS_HOST", "")
	if c, err = resolveCodexSpawnContext(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.env["CBUS_HOST"]; ok {
		t.Error("CBUS_HOST unset in the parent must not appear in the codex child env")
	}
}
