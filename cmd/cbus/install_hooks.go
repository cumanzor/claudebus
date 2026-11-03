package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const installHooksUsage = "usage: cbus install-hooks [--path FILE] [--dry-run]"

// claudeHook is one settings entry the coordinator features need.
type claudeHook struct {
	event, matcher, verb string
}

var claudeHooks = []claudeHook{
	{event: "PermissionDenied", matcher: "*", verb: "hook-denied"},
	{event: "Stop", verb: "hook-regressed"},
}

// settings keys keep their order and numbers keep their spelling, so a merge
// only adds entries instead of re-sorting the user's file.
type jsonMember struct {
	key   string
	value any
}
type jsonObject []jsonMember

func (o jsonObject) get(key string) (any, int) {
	for i, m := range o {
		if m.key == key {
			return m.value, i
		}
	}
	return nil, -1
}

func (o jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := marshalNoEscape(m.key)
		if err != nil {
			return nil, err
		}
		v, err := marshalNoEscape(m.value)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func marshalNoEscape(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func decodeJSONValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		o := jsonObject{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeJSONValue(dec)
			if err != nil {
				return nil, err
			}
			o = append(o, jsonMember{k.(string), v})
		}
		_, err = dec.Token()
		return o, err
	default:
		a := []any{}
		for dec.More() {
			v, err := decodeJSONValue(dec)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err = dec.Token()
		return a, err
	}
}

func parseSettings(raw []byte) (jsonObject, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return jsonObject{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing content after the settings object")
	}
	o, ok := v.(jsonObject)
	if !ok {
		return nil, errors.New("settings is not a JSON object")
	}
	return o, nil
}

// any path to this cbus counts, so a hand-written entry is not added twice
func hookCommandPresent(groups []any, verb, cbusPath string) bool {
	for _, g := range groups {
		group, ok := g.(jsonObject)
		if !ok {
			continue
		}
		hooks, _ := group.get("hooks")
		list, _ := hooks.([]any)
		for _, h := range list {
			hook, ok := h.(jsonObject)
			if !ok {
				continue
			}
			c, _ := hook.get("command")
			if strings.Contains(fmt.Sprint(c), cbusPath+" "+verb) {
				return true
			}
			fields := strings.Fields(fmt.Sprint(c))
			for i := 1; i < len(fields); i++ {
				if fields[i] == verb && filepath.Base(strings.Trim(fields[i-1], `"'`)) == "cbus" {
					return true
				}
			}
		}
	}
	return false
}

func missingClaudeHooks(settings jsonObject, cbusPath string) ([]claudeHook, error) {
	var hooksObj jsonObject
	if v, i := settings.get("hooks"); i >= 0 {
		o, ok := v.(jsonObject)
		if !ok {
			return nil, errors.New(`"hooks" is not an object`)
		}
		hooksObj = o
	}
	var missing []claudeHook
	for _, h := range claudeHooks {
		var groups []any
		if v, i := hooksObj.get(h.event); i >= 0 {
			a, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf(`"hooks.%s" is not a list`, h.event)
			}
			groups = a
		}
		if !hookCommandPresent(groups, h.verb, cbusPath) {
			missing = append(missing, h)
		}
	}
	return missing, nil
}

// mergeClaudeHooks adds what is missing and returns the new file; existing entries are untouched.
func mergeClaudeHooks(raw []byte, cbusPath string) ([]byte, []claudeHook, error) {
	settings, err := parseSettings(raw)
	if err != nil {
		return nil, nil, err
	}
	missing, err := missingClaudeHooks(settings, cbusPath)
	if err != nil || len(missing) == 0 {
		return nil, nil, err
	}
	hooksVal, hooksAt := settings.get("hooks")
	hooksObj, _ := hooksVal.(jsonObject)
	for _, h := range missing {
		group := jsonObject{}
		if h.matcher != "" {
			group = append(group, jsonMember{"matcher", h.matcher})
		}
		group = append(group, jsonMember{"hooks", []any{jsonObject{
			{"type", "command"},
			{"command", cbusPath + " " + h.verb},
		}}})
		v, at := hooksObj.get(h.event)
		if at >= 0 {
			hooksObj[at].value = append(v.([]any), group)
		} else {
			hooksObj = append(hooksObj, jsonMember{h.event, []any{group}})
		}
	}
	if hooksAt >= 0 {
		settings[hooksAt].value = hooksObj
	} else {
		settings = append(settings, jsonMember{"hooks", hooksObj})
	}
	compact, err := marshalNoEscape(settings)
	if err != nil {
		return nil, nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), missing, nil
}

func defaultClaudeSettings() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// $HOME keeps the entry valid in a settings file shared across machines
func hookCbusPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "cbus"
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, exe); err == nil && !strings.HasPrefix(rel, "..") && !strings.ContainsAny(rel, " \t") {
			return "$HOME/" + filepath.ToSlash(rel)
		}
	}
	if strings.ContainsAny(exe, " \t") {
		return `"` + exe + `"`
	}
	return exe
}

func writeSettingsAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func runInstallHooks(args []string) int {
	p, err := splitVerbArgs(args, map[string]bool{"--path": true}, map[string]bool{"--dry-run": true}, true)
	if err != nil {
		return die("%v\n%s", err, installHooksUsage)
	}
	if err := noExtra(p.pos, 0, installHooksUsage); err != nil {
		return die("%v", err)
	}
	path, ok := p.has("--path")
	if !ok || path == "" {
		if path, err = defaultClaudeSettings(); err != nil {
			return die("%v", err)
		}
	}
	// a profile's settings may be a link to a shared file; replacing the link would fork it
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	mode := os.FileMode(0o600)
	raw, err := os.ReadFile(target)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return die("%v", err)
	}
	if exists {
		if fi, err := os.Stat(target); err == nil {
			mode = fi.Mode().Perm()
		}
	}
	cbusPath := hookCbusPath()
	merged, added, err := mergeClaudeHooks(raw, cbusPath)
	if err != nil {
		return die("%s: %v; not changed", target, err)
	}
	if len(added) == 0 {
		fmt.Printf("%s: hooks already installed\n", target)
		return 0
	}
	for _, h := range added {
		fmt.Printf("add %s: %s %s\n", h.event, cbusPath, h.verb)
	}
	if p.flags["--dry-run"] {
		fmt.Printf("dry run: %s not changed\n", target)
		return 0
	}
	if exists {
		backup := fmt.Sprintf("%s.bak-%s", target, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, raw, mode); err != nil {
			return die("backup: %v; not changed", err)
		}
		fmt.Printf("backup: %s\n", backup)
	} else if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return die("%v", err)
	}
	if err := writeSettingsAtomic(target, merged, mode); err != nil {
		return die("write %s: %v", target, err)
	}
	fmt.Printf("updated %s; new sessions pick it up\n", target)
	return 0
}

// claudeHooksHint is one line for install-assets; a machine with no settings file says nothing.
func claudeHooksHint() string {
	path, err := defaultClaudeSettings()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	settings, err := parseSettings(raw)
	if err != nil {
		return ""
	}
	missing, err := missingClaudeHooks(settings, hookCbusPath())
	if err != nil || len(missing) == 0 {
		return ""
	}
	return "cbus: note: coordinator notices are off on this machine; run cbus install-hooks to add them"
}

// a near miss on an install verb should name the real verbs
func unknownVerbHint(verb string) string {
	if verb != "install" {
		return ""
	}
	return "did you mean: cbus install-hooks, install-opencode, install-mods, install-commands, install-roles, install-codex-skills, install-assets"
}
