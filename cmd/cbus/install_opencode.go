package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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
		listed, err := opencodePluginListed(b, opencodeTUIEntry)
		switch {
		case err != nil:
			res.outcome, res.reason = "skipped", fmt.Sprintf(`cannot read its plugin list (%v); make sure %q is in its "plugin" list`, err, opencodeTUIEntry)
		case listed:
			res.outcome = "up-to-date"
		default:
			res.outcome, res.reason = "skipped", fmt.Sprintf(`add %q to its "plugin" list; the TUI plugin does not load until it is listed`, opencodeTUIEntry)
		}
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

// opencodePluginListed reports whether entry is in the config's active
// "plugin" array, at the top level or under "tui" as OpenCode also accepts; a
// mention in a comment or elsewhere does not count. Entries are a path, or a
// [path, options] pair.
func opencodePluginListed(jsonc []byte, entry string) (bool, error) {
	clean, err := stripJSONC(jsonc)
	if err != nil {
		return false, err
	}
	// keys are matched exactly; encoding/json would also accept "Plugin"
	var top map[string]json.RawMessage
	if err := json.Unmarshal(clean, &top); err != nil {
		return false, err
	}
	plugins := func(obj map[string]json.RawMessage) ([]json.RawMessage, error) {
		var list []json.RawMessage
		if raw, ok := obj["plugin"]; ok {
			if err := json.Unmarshal(raw, &list); err != nil {
				return nil, fmt.Errorf(`"plugin" is not a list: %w`, err)
			}
		}
		return list, nil
	}
	entries, err := plugins(top)
	if err != nil {
		return false, err
	}
	if raw, ok := top["tui"]; ok {
		var tui map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tui); err == nil {
			nested, err := plugins(tui)
			if err != nil {
				return false, err
			}
			entries = append(entries, nested...)
		}
	}
	for _, raw := range entries {
		var name string
		if json.Unmarshal(raw, &name) == nil && name == entry {
			return true, nil
		}
		var pair []json.RawMessage
		if json.Unmarshal(raw, &pair) == nil && len(pair) > 0 && json.Unmarshal(pair[0], &name) == nil && name == entry {
			return true, nil
		}
	}
	return false, nil
}

// stripJSONC removes comments, then trailing commas, outside strings, so
// encoding/json can read a JSONC file. The file itself is untouched.
func stripJSONC(b []byte) ([]byte, error) {
	clean, err := scanJSONC(b, true)
	if err != nil {
		return nil, err
	}
	return scanJSONC(clean, false)
}

// scanJSONC copies b, dropping comments on the first pass and trailing commas
// on the second; string contents are copied verbatim either way. An
// unterminated string or block comment is an error, never silently dropped.
func scanJSONC(b []byte, comments bool) ([]byte, error) {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(b) && b[j] != '"' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(b) {
				return nil, errors.New("unterminated string")
			}
			out = append(out, b[i:j+1]...)
			i = j
		case comments && c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, '\n')
			}
		case comments && c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			if i+1 >= len(b) {
				return nil, errors.New("unterminated block comment")
			}
			i++
		case !comments && c == ',':
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
				j++
			}
			if j < len(b) && (b[j] == ']' || b[j] == '}') {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out, nil
}
