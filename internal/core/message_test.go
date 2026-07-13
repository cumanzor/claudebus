package core

import (
	"encoding/json"
	"testing"
)

func TestDecodeMessageLenient(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Message
	}{
		{
			"plain string fields",
			`{"from":"c/o","to":"c/a","ts":"2026-07-13T00:00:00Z","text":"hi"}`,
			Message{From: "c/o", To: "c/a", TS: "2026-07-13T00:00:00Z", Text: "hi"},
		},
		{
			"key order agnostic",
			`{"text":"hi","ts":"t","to":"c/a","from":"c/o"}`,
			Message{From: "c/o", To: "c/a", TS: "t", Text: "hi"},
		},
		{
			// the "json.Number for legacy int aliases" tolerance: numbers where
			// strings are expected decode to their literal.
			"numbers coerce to string literals",
			`{"from":123,"to":"c/a","ts":"t","text":456}`,
			Message{From: "123", To: "c/a", TS: "t", Text: "456"},
		},
		{
			"null text becomes empty (no None leak)",
			`{"from":"x","to":"y","ts":"t","text":null}`,
			Message{From: "x", To: "y", TS: "t", Text: ""},
		},
		{
			"missing fields are zero",
			`{"text":"hi"}`,
			Message{Text: "hi"},
		},
		{
			"presence carries kind+event",
			`{"from":"c/a","to":"c/b","ts":"t","kind":"presence","event":"join","text":"joined c as a"}`,
			Message{From: "c/a", To: "c/b", TS: "t", Text: "joined c as a", Kind: "presence", Event: "join"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DecodeMessage([]byte(c.in))
			if err != nil {
				t.Fatalf("DecodeMessage(%s) error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("DecodeMessage(%s)\n got  %+v\n want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestDecodeMessageNonObjectErrors(t *testing.T) {
	for _, in := range []string{`[1,2,3]`, `"just a string"`, `123`, `not json`} {
		if _, err := DecodeMessage([]byte(in)); err == nil {
			t.Errorf("DecodeMessage(%q) = nil error, want error (not an object)", in)
		}
	}
}

func TestMessageMarshalShape(t *testing.T) {
	// plain line: kind/event omitted; key order matches the client's insertion
	// order {from,to,ts,text}. Bytes are canonical-Go (compact, no spaces) and
	// diverge from the python client's json.dumps spacing by design (ruling D8);
	// consumers parse JSON, never key order (§3.3).
	plain, err := json.Marshal(Message{From: "c/o", To: "c/a", TS: "t", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"from":"c/o","to":"c/a","ts":"t","text":"hi"}`; string(plain) != want {
		t.Errorf("plain marshal = %s, want %s", plain, want)
	}
	// presence line: kind+event present.
	pres, err := json.Marshal(Message{From: "c/a", To: "c/b", TS: "t", Text: "joined", Kind: "presence", Event: "join"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"from":"c/a","to":"c/b","ts":"t","text":"joined","kind":"presence","event":"join"}`; string(pres) != want {
		t.Errorf("presence marshal = %s, want %s", pres, want)
	}
}

func TestSendReqMarshal(t *testing.T) {
	// ts omitempty drops when unset; the rest are always present.
	b, err := json.Marshal(SendReq{Channel: "c", Alias: "a", From: "c/o", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"channel":"c","alias":"a","from":"c/o","text":"hi"}`; string(b) != want {
		t.Errorf("SendReq marshal = %s, want %s", b, want)
	}
}

func TestPeersResponseDecode(t *testing.T) {
	in := `{"dev/nuc":{"connected":true,"lastSeen":"2026-07-12T14:31:10.802809793-06:00","queued":3},` +
		`"x/y":{"connected":false,"lastSeen":"0001-01-01T00:00:00Z","queued":0}}`
	var pr PeersResponse
	if err := json.Unmarshal([]byte(in), &pr); err != nil {
		t.Fatal(err)
	}
	if e := pr["dev/nuc"]; !e.Connected || e.Queued != 3 || e.LastSeen.IsZero() {
		t.Errorf("dev/nuc = %+v, want connected/queued=3/non-zero lastSeen", e)
	}
	// the documented zero-time sentinel (relay restart, never reconnected) parses
	// to the Go zero value.
	if e := pr["x/y"]; e.Connected || e.Queued != 0 || !e.LastSeen.IsZero() {
		t.Errorf("x/y = %+v, want disconnected/queued=0/zero lastSeen", e)
	}
}
