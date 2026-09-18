package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReclaimPreservesUntrustedMetadataAndQueuedMail(t *testing.T) {
	for _, damage := range []string{"malformed", "null", "read error"} {
		for _, operation := range []string{"join", "reserve", "rename", "prune"} {
			t.Run(damage+"/"+operation, func(t *testing.T) {
				t.Setenv("CBUS_DIR", t.TempDir())
				clearSessionEnv(t)
				t.Setenv("CBUS_SESSION_ID", "SELF")
				dir := filepath.Join(CBUSDir(), "ch", "target")
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				meta := filepath.Join(dir, "meta.json")
				if damage == "read error" {
					// Reading a directory as metadata fails deterministically, including
					// under elevated test users that can ignore mode-bit restrictions.
					if err := os.Mkdir(meta, 0755); err != nil {
						t.Fatal(err)
					}
				} else {
					body := []byte(`{"connectionId":"managed",`)
					if damage == "null" {
						body = []byte("null")
					}
					if err := os.WriteFile(meta, body, 0600); err != nil {
						t.Fatal(err)
					}
				}
				inbox := filepath.Join(dir, "inbox.jsonl")
				const queued = "queued mail must survive metadata failure\n"
				if err := os.WriteFile(inbox, []byte(queued), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				switch operation {
				case "join":
					_, _, err = Join("ch", "target")
				case "reserve":
					_, err = ReserveAlias("ch", "target", OriginFresh, "")
				case "rename":
					seedMeta(t, CBUSDir(), "ch", "self", "SELF")
					_, _, _, err = Rename("target", "ch")
				case "prune":
					if msgs := PruneChannel("ch"); len(msgs) != 0 {
						t.Fatalf("prune reported untrusted peer removed: %v", msgs)
					}
				}
				if operation != "prune" && err == nil {
					t.Fatal("destructive operation accepted unreadable or malformed metadata")
				}
				if b, err := os.ReadFile(inbox); err != nil || string(b) != queued {
					t.Fatalf("queued mail lost: %q, %v", b, err)
				}
			})
		}
	}
}

func TestReclaimStillAcceptsAbsentMetadata(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	clearSessionEnv(t)
	t.Setenv("CBUS_SESSION_ID", "SELF")
	if err := os.MkdirAll(filepath.Join(CBUSDir(), "ch", "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	if alias, _, err := Join("ch", "empty"); err != nil || alias != "empty" {
		t.Fatalf("empty reservation join = %q, %v", alias, err)
	}
}

func TestPruneStillReapsValidLegacyMetadataWithoutTimestamp(t *testing.T) {
	t.Setenv("CBUS_DIR", t.TempDir())
	dir := filepath.Join(CBUSDir(), "ch", "legacy")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"sessionId":"OLD"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if msgs := PruneChannel("ch"); len(msgs) != 1 {
		t.Fatalf("valid legacy metadata should keep existing prune behavior: %v", msgs)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("legacy peer survived prune: %v", err)
	}
}
