package main

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testService() forwardService {
	return forwardService{
		label: "cbus-forward-demo-server-hook",
		argv:  []string{"/opt/my tools/cbus", "forward", "demo@server/hook", "--webhook-config", "/cfg/100%&$x"},
		env:   map[string]string{"CBUS_SITE_SERVER_URL": "https://bus.example.com/a?x=1&y=2", "CBUS_DIR": "/data/my bus"},
	}
}

func TestRenderSystemdUnitQuoting(t *testing.T) {
	out := renderSystemdUnit(testService())
	for _, want := range []string{
		`ExecStart="/opt/my tools/cbus" forward demo@server/hook --webhook-config "/cfg/100%%&$$x"`,
		`Environment="CBUS_DIR=/data/my bus"`,
		"Environment=CBUS_SITE_SERVER_URL=https://bus.example.com/a?x=1&y=2",
		"Restart=always",
		"StartLimitBurst=5",
		"WantedBy=default.target",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// the acceptance check: systemd itself accepts what is printed. The broken unit
// proves the verifier can run here, so a verifier that cannot start fails nothing silently.
func TestRenderedSystemdUnitVerifies(t *testing.T) {
	sa, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze not available")
	}
	dir := t.TempDir()
	verify := func(name, body string) error {
		path := filepath.Join(dir, name+".service")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(sa, "verify", path).CombinedOutput()
		if err != nil {
			t.Logf("%s: %v\n%s", name, err, out)
		}
		return err
	}
	if verify("control-good", "[Service]\nExecStart=/bin/true\n") != nil {
		t.Skip("systemd-analyze cannot verify units in this environment")
	}
	if verify("control-broken", "[Service]\nExecStart=relative/path\n") == nil {
		t.Fatal("the verifier accepted a broken unit, so its verdict means nothing")
	}
	svc := testService()
	svc.argv[0] = "/bin/true"
	if err := verify(svc.label, renderSystemdUnit(svc)); err != nil {
		t.Errorf("systemd rejected the printed unit: %v", err)
	}
}

func TestRenderLaunchdPlistIsWellFormed(t *testing.T) {
	out := renderLaunchdPlist(testService(), "/logs")
	dec := xml.NewDecoder(strings.NewReader(out))
	dec.Strict = false
	var text []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if c, ok := tok.(xml.CharData); ok {
			if s := strings.TrimSpace(string(c)); s != "" {
				text = append(text, s)
			}
		}
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"/opt/my tools/cbus", "/cfg/100%&$x", "https://bus.example.com/a?x=1&y=2", "com.cbus.forward.demo-server-hook", "/logs/cbus-forward-demo-server-hook.log"} {
		if !strings.Contains(joined, want) {
			t.Errorf("value %q did not round trip:\n%s", want, joined)
		}
	}
	if strings.Contains(out, "x=1&y") {
		t.Error("ampersand not escaped")
	}
}

func TestRenderForwardServiceRefusesOtherPlatforms(t *testing.T) {
	if _, err := renderForwardService("windows", testService(), "/logs"); err == nil {
		t.Error("windows accepted")
	}
}

func TestForwardServiceEnvCarriesTheRelaySettings(t *testing.T) {
	t.Setenv("CBUS_SITE_MY_HOST_URL", "https://bus.example.com")
	t.Setenv("CBUS_SITE_OTHER_URL", "https://other.example.com")
	t.Setenv("CBUS_DIR", "/data/bus")
	env := forwardServiceEnv("my-host")
	if env["CBUS_SITE_MY_HOST_URL"] == "" || env["CBUS_DIR"] != "/data/bus" {
		t.Errorf("env = %v", env)
	}
	if _, ok := env["CBUS_SITE_OTHER_URL"]; ok {
		t.Errorf("another host's setting leaked: %v", env)
	}
}

func TestPrintServiceKeepsSecretsOut(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "hook.cfg")
	if err := os.WriteFile(cfg, []byte("url=https://hooks.example.com/secret-path\nkey=secret-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CBUS_DIR", dir)
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	rc := runForward([]string{"demo@server/hook", "--webhook-config", cfg, "--timeout", "90s", "--per-min", "3", "--print-service"})
	w.Close()
	os.Stdout = stdout
	buf := make([]byte, 16*1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	for _, leak := range []string{"secret-key", "secret-path"} {
		if strings.Contains(out, leak) {
			t.Errorf("output leaks %q:\n%s", leak, out)
		}
	}
	for _, want := range []string{"1m30s", "demo@server/hook", cfg} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if rc := runForward([]string{"demo@server/hook", "--webhook-config", cfg, "--print-service", "--once"}); rc == 0 {
		t.Error("--print-service with --once accepted")
	}
	if rc := runForward([]string{"demo@server/hook", "--webhook-config", cfg, "--timeout", "soon"}); rc == 0 {
		t.Error("bad --timeout accepted")
	}
}
