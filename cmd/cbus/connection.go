package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"claudebus/internal/client"
)

func runConnect(args []string) int {
	if runtime.GOOS == "windows" {
		return die("cbus connect is not available on windows in phase 1")
	}
	pos, asJSON, opts, err := connectArgs(args)
	if err != nil {
		return die("%v", err)
	}
	if len(pos) < 1 || len(pos) > 2 {
		return die("usage: cbus connect <channel> [alias] [--codex-sqlite-home ABS_PATH] [--json]")
	}
	cfg, thread, err := client.CodexConnectIdentityWithOptions(opts)
	if err != nil {
		return die("%v", err)
	}
	alias := ""
	if len(pos) == 2 {
		alias = pos[1]
	}
	channel := pos[0]
	var relay *client.RelayConfig
	if client.IsRemote(channel) {
		var host, embeddedAlias string
		channel, host, embeddedAlias, err = client.ParseRemote(channel)
		if err != nil {
			return die("%v", err)
		}
		if embeddedAlias != "" || alias == "" {
			return die("remote connect requires: cbus connect CHANNEL@HOST ALIAS")
		}
		relay, err = client.CodexRelayConfig(host)
		if err != nil {
			return die("%v", err)
		}
	}
	if err = ensureDaemon(); err != nil {
		return die("start cbus daemon: %v", err)
	}
	var state client.ConnectionState
	err = client.DaemonCall("POST", "/connect", client.ConnectRequest{Channel: channel, Alias: alias, ThreadID: thread, Config: cfg, Relay: relay}, &state)
	if err != nil {
		return die("%v", err)
	}
	if asJSON {
		return printConnectionJSON(state)
	}
	fmt.Printf("%s: native queue storage available for Codex thread %s\n", client.ConnectionTarget(&state), state.ThreadID)
	fmt.Println("Running consumer capability and receipt are unverified until recipient evidence. Supported CLIs consume without restart; native queue wake can take about 10 seconds. Busy sessions wait; interrupted sessions need user continuation.")
	return 0
}

func connectArgs(args []string) ([]string, bool, client.CodexConnectOptions, error) {
	var opts client.CodexConnectOptions
	var rest []string
	literal := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			literal = true
		}
		if !literal && arg == "--codex-sqlite-home" {
			if i+1 >= len(args) || args[i+1] == "" || opts.SQLiteHome != "" {
				return nil, false, opts, errors.New("--codex-sqlite-home requires one nonempty absolute path")
			}
			i++
			opts.SQLiteHome = args[i]
			continue
		}
		rest = append(rest, arg)
	}
	pos, asJSON, err := connectionArgs(rest)
	return pos, asJSON, opts, err
}

func runConnection(args []string) int {
	if len(args) > 0 && args[0] == "abandon" {
		return runConnectionAbandon(args[1:])
	}
	pos, asJSON, err := connectionArgs(args)
	if err != nil {
		return die("%v", err)
	}
	if len(pos) < 1 || len(pos) > 2 {
		return die("usage: cbus connection status [channel/alias] [--json] | reconcile|disconnect <channel/alias> | abandon <channel/alias> --pending <client-id> --reason <text>")
	}
	switch pos[0] {
	case "status":
		var states []client.ConnectionState
		if err = client.DaemonCall("GET", "/connections", nil, &states); err != nil {
			return die("daemon unavailable: %v (cbus daemon start)", err)
		}
		selected := make([]client.ConnectionState, 0, len(states))
		for _, s := range states {
			if len(pos) == 1 || client.ConnectionTarget(&s) == pos[1] {
				selected = append(selected, s)
			}
		}
		if len(pos) == 2 && len(selected) == 0 {
			return die("no managed connection for %s", pos[1])
		}
		if asJSON {
			return printConnectionJSON(selected)
		}
		for _, s := range selected {
			fmt.Print(connectionStatusText(s))
		}
		return 0
	case "reconcile":
		if len(pos) != 2 {
			return die("usage: cbus connection reconcile <channel/alias> [--json]")
		}
		var state client.ConnectionState
		if err := client.DaemonCall("POST", "/reconcile", map[string]string{"target": pos[1]}, &state); err != nil {
			return die("%v", err)
		}
		if asJSON {
			return printConnectionJSON(state)
		}
		fmt.Print(connectionStatusText(state))
		fmt.Println("Reconciliation checked existing evidence; no message was enqueued or retried.")
		return 0
	case "disconnect":
		if len(pos) != 2 {
			return die("usage: cbus connection disconnect <channel/alias>")
		}
		if err = client.DaemonCall("POST", "/disconnect", map[string]string{"target": pos[1]}, nil); err != nil {
			return die("%v", err)
		}
		if asJSON {
			return printConnectionJSON(map[string]any{"target": pos[1], "disconnected": true})
		}
		fmt.Printf("disconnected %s; inbox retained; already accepted Codex queue items are unchanged\n", pos[1])
		return 0
	default:
		return die("unknown connection command %q", pos[0])
	}
}

