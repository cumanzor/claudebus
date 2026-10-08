package client

import "strings"

// a managed server is a consumer only while its direct CLI parent is observed
// alive. this does not make the shared backend safe to signal.
func managedCodexCLIParent(pid int, argv string) (int, string, error) {
	if !managedCodexProcess(argv) {
		return 0, "", nil
	}
	_, parent, err := procParent(pid)
	if err != nil || parent <= 1 {
		return 0, "", err
	}
	before, err := procStartTime(parent)
	if err != nil {
		return 0, "", err
	}
	comm, _, err := procParent(parent)
	if err != nil {
		return 0, "", err
	}
	args, err := procArgs(parent)
	if err != nil {
		return 0, "", err
	}
	if !strings.EqualFold(commBase(comm), "codex") || !interactiveCodexProcess(args) || procZombie(parent) {
		return 0, "", nil
	}
	after, err := procStartTime(parent)
	if err != nil || before != after {
		return 0, "", err
	}
	_, stillParent, err := procParent(pid)
	if err != nil || parent != stillParent {
		return 0, "", err
	}
	return parent, before, nil
}

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
