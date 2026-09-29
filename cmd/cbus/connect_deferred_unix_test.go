//go:build darwin || linux

package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claudebus/internal/client"
)

// A short store path keeps the control socket under the unix socket length limit.
func fakeConnectDaemon(t *testing.T, response map[string]any) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cbd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("CBUS_DIR", dir)
	if err := os.MkdirAll(client.DaemonDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(client.DaemonDir(), "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(response)
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestConnectReportsDeferredStepOnStderrAndExitsZero(t *testing.T) {
	deferred := "snapshot presence recipient demo/broken inbox"
	fakeConnectDaemon(t, map[string]any{"id": "c1", "harness": "codex", "channel": "demo", "alias": "joiner", "threadId": "t1", "state": "socket-ready", "deferred": deferred})
	var state client.ConnectionState
	if err := client.DaemonCall("POST", "/connect", nil, &state); err != nil {
		t.Fatal(err)
	}
	want := "cbus: connected as demo/joiner, but a step after registration failed and the daemon retries it: " + deferred + " (cbus connection status demo/joiner)\n"
	for _, asJSON := range []bool{false, true} {
		var rc int
		var out string
		errOut := captureStderr(t, func() { out = captureStdout(t, func() { rc = reportConnect(state, asJSON) }) })
		if rc != 0 || errOut != want {
			t.Fatalf("json=%t rc=%d stderr=%q", asJSON, rc, errOut)
		}
		if asJSON && !strings.Contains(out, `"deferred":"`+deferred+`"`) {
			t.Fatalf("json output lost the deferred field: %s", out)
		}
	}
	state.Deferred = ""
	if errOut := captureStderr(t, func() { captureStdout(t, func() { reportConnect(state, true) }) }); errOut != "" {
		t.Fatalf("clean connect wrote stderr: %q", errOut)
	}
}
