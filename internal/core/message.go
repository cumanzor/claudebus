package core

import (
	"encoding/json"
	"time"
)

// Message is the domain shape of a bus line — the local inbox line
// (bin/cbus:480-482, keys from,to,ts,text in insertion order), the relay stored
// line (main.go:181-187, keys alphabetical), and the presence variant
// (bin/cbus:341-343, adds kind+event). Receivers must parse JSON and never
// pattern-match key order (protocol.md §3.3): the two writers emit different key
// orders, so this type is decode-order-agnostic by construction (JSON) and its
// Marshal emits the plain-line shape {from,to,ts,text} (kind/event omitted unless
// set — presence only).
//
// Decoding is lenient (see flexString): the bash client is untyped, so legacy /
// foreign lines can carry JSON numbers where strings are expected. This is the
// "json.Number for legacy int aliases" tolerance from the port brief, applied to
// every string field rather than via Decoder.UseNumber (which only reaches
// interface{} fields, not typed string fields).
type Message struct {
	From  string `json:"from"`
	To    string `json:"to"`
	TS    string `json:"ts"`
	Text  string `json:"text"`
	Kind  string `json:"kind,omitempty"`  // presence only ("presence")
	Event string `json:"event,omitempty"` // presence only (join|leave|rename|departed)
}

// UnmarshalJSON decodes leniently via a flexString shadow, so a number in any
// field (e.g. `"from":123`, a digit-coerced `"alias":42` precedent) decodes to
// its literal string and `null` decodes to "" instead of erroring. Non-object
// JSON still errors (the caller treats that as "not a message", mirroring the
// framer passthrough gate).
func (m *Message) UnmarshalJSON(b []byte) error {
	var shadow struct {
		From  flexString `json:"from"`
		To    flexString `json:"to"`
		TS    flexString `json:"ts"`
		Text  flexString `json:"text"`
		Kind  flexString `json:"kind"`
		Event flexString `json:"event"`
	}
	if err := json.Unmarshal(b, &shadow); err != nil {
		return err
	}
	m.From, m.To, m.TS = string(shadow.From), string(shadow.To), string(shadow.TS)
	m.Text, m.Kind, m.Event = string(shadow.Text), string(shadow.Kind), string(shadow.Event)
	return nil
}

// DecodeMessage is the convenience form of json.Unmarshal into a Message. It
// returns an error for non-object JSON (arrays, bare scalars).
func DecodeMessage(b []byte) (Message, error) {
	var m Message
	err := json.Unmarshal(b, &m)
	return m, err
}

// flexString decodes a JSON string, number, or null into a Go string. Rationale:
// the client's jset coerces all-digit values to JSON ints (`"alias":42`,
// protocol.md §1.1) and hand-written inbox/spool lines can carry numbers where
// the schema expects strings (§4.5 `from:123`, `text:123`). A number keeps its
// source literal (42 -> "42", 4.0 -> "4.0"); null -> "". The framer's own
// `text:null -> "None"` behavior is a separate, frozen foreign-line quirk on the
// relay/local paths — this clean type deliberately does not reproduce it.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	switch {
	case len(b) == 0 || string(b) == "null":
		*f = ""
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
	default: // number / bool / other bare literal — keep the raw token
		*f = flexString(b)
	}
	return nil
}

// SendReq is the POST /send request body (relay main.go:145-151). channel/alias
// must pass ValidName; text is required non-empty; from defaults to "unknown"
// server-side; ts is optional (server fills RFC3339 when omitted, else stores the
// client value verbatim). Frozen wire contract (port-map.md §5 A1).
type SendReq struct {
	Channel string `json:"channel"`
	Alias   string `json:"alias"`
	From    string `json:"from"`
	Text    string `json:"text"`
	TS      string `json:"ts,omitempty"`
}

// PeersEntry is one value in the GET /peers response object, which is keyed
// "<channel>/<alias>" (relay main.go:39-43). connected is ws-attachment presence
// (not receipt); lastSeen is hub memory (zero time 0001-01-01T00:00:00Z after a
// relay restart until reconnect); queued is len(new/) in the Maildir spool.
type PeersEntry struct {
	Connected bool      `json:"connected"`
	LastSeen  time.Time `json:"lastSeen"`
	Queued    int       `json:"queued"`
}

// PeersResponse is the whole GET /peers body: channel/alias -> entry.
type PeersResponse map[string]PeersEntry
