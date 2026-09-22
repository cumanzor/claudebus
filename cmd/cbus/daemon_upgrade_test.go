package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"claudebus/internal/client"
)

func TestEnsureDaemonIncompatibleOrUnknownNeverStarts(t *testing.T) {
	good := daemonHealth{Running: true, PID: 42, Start: "process-token", Version: version, Protocol: client.DaemonProtocolVersion}
	if err := checkDaemonCompatibility(good); err != nil {
		t.Fatal(err)
	}
	oldVersion, oldProtocol, oldHealth := good, good, good
	oldVersion.Version = "old-build"
	oldProtocol.Protocol--
	oldHealth.Start = ""
	for _, h := range []daemonHealth{oldVersion, oldProtocol, oldHealth} {
		probeErr := checkDaemonCompatibility(h)
		if probeErr == nil {
			t.Fatalf("accepted incompatible health: %+v", h)
		}
		launches := 0
		err := ensureDaemonWith(func() error { return probeErr }, func() error { launches++; return nil })
		if err == nil || launches != 0 {
			t.Fatalf("launched through incompatible daemon: %v, %d", err, launches)
		}
	}
	launches := 0
	unknown := errors.New("health returned malformed JSON")
	if err := ensureDaemonWith(func() error { return unknown }, func() error { launches++; return nil }); !errors.Is(err, unknown) || launches != 0 {
		t.Fatalf("unknown health triggered startup: %v %d", err, launches)
	}
}

func TestDaemonRestartWaitsForSocketAndLock(t *testing.T) {
	h := daemonHealth{Running: true, PID: 42, Start: "old", Protocol: 2}
	probes, stops, locks, starts := 0, 0, 0, 0
	err := restartDaemonWith(context.Background(), func(context.Context) (daemonHealth, error) {
		probes++
		if probes <= 2 {
			return h, nil
		}
		return daemonHealth{}, daemonHealthFailure(syscall.ENOENT)
	}, func(_ context.Context, expected daemonHealth) error {
		stops++
		if expected != h {
			t.Fatal("wrong stop fence")
		}
		return nil
	}, func() (bool, error) { locks++; return locks > 1, nil }, func() error { starts++; return nil })
	if err != nil || stops != 1 || starts != 1 || probes != 4 || locks != 2 {
		t.Fatalf("restart sequencing failed: %v probes=%d stops=%d locks=%d starts=%d", err, probes, stops, locks, starts)
	}
}

func TestDaemonRestartNeverKillsReplacement(t *testing.T) {
	probes, stops, starts := 0, 0, 0
	err := restartDaemonWith(context.Background(), func(context.Context) (daemonHealth, error) {
		probes++
		return daemonHealth{Running: true, PID: 42, Start: []string{"old", "new"}[probes-1], Protocol: 2}, nil
	}, func(context.Context, daemonHealth) error { stops++; return nil }, func() (bool, error) { t.Fatal("replacement lock must not be inspected"); return false, nil }, func() error { starts++; return nil })
	if err == nil || !strings.Contains(err.Error(), "instance changed") || stops != 1 || starts != 0 {
		t.Fatalf("replacement was touched: %v stops=%d starts=%d", err, stops, starts)
	}
}

func TestDaemonRestartTimeoutDoesNotStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	probes, starts := 0, 0
	err := restartDaemonWith(ctx, func(context.Context) (daemonHealth, error) {
		probes++
		if probes == 1 {
			return daemonHealth{Running: true, PID: 42, Start: "old", Protocol: 2}, nil
		}
		return daemonHealth{}, daemonHealthFailure(syscall.ECONNREFUSED)
	}, func(context.Context, daemonHealth) error { return nil }, func() (bool, error) { return false, nil }, func() error { starts++; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || starts != 0 {
		t.Fatalf("unconfirmed stop started daemon: %v %d", err, starts)
	}
}

func TestDaemonRestartLegacyRequiresExplicitStop(t *testing.T) {
	err := restartDaemonWith(context.Background(), func(context.Context) (daemonHealth, error) { return daemonHealth{Running: true, PID: 42}, nil }, func(context.Context, daemonHealth) error { t.Fatal("legacy daemon cannot fence stop"); return nil }, func() (bool, error) { t.Fatal("legacy lock must not be used"); return false, nil }, func() error { t.Fatal("legacy daemon still running"); return nil })
	if err == nil || !strings.Contains(err.Error(), "cbus daemon stop") {
		t.Fatalf("unsafe legacy restart accepted: %v", err)
	}
}

// exitingProbe is what a health probe sees when the daemon closes a connection it
// accepted just before its listener went away: `Get "http://cbus/health": EOF`.
func exitingProbe(err error) error {
	return &url.Error{Op: "Get", URL: "http://cbus/health", Err: err}
}

func TestDaemonRestartStartsAfterExitingProbe(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "unix", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}
	for _, cut := range []error{io.EOF, io.ErrUnexpectedEOF, reset, context.DeadlineExceeded} {
		probes, locks, starts := 0, 0, 0
		err := restartDaemonWith(context.Background(), func(context.Context) (daemonHealth, error) {
			probes++
			if probes == 1 {
				return daemonHealth{Running: true, PID: 42, Start: "old", Protocol: 2}, nil
			}
			return daemonHealth{}, exitingProbe(cut)
		}, func(context.Context, daemonHealth) error { return nil }, func() (bool, error) { locks++; return locks > 1, nil }, func() error { starts++; return nil })
		if err != nil || starts != 1 || locks != 2 {
			t.Fatalf("%v: a probe cut off by the exiting daemon must not abandon the restart: %v locks=%d starts=%d", cut, err, locks, starts)
		}
	}
}

func TestDaemonRestartExitingProbeWaitsForLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	probes, starts := 0, 0
	err := restartDaemonWith(ctx, func(context.Context) (daemonHealth, error) {
		probes++
		if probes == 1 {
			return daemonHealth{Running: true, PID: 42, Start: "old", Protocol: 2}, nil
		}
		return daemonHealth{}, exitingProbe(io.EOF)
	}, func(context.Context, daemonHealth) error { return nil }, func() (bool, error) { return false, nil }, func() error { starts++; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || starts != 0 {
		t.Fatalf("EOF is not proof of exit; a held lock must block the start: %v %d", err, starts)
	}
}

func TestDaemonRestartUnknownProbeErrorDoesNotStart(t *testing.T) {
	probes, starts := 0, 0
	odd := errors.New("health returned malformed JSON")
	err := restartDaemonWith(context.Background(), func(context.Context) (daemonHealth, error) {
		probes++
		if probes == 1 {
			return daemonHealth{Running: true, PID: 42, Start: "old", Protocol: 2}, nil
		}
		return daemonHealth{}, odd
	}, func(context.Context, daemonHealth) error { return nil }, func() (bool, error) { t.Fatal("lock must not decide an unknown failure"); return false, nil }, func() error { starts++; return nil })
	if !errors.Is(err, odd) || starts != 0 {
		t.Fatalf("unknown probe failure started a daemon: %v %d", err, starts)
	}
}
