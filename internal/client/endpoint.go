// Package client holds the cbus-go client-side logic — endpoint resolution,
// address parsing, the remote HTTP client, credential access, and identity —
// that the cmd/cbus binary drives. It reuses internal/core for the shared wire
// structs, name validation, and framer.
package client

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultLocalURL is the loopback front door probed for local mode. Overridable
// via CBUS_RELAY_LOCAL_URL (bin/cbus:149).
const DefaultLocalURL = "http://127.0.0.1:8090"

// probeTimeout bounds the front-door /healthz probe (bin/cbus:150, `curl -m 0.3`).
const probeTimeout = 300 * time.Millisecond

// LocalURL is the loopback relay base: CBUS_RELAY_LOCAL_URL or DefaultLocalURL.
func LocalURL() string {
	if u := os.Getenv("CBUS_RELAY_LOCAL_URL"); u != "" {
		return u
	}
	return DefaultLocalURL
}

// siteEnvVar builds the CBUS_SITE_<HOST>_URL override name exactly as the bash
// client does (bin/cbus:137): uppercase the host, map every non-[A-Z0-9] byte to
// '_', then strip ONE trailing '_'. Distinct hosts can collide after mangling
// (e.g. "my-nas" and "my.nas") — a preserved quirk (protocol.md §12.1).
func siteEnvVar(host string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(host) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "CBUS_SITE_" + strings.TrimSuffix(b.String(), "_") + "_URL"
}

// SiteURL resolves a host to its public relay base: the CBUS_SITE_<HOST>_URL
// override wins, else the built-in table (only nuc), else an error.
//
// Divergence from bash (approved P1 delta): the bash client's `die` for an
// unknown host fires inside a command substitution and is only a non-fatal
// stderr message, so the command limps on with an empty base; the port promotes
// it to a hard error (protocol.md §12.1 quirk; port-map soft->hard ruling).
func SiteURL(host string) (string, error) {
	v := siteEnvVar(host)
	if u := os.Getenv(v); u != "" {
		return u, nil
	}
	switch host {
	case "nuc":
		return "https://bus.example.com", nil
	default:
		return "", &UnknownHostError{Host: host, EnvVar: v}
	}
}

// UnknownHostError is returned by SiteURL for a host with no built-in or override.
type UnknownHostError struct {
	Host   string
	EnvVar string
}

func (e *UnknownHostError) Error() string {
	return "unknown relay host " + strconv.Quote(e.Host) + " (set " + e.EnvVar + ")"
}

// FrontDoor is the resolved relay endpoint for a host: whether the loopback
// probe selected local mode (no CF Access headers) and the base URL to use.
type FrontDoor struct {
	Local bool   // true => loopback, omit CF Access headers
	Base  string // e.g. http://127.0.0.1:8090 or https://bus.example.com
}

// ResolveFrontDoor picks the front door for a host with zero config: probe the
// loopback /healthz; if it answers with an exact "ok" line, use local mode
// (loopback base, no CF Access), else public mode (site base). A session running
// ON the relay host reaches it at loopback; everyone else goes through the public
// hostname (bin/cbus:146-155). The probe is trust-by-port: anything answering
// "ok" on the loopback base is believed.
func ResolveFrontDoor(host string) (FrontDoor, error) {
	lu := LocalURL()
	if probeLocalOK(lu) {
		return FrontDoor{Local: true, Base: lu}, nil
	}
	site, err := SiteURL(host)
	if err != nil {
		return FrontDoor{}, err
	}
	return FrontDoor{Local: false, Base: site}, nil
}

// probeLocalOK GETs <base>/healthz with a 0.3s timeout and reports whether any
// response line is exactly "ok" (bash: `curl -fsS -m 0.3 … | grep -q '^ok$'`).
// A >=400 status (curl -f) or any transport error is not-ok.
func probeLocalOK(base string) bool {
	c := &http.Client{Timeout: probeTimeout}
	resp, err := c.Get(strings.TrimRight(base, "/") + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	for _, line := range strings.Split(string(body), "\n") {
		if line == "ok" {
			return true
		}
	}
	return false
}

// WSURL swaps an http(s) base to its ws(s) form; any other scheme yields "" (a
// preserved quirk — bin/cbus:157-162, protocol.md §12.2).
func WSURL(u string) string {
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	default:
		return ""
	}
}
