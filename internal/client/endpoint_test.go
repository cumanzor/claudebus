package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSiteEnvVar(t *testing.T) {
	cases := map[string]string{
		"nuc":    "CBUS_SITE_NUC_URL",
		"my-nas": "CBUS_SITE_MY_NAS_URL",
		"my.nas": "CBUS_SITE_MY_NAS_URL", // collides with my-nas — preserved quirk
		"MBP":    "CBUS_SITE_MBP_URL",
		"h.":     "CBUS_SITE_H_URL", // trailing dot -> _, then the ONE trailing _ stripped
		"a.b.c":  "CBUS_SITE_A_B_C_URL",
	}
	for host, want := range cases {
		if got := siteEnvVar(host); got != want {
			t.Errorf("siteEnvVar(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestSiteURL(t *testing.T) {
	// no built-in hosts: an unset override is a hard error, even for nuc
	// (the promoted soft->hard delta)
	var uh *UnknownHostError
	if _, err := SiteURL("nuc"); !errors.As(err, &uh) {
		t.Errorf("SiteURL(nuc) with no override should be *UnknownHostError, got %v", err)
	}
	_, err := SiteURL("mystery")
	if !errors.As(err, &uh) {
		t.Fatalf("SiteURL(mystery) err = %v, want *UnknownHostError", err)
	}
	if uh.EnvVar != "CBUS_SITE_MYSTERY_URL" {
		t.Errorf("UnknownHostError.EnvVar = %q", uh.EnvVar)
	}
	// env override resolves the host
	t.Setenv("CBUS_SITE_NUC_URL", "https://override.example")
	if u, err := SiteURL("nuc"); err != nil || u != "https://override.example" {
		t.Errorf("SiteURL(nuc) override = %q, %v", u, err)
	}
}

func TestWSURL(t *testing.T) {
	cases := map[string]string{
		"https://bus.example.com": "wss://bus.example.com",
		"http://127.0.0.1:8090":   "ws://127.0.0.1:8090",
		"ftp://x":                 "", // any other scheme -> empty (quirk)
		"":                        "",
		"bus.example.com":         "", // scheme-less -> empty
	}
	for in, want := range cases {
		if got := WSURL(in); got != want {
			t.Errorf("WSURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolveFrontDoor covers local-vs-public SELECTION in-process (per the m5
// hermeticity ruling: never bind :8090; drive the probe with httptest).
func TestResolveFrontDoor(t *testing.T) {
	// no built-in host: the public-mode branch resolves nuc via its env override
	t.Setenv("CBUS_SITE_NUC_URL", "https://bus.example.com")
	healthz := func(body string, status int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}

	t.Run("exact ok -> local", func(t *testing.T) {
		srv := healthz("ok\n", 200)
		defer srv.Close()
		t.Setenv("CBUS_RELAY_LOCAL_URL", srv.URL)
		fd, err := ResolveFrontDoor("nuc")
		if err != nil || !fd.Local || fd.Base != srv.URL {
			t.Fatalf("ResolveFrontDoor = %+v, %v; want local at %s", fd, err, srv.URL)
		}
	})

	t.Run("ok on its own line among others -> local", func(t *testing.T) {
		srv := healthz("banner\nok\n", 200)
		defer srv.Close()
		t.Setenv("CBUS_RELAY_LOCAL_URL", srv.URL)
		fd, _ := ResolveFrontDoor("nuc")
		if !fd.Local {
			t.Fatalf("a line exactly 'ok' should select local: %+v", fd)
		}
	})

	t.Run("non-ok body -> public", func(t *testing.T) {
		srv := healthz("okay\n", 200) // "okay" != "ok"
		defer srv.Close()
		t.Setenv("CBUS_RELAY_LOCAL_URL", srv.URL)
		fd, err := ResolveFrontDoor("nuc")
		if err != nil || fd.Local || fd.Base != "https://bus.example.com" {
			t.Fatalf("ResolveFrontDoor = %+v, %v; want public", fd, err)
		}
	})

	t.Run(">=400 status -> public", func(t *testing.T) {
		srv := healthz("ok\n", 503) // curl -f fails on >=400
		defer srv.Close()
		t.Setenv("CBUS_RELAY_LOCAL_URL", srv.URL)
		fd, _ := ResolveFrontDoor("nuc")
		if fd.Local {
			t.Fatalf("a >=400 healthz must not select local: %+v", fd)
		}
	})

	t.Run("redirect is not followed -> public", func(t *testing.T) {
		// an "ok"-serving target the probe must NOT be redirected to (F1)
		target := healthz("ok\n", 200)
		defer target.Close()
		redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/healthz", http.StatusFound)
		}))
		defer redir.Close()
		t.Setenv("CBUS_RELAY_LOCAL_URL", redir.URL)
		fd, _ := ResolveFrontDoor("nuc")
		if fd.Local {
			t.Fatalf("a loopback 302 must not be followed to an ok-serving host: %+v", fd)
		}
	})

	t.Run("slow probe times out -> public", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(600 * time.Millisecond) // > the 300ms probe timeout
			_, _ = w.Write([]byte("ok\n"))
		}))
		defer srv.Close()
		t.Setenv("CBUS_RELAY_LOCAL_URL", srv.URL)
		start := time.Now()
		fd, _ := ResolveFrontDoor("nuc")
		if fd.Local {
			t.Fatalf("a probe that outlasts the timeout must select public: %+v", fd)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("probe took %v, expected ~300ms timeout", elapsed)
		}
	})
}
