package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"claudebus/internal/client"
)

type forwardService struct {
	label string
	argv  []string
	env   map[string]string
}

// a service does not inherit the shell, so the settings that locate the relay and the store go with it
func forwardServiceEnv(host string) map[string]string {
	env := map[string]string{}
	for _, name := range []string{client.SiteEnvVar(host), "CBUS_DIR", "CBUS_RELAY_LOCAL_URL", "CBUS_HOST", "XDG_CONFIG_HOME"} {
		if v := os.Getenv(name); v != "" {
			env[name] = v
		}
	}
	return env
}

func forwardServiceLabel(channel, host, alias string) string {
	return "cbus-forward-" + channel + "-" + host + "-" + alias
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// $$ is an escape only on Exec lines; Environment= would keep both dollars
func systemdQuoteEnv(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if !strings.ContainsAny(s, " \t\"'\\;") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func systemdQuoteExec(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if !strings.ContainsAny(s, " \t\"'\\$;") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "$", "$$")
	return `"` + s + `"`
}

// renderSystemdUnit is a user unit, so it reads the same credentials and store as the shell that printed it.
func renderSystemdUnit(s forwardService) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# install: save as ~/.config/systemd/user/%s.service, then\n", s.label)
	b.WriteString("#   systemctl --user daemon-reload\n")
	fmt.Fprintf(&b, "#   systemctl --user enable --now %s\n", s.label)
	b.WriteString("# to run without a login session: loginctl enable-linger $USER\n")
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=cbus forward: %s\n", s.label)
	b.WriteString("After=network-online.target\nWants=network-online.target\n")
	b.WriteString("StartLimitIntervalSec=600\nStartLimitBurst=5\n\n[Service]\n")
	for _, k := range sortedKeys(s.env) {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuoteEnv(k+"="+s.env[k]))
	}
	quoted := make([]string, len(s.argv))
	for i, a := range s.argv {
		quoted[i] = systemdQuoteExec(a)
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(quoted, " "))
	b.WriteString("Restart=always\nRestartSec=10\nUMask=0077\nNoNewPrivileges=yes\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// renderLaunchdPlist is a LaunchAgent: it runs in the login session, where the relay token in the Keychain is readable.
func renderLaunchdPlist(s forwardService, logDir string) string {
	label := "com.cbus.forward." + strings.TrimPrefix(s.label, "cbus-forward-")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	// a name may hold "--", which XML forbids inside a comment, so the label stays out of it
	b.WriteString("<!-- install: save under ~/Library/LaunchAgents as <Label>.plist, then\n")
	b.WriteString("     launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/<Label>.plist -->\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "  <key>Label</key><string>%s</string>\n", xmlText(label))
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, a := range s.argv {
		fmt.Fprintf(&b, "    <string>%s</string>\n", xmlText(a))
	}
	b.WriteString("  </array>\n")
	if len(s.env) > 0 {
		b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
		for _, k := range sortedKeys(s.env) {
			fmt.Fprintf(&b, "    <key>%s</key><string>%s</string>\n", xmlText(k), xmlText(s.env[k]))
		}
		b.WriteString("  </dict>\n")
	}
	b.WriteString("  <key>RunAtLoad</key><true/>\n  <key>KeepAlive</key><true/>\n  <key>ThrottleInterval</key><integer>10</integer>\n")
	log := filepath.Join(logDir, s.label+".log")
	fmt.Fprintf(&b, "  <key>StandardErrorPath</key><string>%s</string>\n", xmlText(log))
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func renderForwardService(goos string, s forwardService, logDir string) (string, error) {
	switch goos {
	case "linux":
		return renderSystemdUnit(s), nil
	case "darwin":
		return renderLaunchdPlist(s, logDir), nil
	default:
		return "", fmt.Errorf("--print-service supports linux and macOS, not %s", goos)
	}
}
