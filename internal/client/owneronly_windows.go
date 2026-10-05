package client

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	advapi32                 = syscall.NewLazyDLL("advapi32.dll")
	procSDDLToSecurityDesc   = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procGetSecurityDescDacl  = advapi32.NewProc("GetSecurityDescriptorDacl")
	procSetNamedSecurityInfo = advapi32.NewProc("SetNamedSecurityInfoW")
	procLocalFree            = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

const (
	seFileObject              = 1
	daclSecurityInformation   = 0x4
	protectedDaclSecurityInfo = 0x80000000
	sddlRevision1             = 1
)

// restrictToOwner replaces path's DACL with a protected one granting only the
// current user, SYSTEM and Administrators, inherited by everything below it.
// 0700 is a no-op on windows, and a profile directory can carry grants for
// other accounts that every child would otherwise inherit.
func restrictToOwner(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	sddl, err := syscall.UTF16PtrFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return err
	}
	var sd uintptr
	if r, _, e := procSDDLToSecurityDesc.Call(uintptr(unsafe.Pointer(sddl)), sddlRevision1, uintptr(unsafe.Pointer(&sd)), 0); r == 0 {
		return fmt.Errorf("build owner-only ACL: %w", e)
	}
	defer procLocalFree.Call(sd)
	var present, defaulted int32
	var dacl uintptr
	if r, _, e := procGetSecurityDescDacl.Call(sd, uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&dacl)), uintptr(unsafe.Pointer(&defaulted))); r == 0 {
		return fmt.Errorf("read owner-only ACL: %w", e)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if r, _, _ := procSetNamedSecurityInfo.Call(uintptr(unsafe.Pointer(name)), seFileObject, daclSecurityInformation|protectedDaclSecurityInfo, 0, 0, dacl, 0); r != 0 {
		return fmt.Errorf("restrict %s to its owner: %w", path, syscall.Errno(r))
	}
	return nil
}

func currentUserSID() (string, error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String()
}
