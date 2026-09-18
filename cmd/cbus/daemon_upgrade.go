package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"claudebus/internal/client"
)

type daemonHealth struct {
	Running  bool   `json:"running"`
	PID      int    `json:"pid"`
	Start    string `json:"start"`
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
}

func readDaemonHealth(ctx context.Context) (daemonHealth, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var h daemonHealth
	err := client.DaemonCallContext(ctx, "GET", "/health", nil, &h)
	return h, err
}

func checkDaemonCompatibility(h daemonHealth) error {
	if !h.Running || h.PID <= 0 || h.Start == "" || h.Protocol != client.DaemonProtocolVersion || h.Version != version {
		return fmt.Errorf("running daemon is incompatible (version=%q protocol=%d; this binary=%q protocol=%d); run cbus daemon restart to load this binary; registrations and pending mail are retained", h.Version, h.Protocol, version, client.DaemonProtocolVersion)
	}
	return nil
}

func daemonAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

func restartDaemon() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return restartDaemonWith(ctx, readDaemonHealth, func(ctx context.Context, h daemonHealth) error {
		return client.DaemonCallContext(ctx, "POST", "/stop", map[string]any{"pid": h.PID, "start": h.Start}, nil)
	}, client.DaemonLockAvailable, ensureDaemon)
}

func restartDaemonWith(ctx context.Context, health func(context.Context) (daemonHealth, error), stop func(context.Context, daemonHealth) error, lockAvailable func() (bool, error), start func() error) error {
	original, err := health(ctx)
	if err != nil && !daemonAbsent(err) {
		if errors.Is(err, os.ErrPermission) {
			return daemonAccessError(err)
		}
		return err
	}
	if err == nil {
		if original.PID <= 0 || original.Start == "" || original.Protocol < 2 {
			return errors.New("this legacy daemon cannot fence a restart to one process; run cbus daemon stop explicitly, then cbus daemon start after it exits; registrations and pending mail are retained")
		}
		if err := stop(ctx, original); err != nil {
			return fmt.Errorf("stop observed daemon: %w", err)
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("daemon shutdown was not confirmed; no replacement started: %w", err)
		}
		current, err := health(ctx)
		if err == nil {
			if original.PID == 0 || current.PID != original.PID || current.Start != original.Start {
				return errors.New("daemon instance changed during restart; replacement was not stopped; inspect cbus daemon status")
			}
		} else if !daemonAbsent(err) {
			return fmt.Errorf("confirm daemon shutdown: %w", err)
		} else {
			available, err := lockAvailable()
			if err != nil {
				return fmt.Errorf("confirm daemon lock released: %w", err)
			}
			if available {
				return start()
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
}
