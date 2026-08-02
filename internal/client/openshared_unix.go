//go:build darwin || linux

package client

import "os"

// openSharedRead opens path for reading. Unix already permits a held file to be
// unlinked out from under its reader, which is the semantics the follower is built on,
// so there is nothing to add here — the seam exists for the windows side.
func openSharedRead(path string) (*os.File, error) { return os.Open(path) }
