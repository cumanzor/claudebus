//go:build windows

package client

import "os"

// shared-delete access, so holding the transcript never blocks Claude Code
// from rotating or removing it.
func openClaudeTranscript(path string) (*os.File, error) { return openSharedRead(path) }

// windows grants through the profile's ACL rather than mode bits; refuse
// reparse points and anything that is not a plain file.
func trustedClaudeTranscriptInfo(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0
}
