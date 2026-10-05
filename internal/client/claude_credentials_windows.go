package client

import "os"

// windows has no FIFO to swap in for a regular file.
const nonBlockingOpen = 0

// the daemon directory carries a protected owner-only DACL that everything
// below it inherits (restrictToOwner), so privacy here is about refusing
// reparse points and anything that is not a plain file or directory.
func privateClaudeCredentialInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular|os.ModeDevice|os.ModeNamedPipe) != 0 {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}
