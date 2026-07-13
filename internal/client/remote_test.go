package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claudebus/internal/core"
)

// seedCreds points XDG at a temp dir and writes credential fields there.
func seedCreds(t *testing.T, host string, fields map[string]string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for k, v := range fields {
		if err := (fileBackend{}).put(host, k, v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteSendPublicIncludesCF(t *testing.T) {
	seedCreds(t, "testhost", map[string]string{"token": "TOK", "cf-id": "CFID", "cf-secret": "CFSEC"})
	var auth, cfid, cfsec, ctype, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/send" {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		cfid = r.Header.Get("CF-Access-Client-Id")
		cfsec = r.Header.Get("CF-Access-Client-Secret")
		ctype = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"ok":true,"id":"x"}`))
	}))
	defer srv.Close()
	t.Setenv("CBUS_RELAY_LOCAL_URL", "http://127.0.0.1:1") // refused -> probe fails -> public
	t.Setenv("CBUS_SITE_TESTHOST_URL", srv.URL)

	ep, err := ResolveRemote(NewFileCredStore(), "testhost")
	if err != nil || ep.Local {
		t.Fatalf("ResolveRemote = %+v, %v; want public", ep, err)
	}
	if err := RemoteSend(ep, core.SendReq{Channel: "c", Alias: "a", From: "c/o", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer TOK" || cfid != "CFID" || cfsec != "CFSEC" {
		t.Errorf("headers auth=%q cf-id=%q cf-secret=%q", auth, cfid, cfsec)
	}
	if ctype != "application/json" {
		t.Errorf("Content-Type = %q", ctype)
	}
	var req core.SendReq
	if json.Unmarshal([]byte(body), &req); req != (core.SendReq{Channel: "c", Alias: "a", From: "c/o", Text: "hi"}) {
		t.Errorf("body = %s", body)
	}
}

func TestRemoteSendLocalSkipsCF(t *testing.T) {
	seedCreds(t, "testhost", map[string]string{"token": "TOK", "cf-id": "CFID", "cf-secret": "CFSEC"})
	var auth, cfid string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte("ok\n"))
		case "/send":
			auth = r.Header.Get("Authorization")
			cfid = r.Header.Get("CF-Access-Client-Id")
			_, _ = w.Write([]byte(`{"ok":true,"id":"x"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("CBUS_RELAY_LOCAL_URL", srv.URL) // healthz ok -> local

	ep, err := ResolveRemote(NewFileCredStore(), "testhost")
	if err != nil || !ep.Local {
		t.Fatalf("ResolveRemote = %+v, %v; want local", ep, err)
	}
	if err := RemoteSend(ep, core.SendReq{Channel: "c", Alias: "a", From: "x", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer TOK" {
		t.Errorf("Authorization = %q", auth)
	}
	if cfid != "" {
		t.Errorf("local mode must NOT send CF headers, got CF-Access-Client-Id=%q", cfid)
	}
}

func TestResolveRemoteMissingCreds(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CBUS_RELAY_LOCAL_URL", "http://127.0.0.1:1")
	t.Setenv("CBUS_SITE_TESTHOST_URL", "https://example.invalid")

	if _, err := ResolveRemote(NewFileCredStore(), "testhost"); err == nil || !strings.Contains(err.Error(), "no relay token") {
		t.Errorf("no token: err = %v", err)
	}
	_ = (fileBackend{}).put("testhost", "token", "TOK")
	if _, err := ResolveRemote(NewFileCredStore(), "testhost"); err == nil || !strings.Contains(err.Error(), "no cf-id") {
		t.Errorf("no cf-id: err = %v", err)
	}
}

func TestRemoteListDecodes(t *testing.T) {
	seedCreds(t, "testhost", map[string]string{"token": "TOK", "cf-id": "I", "cf-secret": "S"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/peers" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer TOK" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"dev/nuc":{"connected":true,"lastSeen":"2026-07-12T14:31:10.802809793-06:00","queued":2}}`))
	}))
	defer srv.Close()
	t.Setenv("CBUS_RELAY_LOCAL_URL", "http://127.0.0.1:1")
	t.Setenv("CBUS_SITE_TESTHOST_URL", srv.URL)

	ep, _ := ResolveRemote(NewFileCredStore(), "testhost")
	peers, err := RemoteList(ep)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := peers["dev/nuc"]; !ok || !e.Connected || e.Queued != 2 || e.LastSeen.IsZero() {
		t.Errorf("peers[dev/nuc] = %+v (ok=%v)", peers["dev/nuc"], ok)
	}
}
