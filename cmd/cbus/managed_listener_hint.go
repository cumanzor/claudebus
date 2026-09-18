package main

import (
	"claudebus/internal/client"
	"fmt"
	"path/filepath"
)

func printManagedListenerHint(channel, alias string) bool {
	m, ok := client.ReadPeerMeta(filepath.Join(client.CBUSDir(), channel, alias, "meta.json"))
	if !ok || m.ConnectionID == "" {
		return false
	}
	fmt.Printf("daemon-managed: inspect cbus connection status %s/%s; do not arm a Monitor or tail loop\n", channel, alias)
	return true
}
