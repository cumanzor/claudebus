package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const codexPermissionsUsage = "usage: cbus codex-permissions [--binary PATH] [--install [--path FILE] [--force]]"

// Preview by default. Neither skill installation nor selfupdate installs rules.
// Only replies need unattended shell execution; connection setup, recovery and
// lifecycle changes retain the session's normal exact-command approval path.
func runCodexPermissions(args []string) int {
	p, err := splitVerbArgs(args, map[string]bool{"--binary": true, "--path": true}, map[string]bool{"--install": true, "--force": true}, true)
	if err != nil {
		return die("%v (%s)", err, codexPermissionsUsage)
	}
	if err := noExtra(p.pos, 0, codexPermissionsUsage); err != nil {
		return die("%v", err)
	}
	if !p.flags["--install"] && (p.flags["--force"] || p.opts["--path"] != "") {
		return die("--path and --force require --install; without it this command only previews a rule")
	}
	binary := p.opts["--binary"]
	if binary == "" {
		binary, err = os.Executable()
		if err != nil {
			return die("locate cbus executable: %v", err)
		}
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return die("resolve cbus executable: %v", err)
	}
	// Execpolicy matches literal argv, not filesystem identity. Preserve the
	// supplied invocation path (including symlinks such as /tmp on macOS).
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() {
		return die("--binary must identify the installed cbus executable")
	}
	content := codexReplyRule(binary)
	if !p.flags["--install"] {
		fmt.Print(string(content))
		fmt.Fprintln(os.Stderr, "Preview only. This allows the exact executable's send command outside the Codex sandbox. Use --install to write the rule deliberately.")
		return 0
	}
	dst := p.opts["--path"]
	if dst == "" {
		skills, err := defaultCodexSkillsDir()
		if err != nil {
			return die("resolve Codex home: %v", err)
		}
		dst = filepath.Join(filepath.Dir(skills), "rules", "cbus.rules")
	}
	dst, err = filepath.Abs(dst)
	if err != nil {
		return die("resolve rule destination: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return die("create rules directory: %v", err)
	}
	res := installTrackedCodexAsset(dst, dst+".cbus-sha256", filepath.Base(dst), content, p.flags["--force"])
	code := reportAssets("Codex reply rule", filepath.Dir(dst), []assetResult{res})
	if code == 0 {
		fmt.Printf("Allows only %s send outside the command sandbox. New CLI sessions load the rule; existing sessions can use their normal exact-command approval.\n", binary)
	}
	return code
}

func codexReplyRule(binary string) []byte {
	program, _ := json.Marshal(binary)
	return []byte(fmt.Sprintf(`# cbus: optional permission for unattended bus replies.
# This exact executable's send command runs outside the Codex command sandbox.
# Does not allow connect, daemon management, abandonment, or arbitrary shell commands.
prefix_rule(
    pattern = [%s, "send"],
    decision = "allow",
    justification = "Allow the installed cbus executable to send bus replies",
)
`, program))
}
