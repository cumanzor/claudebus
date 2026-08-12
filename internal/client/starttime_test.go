package client

import (
	"encoding/binary"
	"strings"
	"testing"
)

// procStatLine is a realistic /proc/<pid>/stat body. Every field carries a distinct
// value so a wrong index is visible rather than accidentally right, and the anchors
// are checkable by eye: ppid is 1 (index 1) and starttime is 987654 (index 19).
const procStatLine = "1234 (bash) S 1 1234 1234 34816 1234 4194304 100 200 300 400 " +
	"500 600 700 800 20 0 1 0 987654 12345678 900\n"

// bsdinfoWith lays the start time at LITERAL offsets 120/128 rather than at
// offStartTvsec/offStartTvusec. Using the constants would make the composition test
// tautological — it would write and read at the same place and pass on any offset,
// which is exactly what it did before a mutation run caught it. Hardcoding the ABI
// here means the test disagrees with the code when the code is wrong.
func bsdinfoWith(sec, usec uint64) []byte {
	buf := make([]byte, 136)
	binary.LittleEndian.PutUint64(buf[120:], sec)
	binary.LittleEndian.PutUint64(buf[128:], usec)
	return buf
}

func TestDarwinStartTokenComposition(t *testing.T) {
	got, err := darwinStartToken(bsdinfoWith(1784436015, 326553))
	if err != nil {
		t.Fatalf("darwinStartToken: %v", err)
	}
	if want := "1784436015.326553"; got != want {
		t.Errorf("token = %q, want %q", got, want)
	}
}

// TestDarwinStartTokenShortBuffer: a truncated proc_bsdinfo must error, not read past
// the returned length into whatever the stack had (which would be stable garbage and
// therefore an extremely convincing wrong answer).
func TestDarwinStartTokenShortBuffer(t *testing.T) {
	for _, n := range []int{0, 119, offStartTvusec + 7} {
		if _, err := darwinStartToken(make([]byte, n)); err == nil {
			t.Errorf("darwinStartToken(%d bytes) should error", n)
		}
	}
}

// TestLinuxStartTokenComposition runs the linux format on whatever host this is —
// the point of the platform-neutral composer (B1). Before it, the linux field index
// could only be argued for; now it is asserted from a darwin laptop.
func TestLinuxStartTokenComposition(t *testing.T) {
	got, err := linuxStartToken([]byte(procStatLine), "abc-boot-id")
	if err != nil {
		t.Fatalf("linuxStartToken: %v", err)
	}
	if want := "abc-boot-id:987654"; got != want {
		t.Errorf("token = %q, want %q", got, want)
	}
}

// TestLinuxStartTokenHostileComm: comm is attacker-controlled (a process can rename
// itself) and the kernel does not escape it. Splitting on the FIRST ')' or on spaces
// would shift every later field, silently moving starttime.
func TestLinuxStartTokenHostileComm(t *testing.T) {
	hostile := strings.Replace(procStatLine, "(bash)", "(evil ) proc (x)", 1)
	got, err := linuxStartToken([]byte(hostile), "b")
	if err != nil {
		t.Fatalf("linuxStartToken: %v", err)
	}
	if want := "b:987654"; got != want {
		t.Errorf("token = %q, want %q — a comm with spaces/parens shifted the field", got, want)
	}
}

// TestLinuxStartTokenBootIDVariation is the B4 seam: same pid, same jiffies, different
// boot. Jiffies are boot-relative while $CBUS_DIR outlives a reboot, so without the
// boot id a post-reboot pid could byte-match a pre-reboot record and a stranger would
// read as the armed listener. Runnable here on darwin because the composer is pure.
func TestLinuxStartTokenBootIDVariation(t *testing.T) {
	before, err := linuxStartToken([]byte(procStatLine), "boot-A")
	if err != nil {
		t.Fatal(err)
	}
	after, err := linuxStartToken([]byte(procStatLine), "boot-B")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Errorf("identical token %q across boots — a recycled pid would read alive after a reboot", before)
	}
}

// TestLinuxStartTokenBootIDTrimmed: /proc/sys/kernel/random/boot_id ends in a newline.
// Untrimmed it would poison byte-equality on every comparison AND leak a raw \n into
// meta.json. The composer trims defensively so the guarantee does not depend on which
// caller composed the token (B1).
func TestLinuxStartTokenBootIDTrimmed(t *testing.T) {
	got, err := linuxStartToken([]byte(procStatLine), "boot-A\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got, "\n\r\t ") {
		t.Errorf("token %q carries whitespace", got)
	}
	clean, err := linuxStartToken([]byte(procStatLine), "boot-A")
	if err != nil {
		t.Fatal(err)
	}
	if got != clean {
		t.Errorf("trimmed %q != untrimmed-input %q — byte equality would break across writers", clean, got)
	}
}

// TestLinuxStartTokenNoBootID: degrade to bare jiffies. Weaker witness, never a
// falser one — and asymmetric readability across arm and probe yields a mismatch,
// which reads dead.
func TestLinuxStartTokenNoBootID(t *testing.T) {
	got, err := linuxStartToken([]byte(procStatLine), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := "987654"; got != want {
		t.Errorf("token = %q, want %q", got, want)
	}
}

func TestLinuxStartTokenMalformed(t *testing.T) {
	cases := map[string]string{
		"no comm parens": "1234 bash S 1 2 3",
		"too few fields": "1234 (bash) S 1 2 3",
		"empty":          "",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := linuxStartToken([]byte(body), "b"); err == nil {
				t.Error("should error rather than compose a partial token")
			}
		})
	}
}

// TestWindowsStartTokenComposition pins the 64-bit composition of a process creation
// FILETIME. Nothing else can: the stability check against a live process on windows is
// blind to the mutation this composer is most likely to suffer, because a
// swapped-halves composer is perfectly STABLE across two reads and so reads green while
// producing the wrong token. Only injected halves separate them.
func TestWindowsStartTokenComposition(t *testing.T) {
	cases := []struct {
		name      string
		high, low uint32
		want      string
		wantErr   bool
	}{
		{name: "low half alone", low: 5, want: "5"},
		{name: "high half alone lands at 2^32", high: 1, want: "4294967296"},
		{name: "both halves", high: 1, low: 5, want: "4294967301"},
		{name: "a zero creation time is rejected, never composed", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := windowsStartToken(c.high, c.low)
			if c.wantErr {
				if err == nil {
					t.Fatalf("windowsStartToken(%d,%d) = %q, want an error", c.high, c.low, got)
				}
				if got != "" {
					t.Errorf("windowsStartToken(%d,%d) returned %q alongside its error", c.high, c.low, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("windowsStartToken(%d,%d): %v", c.high, c.low, err)
			}
			if got != c.want {
				t.Errorf("windowsStartToken(%d,%d) = %q, want %q", c.high, c.low, got, c.want)
			}
		})
	}
}
