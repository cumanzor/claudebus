package client

import "strings"

// accept only the local managed-server launch shape, never a flag in a prompt,
// a generic app server, or a daemon maintenance subcommand.
func managedCodexProcess(argv string) bool {
	args := strings.Fields(argv)
	if len(args) < 2 || !strings.EqualFold(commBase(args[0]), "codex") || args[1] != "app-server" {
		return false
	}
	managed, local := false, false
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--managed-daemon":
			if managed {
				return false
			}
			managed = true
		case "--listen", "--listen=unix://":
			if local {
				return false
			}
			if args[i] == "--listen" {
				i++
				if i >= len(args) || args[i] != "unix://" {
					return false
				}
			}
			local = true
		case "--analytics-default-enabled":
		default:
			return false
		}
	}
	return managed && local
}
