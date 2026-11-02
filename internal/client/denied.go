package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"claudebus/internal/core"
)

const (
	deniedKind       = "denied"
	deniedWindow     = 10 * time.Minute
	deniedStateTTL   = 24 * time.Hour
	deniedFieldMax   = 200
	deniedWithheld   = "[withheld]"
	deniedMinToken   = 8
	deniedPayloadMax = 16 << 20
)

var deniedNow = time.Now

// deniedPayload is all the hook reads. tool_input stays raw: it only ever feeds the
// withhold list, because a credential denial's command can hold the secret.
type deniedPayload struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	Reason    string          `json:"reason"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// HookDenied runs the PermissionDenied hook: it tells the coordinator of every local
// registration of this session that the classifier refused a tool call. The coordinator
// cannot override it, only reroute the work or ask the operator. Best-effort, and it
// writes nothing to stdout, which Claude Code parses for a retry decision.
func HookDenied(stdin io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(stdin, deniedPayloadMax))
	if err != nil {
		return err
	}
	var in deniedPayload
	if err := json.Unmarshal(b, &in); err != nil {
		return fmt.Errorf("unreadable payload: %w", err)
	}
	sid := in.SessionID
	if sid == "" {
		sid = SessionID()
	}
	if sid == "" {
		return nil
	}
	restore := OverrideSessionID(sid)
	regs := ResolveSelf()
	restore()
	tool, reason, desc := deniedFields(in)
	var errs []string
	for _, reg := range regs {
		coord, ok := CoordinatorOf(reg.Channel, reg.Alias)
		if !ok {
			continue
		}
		if err := notifyDenied(reg, coord, sid, tool, reason, desc); err != nil {
			errs = append(errs, fmt.Sprintf("%s/%s: %v", reg.Channel, reg.Alias, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func notifyDenied(reg LocalReg, coord, sid, tool, reason, desc string) error {
	key := deniedKey(tool, reason)
	send, more, since, err := claimDenied(reg.Channel, reg.Alias, sid, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cbus: hook-denied: collapse state: %v\n", err)
		send, more = true, 0 // a duplicate beats a lost denial
	}
	if !send {
		return nil
	}
	ch, alias, ok := strings.Cut(coord, "/")
	if !ok {
		return fmt.Errorf("bad coordinator %q", coord)
	}
	text := deniedText(reg.Alias, tool, reason, desc, more, since)
	err = deliverNotice(ch, alias, core.Message{From: reg.Channel + "/" + reg.Alias, To: coord, TS: Now(), Kind: deniedKind, Text: text})
	if err != nil {
		releaseDenied(reg.Channel, reg.Alias, key) // not told, so the next one is still a first
	}
	return err
}

func deniedText(alias, tool, reason, desc string, more int, since time.Time) string {
	parts := []string{tool}
	if reason != "" {
		parts = append(parts, "["+strings.Trim(reason, "[] ")+"]")
	}
	if desc != "" {
		parts = append(parts, fmt.Sprintf("%q", desc))
	}
	text := alias + " denied: " + strings.Join(parts, ", ")
	if more > 0 {
		text += fmt.Sprintf(" (%d more since %s)", more, since.UTC().Format("15:04Z"))
	}
	return text
}

func deniedFields(in deniedPayload) (tool, reason, desc string) {
	tool = strings.Map(func(r rune) rune {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.:-", r)) {
			return r
		}
		return -1
	}, in.ToolName)
	if tool == "" {
		tool = "unknown"
	}
	var input any
	_ = json.Unmarshal(in.ToolInput, &input)
	fields, _ := input.(map[string]any)
	if tool == "Bash" && fields != nil {
		desc, _ = fields["description"].(string)
		delete(fields, "description") // the one input field this notice is allowed to carry
	}
	tokens := deniedTokens(input)
	return tool, deniedClean(in.Reason, tokens), deniedClean(desc, tokens)
}

// deniedTokens is every long-enough piece of the tool input, longest first, so a whole
// value is withheld before a piece of it.
func deniedTokens(v any) []string {
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			for _, t := range strings.Fields(x) {
				seen[t] = true
			}
			for _, t := range strings.FieldsFunc(x, func(r rune) bool {
				return unicode.IsSpace(r) || strings.ContainsRune("=:'\"`,;|&<>()[]{}/\\$", r)
			}) {
				seen[t] = true
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	out := make([]string, 0, len(seen))
	for t := range seen {
		if len(t) >= deniedMinToken {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// deniedClean withholds echoed input. Two or more echoes read as a quoted command, whose
// short words could be the secret, so the whole field goes.
func deniedClean(s string, tokens []string) string {
	echoes := 0
	for _, t := range tokens {
		if strings.Contains(s, t) {
			echoes++
			s = strings.ReplaceAll(s, t, deniedWithheld)
		}
	}
	if echoes > 1 {
		return deniedWithheld
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > deniedFieldMax {
		s = string(r[:deniedFieldMax]) + "..."
	}
	return s
}

func deniedKey(tool, reason string) string {
	sum := sha256.Sum256([]byte(tool + "\x00" + reason))
	return hex.EncodeToString(sum[:8])
}

type deniedEntry struct {
	Sent       string `json:"sent"`
	Suppressed int    `json:"suppressed,omitempty"`
}

type deniedState struct {
	SessionID string                 `json:"sessionId"`
	Entries   map[string]deniedEntry `json:"entries"`
}

func deniedStatePath(ch, alias string) string {
	return filepath.Join(CBUSDir(), ".denials", ch, alias+".json")
}

func readDeniedState(path, sid string) deniedState {
	st := deniedState{SessionID: sid, Entries: map[string]deniedEntry{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var got deniedState
	if json.Unmarshal(b, &got) != nil || got.SessionID != sid || got.Entries == nil {
		return st
	}
	return got
}

// claimDenied decides whether this denial is a first or a repeat inside the window.
// more and since describe what was held back since the last notice.
func claimDenied(ch, alias, sid, key string) (send bool, more int, since time.Time, err error) {
	unlock, err := lockPeer(ch, alias)
	if err != nil {
		return false, 0, time.Time{}, err
	}
	defer unlock()
	path := deniedStatePath(ch, alias)
	st := readDeniedState(path, sid)
	now := deniedNow().UTC()
	for k, e := range st.Entries {
		if at, perr := time.Parse(time.RFC3339, e.Sent); perr == nil && now.Sub(at) > deniedStateTTL {
			delete(st.Entries, k)
		}
	}
	e := st.Entries[key]
	at, perr := time.Parse(time.RFC3339, e.Sent)
	if perr == nil && now.Sub(at) < deniedWindow {
		e.Suppressed++
		st.Entries[key] = e
		return false, 0, time.Time{}, writeDeniedState(path, st)
	}
	more, since = e.Suppressed, at
	st.Entries[key] = deniedEntry{Sent: now.Format(time.RFC3339)}
	return true, more, since, writeDeniedState(path, st)
}

func releaseDenied(ch, alias, key string) {
	unlock, err := lockPeer(ch, alias)
	if err != nil {
		return
	}
	defer unlock()
	path := deniedStatePath(ch, alias)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var st deniedState
	if json.Unmarshal(b, &st) != nil || st.Entries == nil {
		return
	}
	delete(st.Entries, key)
	_ = writeDeniedState(path, st)
}

func writeDeniedState(path string, st deniedState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeCoordinatorBytes(path, b)
}
