//go:build windows

package client

// Windows' detached app-server/process handle topology does not expose a
// portable queue-store witness here. The explicit store option remains usable.
func currentCodexRuntimeBinding() (codexRuntimeBinding, error) {
	return codexRuntimeBinding{}, nil
}
