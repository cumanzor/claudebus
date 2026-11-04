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

const installHooksUsage = "usage: cbus install-hooks [--path FILE] [--dry-run] [--repair]"

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

// hookChange is an entry to add. dead lists entries for the verb whose binary cannot run.
type hookChange struct {
	claudeHook
	dead []jsonObject
}

func isCbusBinary(path string) bool {
	base := filepath.Base(filepath.FromSlash(strings.Trim(path, `"'`)))
	return base == "cbus" || base == "cbus.exe"
}

// a plain "<path> <verb>" entry, the only shape whose binary can be judged; quote is 0, ' or "
func splitHookCommand(cmd string) (path, verb string, quote byte, ok bool) {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) || strings.HasPrefix(cmd, `'`) {
		quote = cmd[0]
		end := strings.IndexByte(cmd[1:], quote)
		if end < 0 {
			return "", "", 0, false
		}
		path, verb = cmd[1:end+1], strings.TrimSpace(cmd[end+2:])
	} else {
		var found bool
		if path, verb, found = strings.Cut(cmd, " "); !found {
			return "", "", 0, false
		}
		verb = strings.TrimSpace(verb)
	}
	return path, verb, quote, path != "" && verb != "" && !strings.ContainsAny(verb, " \t;&|<>")
}

// hookBinaryDead follows what the shell would do with the path. A bare name resolves on PATH, so it is not judged.
func hookBinaryDead(path string, quote byte) bool {
	home, homeErr := os.UserHomeDir()
	switch {
	case strings.HasPrefix(path, "~/"):
		if quote != 0 {
			return true // a quoted tilde does not expand
		}
		if homeErr != nil {
			return false
		}
		path = filepath.Join(home, filepath.FromSlash(path[2:]))
	case strings.HasPrefix(path, "$HOME/") || strings.HasPrefix(path, "${HOME}/"):
		if quote == '\'' {
			return true // single quotes do not expand
		}
		if homeErr != nil {
			return false
		}
		if quote == 0 && strings.ContainsAny(home, " \t") {
			return true // the shell splits an unquoted $HOME at the space
		}
		path = filepath.Join(home, filepath.FromSlash(path[strings.IndexByte(path, '/')+1:]))
	}
	if !filepath.IsAbs(path) {
		return false
	}
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

// any path to this cbus counts, so a hand-written entry is not added twice; dead entries are returned separately
func findHookCommand(groups []any, verb, cbusPath string) (present bool, dead []jsonObject) {
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
			cmd := fmt.Sprint(c)
			if strings.Contains(cmd, cbusPath+" "+verb) {
				present = true
				continue
			}
			if path, v, quote, simple := splitHookCommand(cmd); simple && v == verb && isCbusBinary(path) {
				if hookBinaryDead(path, quote) {
					dead = append(dead, hook)
				} else {
					present = true
				}
				continue
			}
			fields := strings.Fields(cmd)
			for i := 1; i < len(fields); i++ {
				if fields[i] == verb && isCbusBinary(fields[i-1]) {
					present = true
				}
			}
		}
	}
	return present, dead
}

func hookCommandText(hook jsonObject) string {
	c, _ := hook.get("command")
	return fmt.Sprint(c)
}

// missingClaudeHooks lists verbs with no working entry, and verbs with a dead entry beside a working one.
func missingClaudeHooks(settings jsonObject, cbusPath string) ([]hookChange, error) {
	var hooksObj jsonObject
	if v, i := settings.get("hooks"); i >= 0 {
		o, ok := v.(jsonObject)
		if !ok {
			return nil, errors.New(`"hooks" is not an object`)
		}
		hooksObj = o
	}
	var missing []hookChange
	for _, h := range claudeHooks {
		var groups []any
		if v, i := hooksObj.get(h.event); i >= 0 {
			a, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf(`"hooks.%s" is not a list`, h.event)
			}
			groups = a
		}
		present, dead := findHookCommand(groups, h.verb, cbusPath)
		if !present || len(dead) > 0 {
			missing = append(missing, hookChange{claudeHook: h, dead: dead})
		}
	}
	return missing, nil
}

