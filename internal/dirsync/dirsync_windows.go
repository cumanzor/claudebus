package dirsync

import "os"

// a read-only directory handle cannot be flushed on windows (FlushFileBuffers
// needs write access), and NTFS journals the entry change itself.
func Sync(string) error { return nil }

func SyncRoot(*os.Root) error { return nil }
