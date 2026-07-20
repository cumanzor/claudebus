package main

import (
	"encoding/json"
	"fmt"
	"os"

	"claudebus/internal/client"
)

// The machine-readable shape of the read-only verbs (cbus-8k9.4 M5). These DTOs are
// deliberately separate from client's view types: the field names here are a public
// contract a GUI parses, and an internal rename must not be able to change them by
// accident.
//
// FORWARD COMPATIBILITY, since windowing identity is not landed yet: every level is an
// OBJECT, never a bare array, so a level can gain sibling keys without breaking a
// consumer; peers are objects with named keys, so window/pane/term arrive as purely
// additive fields. schemaVersion bumps only on a BREAKING change — adding a field is
// not one, and a consumer that treats an unknown key as fatal is the one at fault.
const jsonSchemaVersion = 1

type listJSON struct {
	SchemaVersion int           `json:"schemaVersion"`
	Host          string        `json:"host"`
	Channels      []channelJSON `json:"channels"`
}

type channelJSON struct {
	Name string `json:"name"`
	// LegacyV1 marks a pre-channels bash entry. It is rendered EXPLICITLY rather than
	// omitted (no silent caps) and carries an empty peers array rather than a half-peer,
	// so a consumer iterating channels[].peers[] gets nothing for it instead of choking.
	LegacyV1 bool       `json:"legacyV1,omitempty"`
	Peers    []peerJSON `json:"peers"`
}

type peerJSON struct {
	Alias     string `json:"alias"`
	SessionID string `json:"sessionId"`
	Listening bool   `json:"listening"`
	// ListenerPid is absent, never 0, when the peer never armed — 0 is a real pid-shaped
	// value and would read as one.
	ListenerPid int    `json:"listenerPid,omitempty"`
	Host        string `json:"host"`
	Cwd         string `json:"cwd"`
	// Scope is pinned to "local" for now. The key exists before remote does so a
	// consumer written today keeps working when "remote" starts appearing.
	Scope  string `json:"scope"`
	Origin string `json:"origin,omitempty"`
	Model  string `json:"model,omitempty"`
}

type channelsJSON struct {
	SchemaVersion int                `json:"schemaVersion"`
	Host          string             `json:"host"`
	Channels      []channelCountJSON `json:"channels"`
}

type channelCountJSON struct {
	Name      string `json:"name"`
	Peers     int    `json:"peers"`
	Listening int    `json:"listening"`
}

// emitListJSON renders the same snapshot the text path renders, under the same
// --active and channel filters. An empty store is a valid document with an empty
// channels array, not a sentence: stdout carries exactly the JSON doc.
func emitListJSON(snap client.StoreSnapshot, active bool, chosen string) int {
	doc := listJSON{
		SchemaVersion: jsonSchemaVersion,
		Host:          client.ShortHostname(),
		Channels:      []channelJSON{},
	}
	for _, ch := range snap.Channels {
		if chosen != "" && ch.Name != chosen {
			continue
		}
		if ch.LegacyV1 {
			if active {
				continue
			}
			doc.Channels = append(doc.Channels, channelJSON{Name: ch.Name, LegacyV1: true, Peers: []peerJSON{}})
			continue
		}
		out := channelJSON{Name: ch.Name, Peers: []peerJSON{}}
		for _, p := range ch.Peers {
			if active && !p.Listening {
				continue
			}
			out.Peers = append(out.Peers, peerJSON{
				Alias:       p.Alias,
				SessionID:   p.SessionID,
				Listening:   p.Listening,
				ListenerPid: p.ListenerPid,
				Host:        p.Host,
				Cwd:         p.Cwd,
				Scope:       "local",
				Origin:      p.Origin,
				Model:       p.Model,
			})
		}
		// --active drops a channel that has no live peer left, matching the text path,
		// which prints no row for it either.
		if active && len(out.Peers) == 0 {
			continue
		}
		doc.Channels = append(doc.Channels, out)
	}
	return emitJSON(doc)
}

func emitChannelsJSON(snap client.StoreSnapshot) int {
	doc := channelsJSON{
		SchemaVersion: jsonSchemaVersion,
		Host:          client.ShortHostname(),
		Channels:      []channelCountJSON{},
	}
	for _, ch := range snap.Channels {
		if ch.LegacyV1 || len(ch.Peers) == 0 {
			continue
		}
		live := 0
		for _, p := range ch.Peers {
			if p.Listening {
				live++
			}
		}
		doc.Channels = append(doc.Channels, channelCountJSON{Name: ch.Name, Peers: len(ch.Peers), Listening: live})
	}
	return emitJSON(doc)
}

type whoamiJSON struct {
	SchemaVersion int    `json:"schemaVersion"`
	SessionID     string `json:"sessionId"`
	// Joined is the answer the exit code also carries, spelled out so a consumer that
	// only reads stdout does not have to infer it from two array lengths.
	Joined bool `json:"joined"`
	// Local and Remote are separate keys rather than one list with a kind field: they
	// are genuinely different identities (a local registration has no host, a remote
	// from-default marker always does), and the split is what makes that legible.
	Local  []localRegJSON  `json:"local"`
	Remote []remoteRegJSON `json:"remote"`
}

type localRegJSON struct {
	Channel string `json:"channel"`
	Alias   string `json:"alias"`
}

type remoteRegJSON struct {
	Channel string `json:"channel"`
	Host    string `json:"host"`
	Alias   string `json:"alias"`
}

// emitWhoamiJSON renders ONE document shape whether or not the session is joined: an
// unjoined session gets the same keys with empty arrays, never a different document
// and never a sentence, so a consumer parses one thing. The exit code stays 1 when
// both collections are empty — frozen behavior scripts already branch on.
func emitWhoamiJSON(local []client.LocalReg, remote []client.RemoteReg) int {
	doc := whoamiJSON{
		SchemaVersion: jsonSchemaVersion,
		SessionID:     client.SessionID(),
		Joined:        len(local) > 0 || len(remote) > 0,
		Local:         []localRegJSON{},
		Remote:        []remoteRegJSON{},
	}
	for _, r := range local {
		doc.Local = append(doc.Local, localRegJSON{Channel: r.Channel, Alias: r.Alias})
	}
	for _, r := range remote {
		doc.Remote = append(doc.Remote, remoteRegJSON{Channel: r.Channel, Host: r.Host, Alias: r.Alias})
	}
	if rc := emitJSON(doc); rc != 0 {
		return rc
	}
	if !doc.Joined {
		return 1
	}
	return 0
}

// emitJSON writes one indented document to stdout. HTML escaping is off for the same
// reason the formation writer turns it off: a cwd containing & or < must round-trip as
// itself, not as an entity.
func emitJSON(doc any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintf(os.Stderr, "cbus: %v\n", err)
		return 1
	}
	return 0
}