func connectionStatusText(s client.ConnectionState) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s: %s; accepted=%d; abandoned=%d\n", client.ConnectionTarget(&s), s.State, s.Accepted, s.Abandoned)
	if s.Consumer == nil {
		fmt.Fprintln(&out, "  CLI consumer: unknown (no runtime observation)")
	} else {
		fmt.Fprintf(&out, "  CLI consumer: %s; observed=%s", s.Consumer.State, s.Consumer.ObservedAt)
		if s.Consumer.PID > 0 {
			fmt.Fprintf(&out, "; pid=%d", s.Consumer.PID)
		}
		fmt.Fprintln(&out)
		if s.Consumer.Detail != "" {
			fmt.Fprintf(&out, "  %s\n", s.Consumer.Detail)
		}
	}
	if s.LastAccepted == nil {
		fmt.Fprintln(&out, "  latest receipt: unverified (no tracked message observation)")
	} else {
		d := s.LastAccepted
		fmt.Fprintf(&out, "  latest accepted message: %s; observed=%s; client=%s\n", d.State, d.ObservedAt, d.Attempt.ClientID)
		if d.State == "received" {
			fmt.Fprintln(&out, "  Received means present in this thread's history; completion, reply and current process liveness are unverified.")
		} else {
			fmt.Fprintln(&out, "  Recipient receipt is unverified. Run connection reconcile to refresh this observation.")
		}
	}
	if s.Pending != nil {
		outcome := "unknown; automatic replay blocked"
		if s.Pending.QueueID != "" || s.Pending.Evidence != nil {
			outcome = "acceptance observed; durable cursor update pending"
		}
		fmt.Fprintf(&out, "  pending=%s; %s\n", s.Pending.ClientID, outcome)
	}
	if s.Error != "" {
		fmt.Fprintf(&out, "  %s\n", s.Error)
	}
	return out.String()
}

func abandonArgs(args []string) (client.AbandonRequest, bool, error) {
	var req client.AbandonRequest
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--json":
			asJSON = true
		case "--pending", "--reason":
			if i+1 == len(args) {
				return req, false, fmt.Errorf("missing value for %s", arg)
			}
			i++
			field := &req.ClientID
			if arg == "--reason" {
				field = &req.Reason
			}
			if *field != "" {
				return req, false, fmt.Errorf("duplicate flag %s", arg)
			}
			*field = args[i]
		default:
			if strings.HasPrefix(arg, "-") || req.Target != "" {
				return req, false, fmt.Errorf("unexpected argument %s", arg)
			}
			req.Target = arg
		}
	}
	if req.Target == "" || req.ClientID == "" || strings.TrimSpace(req.Reason) == "" {
		return req, false, errors.New("usage: cbus connection abandon <channel/alias> --pending <client-id> --reason <text> [--json]")
	}
	return req, asJSON, nil
}

