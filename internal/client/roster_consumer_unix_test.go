//go:build darwin || linux

package client

import "testing"

func TestListShowsOnlineClaudeConsumer(t *testing.T) {
	_, c, _ := admittedClaude(t)
	p := scanPeer(t, c.Channel, c.Alias)
	if !p.Native || p.ConsumerState != "online" || p.ConsumerPid != c.Claude.Binding.Endpoint.PID {
		t.Fatalf("Claude consumer pid not shown: %+v", p)
	}
}
