package main

import (
	"encoding/json"
	"strings"
	"testing"

	"claudebus/internal/client"
)

func TestListPidColumn(t *testing.T) {
	for _, tc := range []struct {
		name string
		peer client.PeerView
		want string
	}{
		{"legacy listener", client.PeerView{ListenerPid: 812}, "812"},
		{"legacy never armed", client.PeerView{}, "?"},
		{"native online", client.PeerView{Native: true, ListenerPid: 900, ConsumerState: "online", ConsumerPid: 4321}, "4321"},
		{"native not online", client.PeerView{Native: true, ListenerPid: 900, ConsumerState: "exited"}, "?"},
		{"native disconnected", client.PeerView{Native: true, ListenerPid: -1, ConsumerState: "disconnected"}, "?"},
	} {
		if got := listPid(tc.peer); got != tc.want {
			t.Errorf("%s: pid=%s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestListJSONConsumerFieldsAreAdditive(t *testing.T) {
	jsonStore(t)
	snap := client.StoreSnapshot{Channels: []client.ChannelView{{Name: "demo", Peers: []client.PeerView{
		{Alias: "native", Listening: true, ListenerPid: 900, Native: true, ConsumerState: "online", ConsumerPid: 4321},
		{Alias: "exited", Listening: true, ListenerPid: 900, Native: true, ConsumerState: "exited"},
		{Alias: "legacy", Listening: true, ListenerPid: 812},
	}}}}
	out := captureStdout(t, func() { emitListJSON(snap, false, "") })
	var raw struct {
		Channels []struct {
			Peers []map[string]json.RawMessage `json:"peers"`
		} `json:"channels"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil || len(raw.Channels) != 1 || len(raw.Channels[0].Peers) != 3 {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	native, exited, legacy := raw.Channels[0].Peers[0], raw.Channels[0].Peers[1], raw.Channels[0].Peers[2]
	if string(native["listenerPid"]) != "900" || string(native["consumerPid"]) != "4321" || string(native["consumerState"]) != `"online"` {
		t.Errorf("native row: %s", out)
	}
	if _, ok := exited["consumerPid"]; ok || string(exited["consumerState"]) != `"exited"` {
		t.Errorf("exited row carried a consumer pid: %s", out)
	}
	for _, k := range []string{"consumerPid", "consumerState"} {
		if _, ok := legacy[k]; ok || strings.Contains(string(legacy["listenerPid"]), "4321") {
			t.Errorf("legacy row gained %s: %s", k, out)
		}
	}
}
