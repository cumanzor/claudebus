package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"claudebus/internal/core"
)

// Explicit HTTP timeouts the bash client lacks (only its 0.3s healthz probe is
// bounded). connectTimeout bounds the TCP+TLS dial; totalTimeout bounds the whole
// request. NO retry anywhere: POST /send is non-idempotent and the relay mints no
// idempotency key, so a retry would duplicate — and Go's transport never retries a
// POST regardless (documented invariant).
const (
	connectTimeout = 4 * time.Second
	totalTimeout   = 20 * time.Second
)

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: totalTimeout,
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: connectTimeout}).DialContext,
			TLSHandshakeTimeout: connectTimeout,
		},
	}
}

// RemoteEndpoint is a resolved front door plus the auth headers for a host.
type RemoteEndpoint struct {
	FrontDoor
	headers map[string]string
}

// Mode is "local" or "public" (for user-facing messages).
func (e RemoteEndpoint) Mode() string {
	if e.Local {
		return "local"
	}
	return "public"
}

// ResolveRemote builds the front door + auth headers for host: always the Bearer
// token; in public mode also the CF-Access service-token pair (local mode skips
// them — a loopback session is behind the tunnel). Missing credentials are hard
// errors pointing at `cbus auth set` (matching bin/cbus:225-234).
func ResolveRemote(store *CredStore, host string) (RemoteEndpoint, error) {
	fd, err := ResolveFrontDoor(host)
	if err != nil {
		return RemoteEndpoint{}, err
	}
	token, _ := store.Get(host, "token")
	if token == "" {
		return RemoteEndpoint{}, fmt.Errorf("no relay token for %q — run: cbus auth set %s --token -", host, host)
	}
	h := map[string]string{"Authorization": "Bearer " + token}
	if !fd.Local {
		cfid, _ := store.Get(host, "cf-id")
		if cfid == "" {
			return RemoteEndpoint{}, fmt.Errorf("no cf-id for %q — run: cbus auth set %s --cf-id -", host, host)
		}
		cfsec, _ := store.Get(host, "cf-secret")
		if cfsec == "" {
			return RemoteEndpoint{}, fmt.Errorf("no cf-secret for %q — run: cbus auth set %s --cf-secret -", host, host)
		}
		h["CF-Access-Client-Id"] = cfid
		h["CF-Access-Client-Secret"] = cfsec
	}
	return RemoteEndpoint{FrontDoor: fd, headers: h}, nil
}

func (e RemoteEndpoint) apply(req *http.Request) {
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}
}

// RemoteSend POSTs a message to <base>/send. No retry (see the timeout note).
func RemoteSend(e RemoteEndpoint, req core.SendReq) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequest(http.MethodPost, e.Base+"/send", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	e.apply(httpReq)
	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return fmt.Errorf("relay send failed (%s %s): %w", e.Mode(), e.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("relay send failed (%s %s): %d %s", e.Mode(), e.Base, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// RemoteTailSpec builds the Monitor ws arm-spec a remote `cbus tail` prints (A5):
// the base's scheme is swapped to ws/wss and the relay token rides the
// subprotocol (the ws leg is subprotocol-only — no CF Access). Byte-identical to
// bin/cbus:285-292; the em-dash before "expected" is U+2014.
func RemoteTailSpec(base, token, channel, host, alias string) string {
	url := WSURL(base) + "/tail?channel=" + channel + "&alias=" + alias
	addr := channel + "@" + host
	return "remote listening is armed with the Monitor tool's ws source (not a command):\n" +
		"  url:         " + url + "\n" +
		"  protocols:   [\"bearer.cbus." + token + "\"]\n" +
		"  description: cbus:" + addr + "/" + alias + "   (persistent: true)\n" +
		"identity recorded for THIS session: sends to " + addr + "/* default to from=" + addr + "/" + alias + "\n" +
		"note: the protocols entry carries the relay token — expected; it IS the auth.\n"
}

// RemotePrune POSTs to <base>/prune (optionally scoped to one channel) and returns
// the "<channel>/<alias>" keys the relay removed from its spool. Non-idempotent in
// name only — re-running is safe (already-gone peers just aren't in the result).
func RemotePrune(e RemoteEndpoint, channel string) ([]string, error) {
	u := e.Base + "/prune"
	if channel != "" {
		u += "?channel=" + url.QueryEscape(channel)
	}
	httpReq, err := http.NewRequest(http.MethodPost, u, nil)
	if err != nil {
		return nil, err
	}
	e.apply(httpReq)
	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("relay prune failed (%s %s): %w", e.Mode(), e.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("relay prune failed (%s %s): %d %s", e.Mode(), e.Base, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out core.PruneResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Pruned, nil
}

// RemoteList GETs <base>/peers into a core.PeersResponse.
func RemoteList(e RemoteEndpoint) (core.PeersResponse, error) {
	httpReq, err := http.NewRequest(http.MethodGet, e.Base+"/peers", nil)
	if err != nil {
		return nil, err
	}
	e.apply(httpReq)
	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("relay list failed (%s %s): %w", e.Mode(), e.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay list failed (%s %s): status %d", e.Mode(), e.Base, resp.StatusCode)
	}
	var peers core.PeersResponse
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return nil, err
	}
	return peers, nil
}
