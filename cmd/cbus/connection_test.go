package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"claudebus/internal/client"
)

// HTTP over a Unix socket wraps the kernel failure through all three layers.
// Permission classification must inspect that chain, not just the top-level text.
func daemonHealthFailure(errno syscall.Errno) error {
	return &url.Error{Op: "Get", URL: "http://cbus/health", Err: &net.OpError{
		Op: "dial", Net: "unix", Addr: &net.UnixAddr{Name: "/test/control.sock", Net: "unix"},
		Err: &os.SyscallError{Syscall: "connect", Err: errno},
	}}
}

func TestConnectionStatusDistinguishesHistoricalReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, extra, want string
	}{
		{"old journal", "", "latest receipt: unverified"},
		{"queue accepted", `,"lastAccepted":{"attempt":{"clientId":"c1"},"state":"accepted","observedAt":"then"}`, "Recipient receipt is unverified"},
		{"queue present", `,"lastAccepted":{"attempt":{"clientId":"c1"},"state":"queued","observedAt":"then"}`, "latest accepted message: queued; observed=then"},
		{"history", `,"lastAccepted":{"attempt":{"clientId":"c1"},"state":"received","observedAt":"then"}`, "completion, reply and current process liveness are unverified"},
		{"unknown", `,"pending":{"clientId":"blocked"}`, "pending=blocked; unknown; automatic replay blocked"},
		{"acknowledged unsaved", `,"pending":{"clientId":"blocked","queueId":"q1"}`, "acceptance observed; durable cursor update pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state client.ConnectionState
			if err := json.Unmarshal([]byte(`{"channel":"dev","alias":"worker","state":"queue-ready","accepted":3`+tc.extra+`}`), &state); err != nil {
				t.Fatal(err)
			}
			got := connectionStatusText(state)
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "accepted=3") {
				t.Fatalf("misleading status: %s", got)
			}
		})
	}
}

func TestAbandonRequiresExplicitPendingIDAndReason(t *testing.T) {
	for _, args := range [][]string{
		{}, {"dev/worker"}, {"dev/worker", "--pending", "c1"},
		{"dev/worker", "--reason", "unblock"},
		{"dev/worker", "--pending", "c1", "--reason", "  "},
		{"dev/worker", "--pending", "c1", "--reason"},
		{"dev/worker", "--pending", "c1", "--pending", "c2", "--reason", "unblock"},
		{"dev/worker", "--pending", "c1", "--reason", "unblock", "--force"},
	} {
		if _, _, err := abandonArgs(args); err == nil {
			t.Fatalf("incomplete/ambiguous abandon command accepted: %q", args)
		}
	}
	req, asJSON, err := abandonArgs([]string{"dev/worker", "--pending", "c1", "--reason", "operator accepts unknown delivery", "--json"})
	if err != nil || !asJSON || req.Target != "dev/worker" || req.ClientID != "c1" || req.Reason != "operator accepts unknown delivery" {
		t.Fatalf("explicit recovery command not preserved: %+v, %v", req, err)
	}
}

func TestEnsureDaemonPermissionDeniedDoesNotLaunch(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EPERM, syscall.EACCES} {
		t.Run(errno.Error(), func(t *testing.T) {
			probeErr := daemonHealthFailure(errno)
			probes, launches := 0, 0
			err := ensureDaemonWith(func() error {
				probes++
				return probeErr
			}, func() error {
				launches++
				return errors.New("duplicate daemon startup attempted")
			})
			if probes != 1 || launches != 0 {
				t.Fatalf("denied access triggered retry/startup: probes=%d launches=%d", probes, launches)
			}
			if !errors.Is(err, errno) || !errors.Is(err, os.ErrPermission) {
				t.Fatalf("permission cause was lost: %v", err)
			}
			var urlErr *url.Error
			if !errors.As(err, &urlErr) || urlErr != probeErr {
				t.Fatalf("original socket error chain was lost: %v", err)
			}
			if !strings.Contains(err.Error(), "request approval") || !strings.Contains(err.Error(), "this exact command") {
				t.Errorf("missing useful permission diagnostic: %v", err)
			}
		})
	}
}

func TestEnsureDaemonAbsentStillStarts(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOENT, syscall.ECONNREFUSED} {
		t.Run(errno.Error(), func(t *testing.T) {
			probes, launches := 0, 0
			err := ensureDaemonWith(func() error {
				probes++
				if probes == 1 {
					return daemonHealthFailure(errno)
				}
				return nil
			}, func() error { launches++; return nil })
			if err != nil || probes != 2 || launches != 1 {
				t.Fatalf("absent-daemon bootstrap changed: probes=%d launches=%d err=%v", probes, launches, err)
			}
		})
	}
}

func TestEnsureDaemonRunningDoesNotLaunch(t *testing.T) {
	launches := 0
	if err := ensureDaemonWith(func() error { return nil }, func() error { launches++; return nil }); err != nil || launches != 0 {
		t.Fatalf("healthy daemon caused startup: launches=%d err=%v", launches, err)
	}
}

func TestEnsureDaemonReadinessPreservesPermissionError(t *testing.T) {
	probes, launches := 0, 0
	err := ensureDaemonWith(func() error {
		probes++
		if probes == 1 {
			return daemonHealthFailure(syscall.ENOENT)
		}
		return daemonHealthFailure(syscall.EPERM)
	}, func() error { launches++; return nil })
	if !errors.Is(err, syscall.EPERM) || probes != 2 || launches != 1 {
		t.Fatalf("readiness denial became a generic timeout: probes=%d launches=%d err=%v", probes, launches, err)
	}
}
