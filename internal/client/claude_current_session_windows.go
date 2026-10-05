//go:build windows

package client

import "os"

// windows reports a reparse point through Lstat's mode; there is no O_NOFOLLOW.
const noFollowOpen = 0

func claudeRegistryInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular|os.ModeDevice|os.ModeNamedPipe) != 0 {
		return false
	}
	if directory {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}