func runConnectionAbandon(args []string) int {
	req, asJSON, err := abandonArgs(args)
	if err != nil {
		return die("%v", err)
	}
	var state client.ConnectionState
	if err := client.DaemonCall("POST", "/abandon", req, &state); err != nil {
		return die("%v", err)
	}
	if asJSON {
		return printConnectionJSON(state)
	}
	fmt.Printf("Abandoned local attempt %s; later mail may proceed. The original may already have arrived or may still arrive; no native queue item was canceled and no retry was sent.\n", req.ClientID)
	fmt.Print(connectionStatusText(state))
	return 0
}

func runDaemon(args []string) int {
	if runtime.GOOS == "windows" {
		return die("cbus daemon is not available on windows in phase 1")
	}
	pos, asJSON, err := connectionArgs(args)
	if err != nil {
		return die("%v", err)
	}
	if len(pos) != 1 {
		return die("usage: cbus daemon start|restart|status|stop|serve [--json]")
	}
	switch pos[0] {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err = client.RunDaemon(ctx, version); err != nil {
			return die("%v", err)
		}
		return 0
	case "start", "restart":
		if pos[0] == "restart" {
			err = restartDaemon()
		} else {
			err = ensureDaemon()
		}
		if err != nil {
			return die("%v", err)
		}
		fallthrough
	case "status":
		state, err := readDaemonHealth(context.Background())
		if err != nil {
			return die("daemon unavailable: %v", err)
		}
		if asJSON {
			return printConnectionJSON(state)
		}
		fmt.Printf("cbus daemon running (pid %d; version %s; protocol %d)\n", state.PID, state.Version, state.Protocol)
		return 0
	case "stop":
		if err = client.DaemonCall("POST", "/stop", nil, nil); err != nil {
			return die("%v", err)
		}
		if asJSON {
			return printConnectionJSON(map[string]bool{"stopping": true})
		}
		fmt.Println("daemon stopping; registrations and pending messages retained")
		return 0
	default:
		return die("unknown daemon command %q", pos[0])
	}
}

func ensureDaemon() error {
	return ensureDaemonWith(func() error {
		h, err := readDaemonHealth(context.Background())
		if err != nil {
			return err
		}
		return checkDaemonCompatibility(h)
	}, startDaemon)
}

// Keep the health probe and process launch injectable so denied access can be
// tested without starting a real daemon or changing the caller's permissions.
func ensureDaemonWith(health, start func() error) error {
	if err := health(); err == nil {
		return nil
	} else if errors.Is(err, os.ErrPermission) {
		return daemonAccessError(err)
	} else if !daemonAbsent(err) {
		return err
	}
	if err := start(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := health(); err == nil {
			return nil
		} else if errors.Is(err, os.ErrPermission) {
			return daemonAccessError(err)
		} else if !daemonAbsent(err) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become ready; see %s", filepath.Join(client.DaemonDir(), "daemon.log"))
}

func daemonAccessError(err error) error {
	return fmt.Errorf("cannot access cbus daemon socket: %w; if sandboxed, request approval to rerun this exact command with access to the local socket", err)
}

func startDaemon() error {
	if err := os.MkdirAll(client.DaemonDir(), 0700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(client.DaemonDir(), "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon", "serve")
	detachProcess(cmd)
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func connectionArgs(args []string) ([]string, bool, error) {
	var pos []string
	asJSON := false
	literal := false
	for _, arg := range args {
		if !literal && arg == "--" {
			literal = true
			continue
		}
		if !literal && arg == "--json" {
			asJSON = true
			continue
		}
		if !literal && strings.HasPrefix(arg, "-") {
			return nil, false, fmt.Errorf("unknown flag %s", arg)
		}
		pos = append(pos, arg)
	}
	return pos, asJSON, nil
}
func printConnectionJSON(v any) int {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		return die("%v", err)
	}
	return 0
}
