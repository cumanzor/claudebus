package client

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

var (
	procGetNamedSecurityInfo = advapi32.NewProc("GetNamedSecurityInfoW")
	procSecurityDescToSDDL   = advapi32.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW")
	aceSID                   = regexp.MustCompile(`\((A|D);([^;]*);[^;]*;;;([^)]+)\)`)
)

func fileDACL(t *testing.T, path string) string {
	t.Helper()
	name, _ := syscall.UTF16PtrFromString(path)
	var sd uintptr
	if r, _, _ := procGetNamedSecurityInfo.Call(uintptr(unsafe.Pointer(name)), seFileObject, daclSecurityInformation, 0, 0, 0, 0, uintptr(unsafe.Pointer(&sd))); r != 0 {
		t.Fatalf("read DACL of %s: %v", path, syscall.Errno(r))
	}
	defer procLocalFree.Call(sd)
	var str *uint16
	if r, _, e := procSecurityDescToSDDL.Call(sd, sddlRevision1, daclSecurityInformation, uintptr(unsafe.Pointer(&str)), 0); r == 0 {
		t.Fatalf("format DACL of %s: %v", path, e)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(str)))
	n := 0
	for *(*uint16)(unsafe.Add(unsafe.Pointer(str), 2*n)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(str, n))
}

// grantees lists every SID an ACE in the DACL names, and whether each ACE is
// inherited.
func grantees(dacl string) (sids []string, inherited []bool) {
	for _, m := range aceSID.FindAllStringSubmatch(dacl, -1) {
		sids = append(sids, m[3])
		inherited = append(inherited, strings.Contains(m[2], "ID"))
	}
	return sids, inherited
}

func TestRestrictToOwnerLeavesOnlyOwnerSystemAndAdministrators(t *testing.T) {
	me, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"BA", "SY", me}
	slices.Sort(want)
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	before := filepath.Join(dir, "before")
	if err := os.WriteFile(before, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restrictToOwner(dir); err != nil {
		t.Fatal(err)
	}
	dacl := fileDACL(t, dir)
	if !strings.HasPrefix(dacl, "D:P") {
		t.Fatalf("directory DACL still inherits from its parent: %s", dacl)
	}
	sids, _ := grantees(dacl)
	slices.Sort(sids)
	if !slices.Equal(sids, want) {
		t.Fatalf("directory grants %v, want only %v (%s)", sids, want, dacl)
	}
	after := filepath.Join(dir, "after")
	if err := os.WriteFile(after, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{before, after} {
		dacl := fileDACL(t, f)
		sids, inherited := grantees(dacl)
		slices.Sort(sids)
		if !slices.Equal(sids, want) || slices.Contains(inherited, false) {
			t.Fatalf("%s grants %v inherited=%v, want only the inherited %v (%s)", filepath.Base(f), sids, inherited, want, dacl)
		}
	}
}