// mergeClaudeHooks adds what is missing. A dead entry is another machine's path as easily as
// a stale one on this machine, so it is only rewritten with repair; otherwise it is reported.
func mergeClaudeHooks(raw []byte, cbusPath string, repair bool) ([]byte, []hookChange, []string, error) {
	settings, err := parseSettings(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	changes, err := missingClaudeHooks(settings, cbusPath)
	if err != nil || len(changes) == 0 {
		return nil, nil, nil, err
	}
	var dead []string
	wrote := false
	hooksVal, hooksAt := settings.get("hooks")
	hooksObj, _ := hooksVal.(jsonObject)
	for _, h := range changes {
		for _, hook := range h.dead {
			dead = append(dead, h.event+": "+hookCommandText(hook))
			if repair {
				_, at := hook.get("command")
				hook[at].value = cbusPath + " " + h.verb
				wrote = true
			}
		}
		if len(h.dead) > 0 {
			continue
		}
		wrote = true
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
	if !wrote {
		return nil, changes, dead, nil
	}
	compact, err := marshalNoEscape(settings)
	if err != nil {
		return nil, nil, nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, nil, nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), changes, dead, nil
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

// hookCbusPath is how a settings entry should invoke this binary.
func hookCbusPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "cbus"
	}
	home, _ := os.UserHomeDir()
	return hookCommandPath(filepath.ToSlash(exe), filepath.ToSlash(home))
}

// $HOME keeps the entry valid in a settings file shared across machines, and the
// quotes keep it one word when the home directory has a space
func hookCommandPath(exe, home string) string {
	if home != "" && strings.HasPrefix(exe, home+"/") {
		if rel := strings.TrimPrefix(exe, home+"/"); shellSafe(rel) {
			return `"$HOME/` + rel + `"`
		}
	}
	if shellSafe(exe) {
		return exe
	}
	return "'" + strings.ReplaceAll(exe, "'", `'\''`) + "'"
}

func shellSafe(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+:@%,=", r)) {
			return false
		}
	}
	return s != ""
}

// resolveLinkTarget finds the file a settings path really opens. Text joins would misplace
// a relative link whose directory is itself reached through a link, so each directory is
// resolved first. A dangling link gives its target, which EvalSymlinks refuses to do.
func resolveLinkTarget(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	for i := 0; i < 40; i++ {
		dir := filepath.Dir(path)
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		path = filepath.Join(dir, filepath.Base(path))
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		link, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(link) {
			link = dir + string(filepath.Separator) + link
		}
		path = link
	}
	return "", fmt.Errorf("%s: too many levels of symlinks", path)
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
	p, err := splitVerbArgs(args, map[string]bool{"--path": true}, map[string]bool{"--dry-run": true, "--repair": true}, true)
	if err != nil {
		return die("%v\n%s", err, installHooksUsage)
	}
	if err := noExtra(p.pos, 0, installHooksUsage); err != nil {
		return die("%v", err)
	}
	path, ok := p.has("--path")
	if ok && path == "" {
		return die("--path: want a settings file")
	}
	if !ok {
		if path, err = defaultClaudeSettings(); err != nil {
			return die("%v", err)
		}
	}
	// a profile's settings may be a link to a shared file; replacing the link would fork it
	target, err := resolveLinkTarget(path)
	if err != nil {
		return die("%v", err)
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
	repair := p.flags["--repair"]
	merged, changes, dead, err := mergeClaudeHooks(raw, cbusPath, repair)
	if err != nil {
		return die("%s: %v; not changed", target, err)
	}
	if len(changes) == 0 {
		fmt.Printf("%s: hooks already installed\n", target)
		return 0
	}
	for _, h := range changes {
		if len(h.dead) == 0 {
			fmt.Printf("add %s: %s %s\n", h.event, cbusPath, h.verb)
		}
	}
	for _, d := range dead {
		if repair {
			fmt.Printf("repair %s -> %s\n", d, cbusPath)
		} else {
			fmt.Printf("dead on this machine: %s\n", d)
		}
	}
	rc := 0
	if len(dead) > 0 && !repair {
		fmt.Println("not rewritten, in case the file is shared with a machine where it works; --repair points them at this binary")
		rc = 1
	}
	if merged == nil {
		return rc
	}
	if p.flags["--dry-run"] {
		fmt.Printf("dry run: %s not changed\n", target)
		return rc
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
	return rc
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
