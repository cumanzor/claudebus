package client

import (
	"os"
	"syscall"
)

// openSharedRead opens path for reading with the FULL share set, which os.Open does not.
//
// Go's syscall.Open passes FILE_SHARE_READ|FILE_SHARE_WRITE and nothing else
// (GOROOT src/syscall/syscall_windows.go:395, an unconditional sharemode assignment), so
// every handle the stdlib hands back BLOCKS DELETION of its file for as long as it is
// held. The follower holds its inbox handle for the whole life of an arm, so under
// os.Open a rejoin's rm+recreate cannot succeed while any peer on the box is armed:
// reclaim, leave and prune all fail, and because those removals discarded their errors
// they reported success anyway. Naming FILE_SHARE_DELETE restores the unix behaviour the
// design already assumes — the remover unlinks, the holder keeps reading its now-unnamed
// file until it notices the rotation.
//
// That last clause is measured on the TARGET VOLUME, not claimed for windows generally:
// on logos NTFS a held handle carrying all three flags blocked neither a remove nor a
// 50-iteration rm+recreate loop, 8/8. Where a filesystem instead leaves the name in
// delete-pending until the last handle closes, the remove still succeeds but a recreate
// at the same path can fail until the follower lets go. The follower survives either way
// (it polls and reopens); only the recreate timing differs.
//
// READ-ONLY is a boundary, not an omission. The O_APPEND writers at presence.go,
// ledger.go's append and ledger.go's mint lock deliberately stay on the stdlib: routing
// them here would mean hand-rolling create-plus-append-plus-permissions, where a wrong
// FILE_APPEND_DATA corrupts a ledger rather than blocking a delete, and the mint lock's
// correctness rests on close-drops-the-lock. Converting one of them is a decision, not
// tidying — see cbus-que.8.
//
// Read-only and FILE-ONLY, both deliberate. Without FILE_FLAG_BACKUP_SEMANTICS
// CreateFile cannot open a directory, and that flag is NOT passed (D64): no caller opens
// a directory through here, and a seam that quietly answered for one would hand back a
// false negative instead of an error. A directory target fails loudly, pinned by
// TestOpenSharedReadRefusesADirectory.
func openSharedRead(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := syscall.CreateFile(
		p,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
