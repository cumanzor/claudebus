package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestSessionlessWarningIdentityContext(t *testing.T) {
	for _, test := range []struct {
		name, session, channel, alias, want string
	}{
		{name: "native Codex", session: "thread-id"},
		{name: "no identity", want: "replies to it may be unroutable"},
		{name: "wrapper sender", channel: "dev", alias: "codex", want: "send uses CBUS_CHANNEL/CBUS_ALIAS for replies"},
		{name: "invalid context", channel: "../bad", alias: "codex", want: "replies to it may be unroutable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"CLAUDE_CODE_SESSION_ID", "CBUS_SESSION_ID", "GROK_SESSION_ID", "CODEX_THREAD_ID"} {
				t.Setenv(key, "")
			}
			t.Setenv("CODEX_THREAD_ID", test.session)
			t.Setenv("CBUS_CHANNEL", test.channel)
			t.Setenv("CBUS_ALIAS", test.alias)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			original := os.Stderr
			os.Stderr = writer
			warnIfSessionless()
			os.Stderr = original
			_ = writer.Close()
			got, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected warning: %s", got)
				}
			} else if !strings.Contains(string(got), test.want) || !strings.Contains(string(got), "no harness session ID") {
				t.Fatalf("warning = %q; want %q", got, test.want)
			}
		})
	}
}
