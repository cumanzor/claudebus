package client

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDaemonBindingRequiresExplicitAbsolutePaths(t *testing.T) {
	for _, field := range []string{"binary", "home", "cwd", "sqlite", "userhome"} {
		t.Run(field, func(t *testing.T) {
			d, q, req := daemonFixture(t)
			switch field {
			case "binary":
				req.Config.Binary = "codex"
			case "home":
				req.Config.Home = ""
			case "cwd":
				req.Config.Cwd = "."
			case "sqlite":
				req.Config.SQLiteHome = ""
			case "userhome":
				req.Config.UserHome = ""
			}
			if _, err := d.connect(req); err == nil || !strings.Contains(err.Error(), "reconnect from the original current CLI") {
				t.Fatalf("missing binding accepted: %v", err)
			}
			if q.opens != 0 || len(d.statusSnapshots()) != 0 {
				t.Fatal("invalid binding started a sidecar or claimed a connection")
			}
		})
	}
}

func TestDaemonBindingLegacyReconnectPreservesPendingEpoch(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	appendDaemonMessage(t, c, "", "unknown enqueue")
	q.enqueueErr = errors.New("response lost")
	if err := d.deliver(c); err == nil || c.Pending == nil {
		t.Fatalf("fixture did not retain a pending attempt: %v", err)
	}
	pending := *c.Pending
	c.Config.SQLiteHome, c.Config.UserHome = "", ""
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	d = reloadDaemonFixture(t, q)
	legacy := d.snapshot(c.ID)
	if legacy.State != "binding-required" || !strings.Contains(legacy.Error, "reconnect") {
		t.Fatalf("legacy storage was guessed: %+v", legacy)
	}
	opens, reads, calls := q.opens, q.finds, len(q.calls)
	d.schedule()
	schedulerWait(t, "legacy presence-only worker finished", func() bool { return len(d.slots) == 0 })
	if len(d.slots) != 0 || q.opens != opens || q.finds != reads || len(q.calls) != calls {
		t.Fatal("unbound legacy connection attempted native delivery")
	}
	rebound, err := d.connect(req)
	if err != nil || rebound.ID != c.ID || rebound.Offset != c.Offset || rebound.State != "uncertain" || !reflect.DeepEqual(rebound.Pending, &pending) || rebound.Config != req.Config {
		t.Fatalf("binding upgrade lost durable epoch/progress: %+v, %v", rebound, err)
	}
	if len(q.calls) != calls || q.finds != reads {
		t.Fatal("binding upgrade retried or probed the pending message")
	}
}

func TestDaemonBindingRefusesStoreRedirectAndRefreshesProcess(t *testing.T) {
	d, q, req := daemonFixture(t)
	c := mustDaemonConnect(t, d, req)
	before := cloneConnection(c)
	changed := req
	changed.Config.SQLiteHome = filepath.Join(req.Config.Home, "other-store")
	if _, err := d.connect(changed); err == nil || !strings.Contains(err.Error(), "refusing to redirect") {
		t.Fatalf("existing epoch redirected: %v", err)
	}
	if !reflect.DeepEqual(c, before) || q.opens != 1 || q.closes != 0 {
		t.Fatal("store mismatch changed the existing binding")
	}
	changed = req
	changed.Config.Binary = filepath.Join(req.Config.Home, "new-codex")
	changed.Config.UserHome = filepath.Join(req.Config.Home, "other-user-home")
	changed.Config.Cwd = filepath.Join(req.Config.Home, "other-cwd")
	got, err := d.connect(changed)
	if err != nil || got.ID != c.ID || got.Config != changed.Config || q.opens != 2 || q.closes != 1 {
		t.Fatalf("process binding did not refresh its sidecar: %+v, %v, opens=%d closes=%d", got, err, q.opens, q.closes)
	}
	changed.Config.BindingSource, changed.Config.RuntimeVersion = "runtime-proof", "0.test"
	if _, err := d.connect(changed); err != nil || q.opens != 2 || q.closes != 1 {
		t.Fatalf("informational binding unnecessarily replaced the sidecar: %v", err)
	}
	if err := d.disconnect("dev/worker"); err != nil {
		t.Fatal(err)
	}
	changed.Config.SQLiteHome = filepath.Join(req.Config.Home, "other-store")
	if _, err := d.connect(changed); err == nil {
		t.Fatal("disconnect allowed implicit adoption of a different queue store")
	}
}

func TestDaemonBindingMigratesLegacySymlinkHome(t *testing.T) {
	d, _, req := daemonFixture(t)
	if err := os.MkdirAll(req.Config.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(req.Config.Home)
	if err != nil {
		t.Fatal(err)
	}
	req.Config.Home, req.Config.SQLiteHome = canonical, canonical
	c := mustDaemonConnect(t, d, req)
	oldHome := filepath.Join(req.Config.Cwd, "old-codex-home-link")
	if err := os.Symlink(canonical, oldHome); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	c.Config.Home, c.Config.SQLiteHome, c.Config.UserHome = oldHome, "", ""
	if err := d.save(c); err != nil {
		t.Fatal(err)
	}
	rebound, err := d.connect(req)
	if err != nil || rebound.ID != c.ID || rebound.Config.Home != canonical || len(d.statusSnapshots()) != 1 {
		t.Fatalf("legacy symlink home could not upgrade its existing registration: %+v %v", rebound, err)
	}
}

func TestDaemonHealthReportsRunningVersionAndStopFencesInstance(t *testing.T) {
	d := newBusDaemon()
	defer d.cancel()
	d.start, d.version = "original-start", "candidate-version"
	stopped := 0
	h := d.handler(func() { stopped++ })
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/health", nil))
	var health struct {
		PID      int    `json:"pid"`
		Start    string `json:"start"`
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &health); err != nil || health.PID != os.Getpid() || health.Start != d.start || health.Version != d.version || health.Protocol != DaemonProtocolVersion {
		t.Fatalf("health does not identify executing daemon: %+v, %v", health, err)
	}
	for _, body := range []string{`{"pid":1,"start":"original-start"}`, `{"pid":0,"start":"original-start"}`, `{"pid":1}`} {
		r = httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("POST", "/stop", strings.NewReader(body)))
		if r.Code != 409 || stopped != 0 {
			t.Fatalf("stale/incomplete stop fence accepted: %s => %d", body, r.Code)
		}
	}
	for _, body := range []string{"", string(mustBindingJSON(t, map[string]any{"pid": os.Getpid(), "start": d.start}))} {
		r = httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("POST", "/stop", strings.NewReader(body)))
		if r.Code != 200 {
			t.Fatalf("valid stop rejected: %d %s", r.Code, r.Body.String())
		}
	}
	if stopped != 2 {
		t.Fatalf("stop calls=%d", stopped)
	}
}

func mustBindingJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
