package client

import (
	"os"
	"path/filepath"
	"strings"
)

// StoreSnapshot is the whole local bus as the read-only verbs see it: every channel,
// every peer, liveness already resolved.
type StoreSnapshot struct {
	Channels []ChannelView
}

// ChannelView is one channel directory. A legacy v1 entry has no peers by
// construction — it predates channels, so there is no alias level under it.
type ChannelView struct {
	Name     string
	LegacyV1 bool
	Peers    []PeerView
}

// PeerView is one peer as rendered. Fields are whatever meta.json held; a torn read
// leaves them blank rather than dropping the peer.
type PeerView struct {
	Alias       string
	SessionID   string
	Listening   bool
	ListenerPid int
	Host        string
	Cwd         string
	Origin      string
	Model       string
	// A native peer's ListenerPid is the daemon. ConsumerPid is the harness
	// process the daemon last observed online, or 0 when that is not known now.
	Native        bool
	ConsumerState string
	ConsumerPid   int
}

// ScanStore walks $CBUS_DIR and returns every channel and peer, in the ReadDir order
// the text renderers have always used (channel-major, alphabetical).
//
// NOT ChannelRoster (formation_save.go), and the two must not be unified without a
// ruling on which behavior wins. That one answers a different question differently: it
// takes ONE channel, ERRORS when the channel is absent, and DROPS a peer whose
// meta.json is torn. This one takes the whole store, reads an unreadable root as
// empty, and KEEPS a torn-meta peer with blank fields — because `list` has always
// shown that peer with "?" columns rather than hiding it, and hiding a peer is how a
// user loses track of a session.
//
// Liveness is MetaListenerAlive verbatim. No process-state logic lives here.
func ScanStore() StoreSnapshot {
	root := CBUSDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return StoreSnapshot{}
	}
	var snap StoreSnapshot
	for _, chE := range entries {
		if !chE.IsDir() || strings.HasPrefix(chE.Name(), ".") {
			continue
		}
		chDir := filepath.Join(root, chE.Name())
		if fileExists(filepath.Join(chDir, "meta.json")) {
			snap.Channels = append(snap.Channels, ChannelView{Name: chE.Name(), LegacyV1: true})
			continue
		}
		view := ChannelView{Name: chE.Name()}
		aliases, _ := os.ReadDir(chDir)
		for _, alE := range aliases {
			if !alE.IsDir() || strings.HasPrefix(alE.Name(), ".") {
				continue
			}
			metaPath := filepath.Join(chDir, alE.Name(), "meta.json")
			if !fileExists(metaPath) {
				continue
			}
			m, _ := ReadPeerMeta(metaPath) // ok is deliberately ignored: see the doc comment
			peer := PeerView{
				Alias:       alE.Name(),
				SessionID:   m.SessionID,
				Listening:   MetaListenerAlive(metaPath),
				ListenerPid: m.ListenerPid,
				Host:        m.Host,
				Cwd:         m.Cwd,
				Origin:      m.Origin,
				Model:       m.Model,
			}
			if m.ConnectionID != "" {
				peer.Native = true
				peer.ConsumerState, peer.ConsumerPid = journaledConsumer(chE.Name(), alE.Name(), m)
				if !peer.Listening && peer.ConsumerState == "online" {
					// the daemon is not observing it, so the journal may be stale
					peer.ConsumerState, peer.ConsumerPid = "unknown", 0
				}
			}
			view.Peers = append(view.Peers, peer)
		}
		snap.Channels = append(snap.Channels, view)
	}
	return snap
}

// journaledConsumer never fails a list: a missing, unreadable or foreign journal
// reads as unknown. The recorded PID is kept after an exit, so it counts only
// while online.
func journaledConsumer(ch, alias string, m PeerMeta) (state string, pid int) {
	c, err := readManagedJournal(ch, alias, m)
	if err != nil || c.Consumer == nil || c.Consumer.State == "" {
		return "unknown", 0
	}
	if c.Consumer.State == "online" {
		return c.Consumer.State, c.Consumer.PID
	}
	return c.Consumer.State, 0
}
