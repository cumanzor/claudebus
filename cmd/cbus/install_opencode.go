package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"claudebus"
)

const (
	opencodeServerPlugin = "cbus-server.js"
	opencodeTUIPlugin    = "cbus-tui.js"
	opencodeTUIEntry     = "./" + opencodeTUIPlugin
	opencodeJoinCommand  = "commands/bus-join.md"
)

// OpenCode loads server plugins from <config>/plugins/ on its own; TUI plugins
// load only when listed in tui.json or tui.jsonc.
func installOpencodePlugins(fsys fs.FS, dir string, force bool) ([]assetResult, error) {
	read := func(name string) ([]byte, error) {
		return fs.ReadFile(fsys, "integrations/opencode/"+name)
	}
	server, err := read(opencodeServerPlugin)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", opencodeServerPlugin, err)
	}
	tui, err := read(opencodeTUIPlugin)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", opencodeTUIPlugin, err)
	}
	join, err := read(opencodeJoinCommand)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", opencodeJoinCommand, err)
	}
	for _, sub := range []string{"plugins", "commands"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", filepath.Join(dir, sub), err)
		}
	}
	return []assetResult{
		installAsset(filepath.Join(dir, "plugins", opencodeServerPlugin), "plugins/"+opencodeServerPlugin, server, force),
		installAsset(filepath.Join(dir, opencodeTUIPlugin), opencodeTUIPlugin, tui, force),
		installAsset(filepath.Join(dir, filepath.FromSlash(opencodeJoinCommand)), opencodeJoinCommand, join, force),
		registerOpencodeTUIPlugin(dir),
	}, nil
}

// registerOpencodeTUIPlugin creates tui.jsonc when the directory has no TUI
// config. An existing config is never rewritten: comments and layout are the
// user's, so a missing entry is reported with the line to add.
func registerOpencodeTUIPlugin(dir string) assetResult {
	for _, name := range []string{"tui.jsonc", "tui.json"} {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		res := assetResult{name: name}
		if err != nil {
			res.outcome, res.reason = "failed", "read: "+err.Error()
			return res
		}
		if strings.Contains(string(b), `"`+opencodeTUIEntry+`"`) {
			res.outcome = "up-to-date"
			return res
		}
		res.outcome, res.reason = "skipped", fmt.Sprintf(`add %q to its "plugin" list; the TUI plugin does not load until it is listed`, opencodeTUIEntry)
		return res
	}
	res := assetResult{name: "tui.jsonc"}
	if err := writeFileAtomic(filepath.Join(dir, "tui.jsonc"), []byte(`{ "plugin": ["`+opencodeTUIEntry+`"] }`+"\n")); err != nil {
		res.outcome, res.reason = "failed", err.Error()
		return res
	}
	res.outcome = "installed"
	return res
}

// defaultOpencodeConfigDir is OpenCode's global config directory.
func defaultOpencodeConfigDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

func runInstallOpencode(args []string) int {
	const use = "usage: cbus install-opencode [--path DIR] [--force]"
	dir, force, err := parseInstallArgs(args, use)
	if err != nil {
		return die("%v", err)
	}
	if dir == "" {
		if dir, err = defaultOpencodeConfigDir(); err != nil {
			return die("resolve OpenCode config dir: %v", err)
		}
	}
	results, err := installOpencodePlugins(claudebus.OpencodePlugins, dir, force)
	if err != nil {
		return die("%v", err)
	}
	return reportAssets("OpenCode plugins", dir, results)
}

// refreshOpencodePlugins keeps installed plugins in step with this binary's
// socket protocol and never creates an OpenCode config that does not exist.
func refreshOpencodePlugins(args []string) int {
	dir, err := defaultOpencodeConfigDir()
	if err != nil {
		return die("resolve OpenCode config dir: %v", err)
	}
	for _, rel := range []string{filepath.Join("plugins", opencodeServerPlugin), opencodeTUIPlugin} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return runInstallOpencode(append([]string{"--path", dir, "--force"}, args...))
		}
	}
	fmt.Printf("OpenCode plugins not installed in %s; skipped (cbus install-opencode installs them)\n", dir)
	return 0
}
