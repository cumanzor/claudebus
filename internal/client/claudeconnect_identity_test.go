//go:build darwin || linux

package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func claudeCallerFixture(t *testing.T) (string, func() (int, string, error)) {
	t.Helper()
	clearSessionEnv(t)
	endpoint, _, _ := testClaudeEndpoint(t)
	home := t.TempDir()
	config := filepath.Join(home, "profile[one]")
	project := filepath.Join(config, "projects", "original-workspace")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(project, daemonTestThread+".jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"mode","sessionId":"`+daemonTestThread+`"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("CLAUDE_CODE_SESSION_ID", daemonTestThread)
	t.Setenv("CLAUDE_PID", strconv.Itoa(endpoint.PID))
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", endpoint.Socket)
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "test-token-must-never-be-captured")
	return transcript, func() (int, string, error) { return endpoint.PID, endpoint.StartToken, nil }
}

func TestClaudeCallerCapturesExactSessionAcrossCwdAndConfigAlias(t *testing.T) {
	transcript, runtime := claudeCallerFixture(t)
	data, _ := os.ReadFile(transcript)
	// A partial record may span blocks; its eventual receipt must remain visible.
	fullData := append(append([]byte(nil), data...), []byte(strings.Repeat(" ", 5000)+`{"type":`)...)
	if err := os.WriteFile(transcript, fullData, 0600); err != nil {
		t.Fatal(err)
	}
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	alias := filepath.Join(os.Getenv("HOME"), "ccs-profile")
	if err := os.Symlink(config, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", alias)
	t.Chdir(t.TempDir()) // Resuming elsewhere must not select a cwd-derived file.
	binding, err := claudeConnectIdentity(runtime)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(transcript)
	dev, ino, _, _ := fileIdentity(transcript)
	if binding.SessionID != daemonTestThread || binding.TranscriptPath != canonical || binding.TranscriptDev != dev || binding.TranscriptIno != ino || binding.Endpoint.PID != os.Getpid() {
		t.Fatalf("incorrect exact binding: %+v", binding)
	}
	wantConfig, _ := filepath.EvalSymlinks(config)
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if binding.ConfigHome != wantConfig || binding.Cwd != cwd || binding.Cwd == filepath.Dir(transcript) {
		t.Fatal("capture borrowed the transcript's directory or noncanonical config alias")
	}
	encoded, _ := json.Marshal(binding)
	if strings.Contains(string(encoded), "test-token-must-never-be-captured") || strings.Contains(string(encoded), "RuntimeVersion") {
		t.Fatal("binding contains credentials or an unverified version")
	}
	f, err := openBoundClaudeTranscript(binding)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := f.Stat()
	f.Close()
	if binding.TranscriptSize != info.Size() || binding.TranscriptOffset != int64(len(data)) {
		t.Fatal("initial extent and complete-line cursor were not captured")
	}
	if err := os.WriteFile(transcript, data, 0600); err != nil {
		t.Fatal(err)
	}
	if truncated, err := openBoundClaudeTranscript(binding); err == nil {
		truncated.Close()
		t.Fatal("same inode truncated below its captured extent was accepted")
	}
	if err := os.Rename(transcript, transcript+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, fullData, 0600); err != nil {
		t.Fatal(err)
	}
	if replacement, err := openBoundClaudeTranscript(binding); err == nil {
		replacement.Close()
		t.Fatal("same UUID on a replaced inode was accepted")
	}
}

func TestClaudeCallerDefaultConfigHome(t *testing.T) {
	_, runtime := claudeCallerFixture(t)
	defaultHome := filepath.Join(os.Getenv("HOME"), ".claude")
	if err := os.Rename(os.Getenv("CLAUDE_CONFIG_DIR"), defaultHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	binding, err := claudeConnectIdentity(runtime)
	want, _ := filepath.EvalSymlinks(defaultHome)
	if err != nil || binding.ConfigHome != want {
		t.Fatalf("default home not captured: %+v, %v", binding, err)
	}
}

func TestClaudeCallerRejectsMissingOrConflictingEnvironment(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"CLAUDE_CODE_SESSION_ID", ""}, {"CLAUDE_CODE_SESSION_ID", "latest"},
		{"CBUS_SESSION_ID", "22222222-2222-4222-8222-222222222222"},
		{"CLAUDE_PID", ""}, {"CLAUDE_PID", "1"}, {"CLAUDE_PID", "999999"},
		{"CLAUDE_CONFIG_DIR", "relative-profile"}, {"CLAUDE_CODE_MESSAGING_SOCKET", ""}, {"CLAUDE_CONFIG_DIR", "/missing-cbus-test-config"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			_, runtime := claudeCallerFixture(t)
			t.Setenv(tc.key, tc.value)
			if _, err := claudeConnectIdentity(runtime); err == nil {
				t.Fatal("invalid caller environment accepted")
			}
		})
	}
}

func TestClaudeCallerRejectsChangedAncestor(t *testing.T) {
	_, runtime := claudeCallerFixture(t)
	calls := 0
	if _, err := claudeConnectIdentity(func() (int, string, error) {
		pid, start, err := runtime()
		calls++
		if calls > 1 {
			start += "-reused"
		}
		return pid, start, err
	}); err == nil {
		t.Fatal("caller incarnation changed during capture")
	}
}

func TestClaudeCallerTranscriptRequiresUniqueCompleteExactIdentity(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "different session", "sidechain", "unidentified", "malformed", "unfinished", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path, runtime := claudeCallerFixture(t)
			text := `{"sessionId":"` + daemonTestThread + `"}` + "\n"
			switch kind {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				other := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "other-workspace")
				if err := os.MkdirAll(other, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(other, filepath.Base(path)), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".old", path); err != nil {
					t.Fatal(err)
				}
			default:
				text = map[string]string{
					"different session": "{\"sessionId\":\"wrong\"}\n",
					"sidechain":         `{"sessionId":"` + daemonTestThread + `","isSidechain":true}` + "\n",
					"unidentified":      "{\"type\":\"summary\"}\n", "malformed": "{\n",
					"unfinished": strings.TrimSuffix(text, "\n"),
				}[kind]
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := claudeConnectIdentity(runtime); err == nil {
				t.Fatal("invalid transcript accepted")
			}
		})
	}
}

func TestClaudeCallerUsesNearestHarnessWithNativeVersionPath(t *testing.T) {
	for _, name := range []string{"claude", "/usr/local/bin/claude-canary", "/home/test/.local/share/claude/versions/2.1.277"} {
		rows := map[int]procRecord{30: {PPid: 20, Comm: "sh"}, 20: {PPid: 10, Comm: name}, 10: {PPid: 1, Comm: "codex"}}
		lookup := func(pid int) (procRecord, bool) { row, ok := rows[pid]; return row, ok }
		if pid, err := claudeCallerPID(30, lookup); err != nil || pid != 20 {
			t.Fatalf("native ancestor missed: %d, %v", pid, err)
		}
		rows[30] = procRecord{PPid: 20, Comm: "codex"}
		if _, err := claudeCallerPID(30, lookup); err == nil {
			t.Fatal("walk crossed another harness")
		}
	}
	for _, name := range []string{"2.1.277", "/tmp/versions/2.1.277", "sh"} {
		if _, err := claudeCallerPID(30, func(int) (procRecord, bool) { return procRecord{PPid: 1, Comm: name, Argv: "sh claude"}, true }); err == nil {
			t.Fatal("guessed a Claude ancestor")
		}
	}
	if _, err := claudeCallerPID(30, func(int) (procRecord, bool) { return procRecord{}, false }); err == nil {
		t.Fatal("inspection failure accepted")
	}
}
