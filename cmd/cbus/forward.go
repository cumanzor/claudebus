package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"claudebus/internal/client"
)

const forwardUsage = "usage: cbus forward <channel>@<host>/<alias> --webhook-config FILE [--dead-letter FILE] [--per-min N] [--per-hour N] [--max-attempts N] [--timeout D] [--once] [--dry-run] [--print-service]"

// runForward delivers a relay mailbox to an outbound webhook; it is a durable
// relay consumer, so it needs no inbound port and no local cursor.
func runForward(args []string) int {
	if len(args) == 0 || !client.IsRemote(args[0]) {
		return die(forwardUsage)
	}
	ch, host, al, err := client.ParseRemote(args[0])
	if err != nil {
		return die("%v", err)
	}
	if ch == "" || al == "" {
		return die("forward needs a full <channel>@<host>/<alias> address")
	}
	p, err := splitVerbArgs(args[1:],
		map[string]bool{"--webhook-config": true, "--dead-letter": true, "--per-min": true, "--per-hour": true, "--max-attempts": true, "--timeout": true},
		map[string]bool{"--once": true, "--dry-run": true, "--print-service": true}, true)
	if err != nil {
		return die("%v", err)
	}
	if err := noExtra(p.pos, 0, forwardUsage); err != nil {
		return die("%v", err)
	}
	cfgPath, ok := p.has("--webhook-config")
	if !ok || cfgPath == "" {
		return die("--webhook-config is required\n%s", forwardUsage)
	}
	hook, err := client.LoadWebhookConfig(cfgPath)
	if err != nil {
		return die("%v", err)
	}
	ints := map[string]int{"--per-min": 6, "--per-hour": 30, "--max-attempts": 10}
	for name := range ints {
		if v, ok := p.has(name); ok {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return die("%s: want a positive integer, got %q", name, v)
			}
			ints[name] = n
		}
	}
	timeout := client.DefaultForwardTimeout
	if v, ok := p.has("--timeout"); ok {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return die("--timeout: want a positive duration such as 90s, got %q", v)
		}
		timeout = d
	}
	deadLetter, ok := p.has("--dead-letter")
	if !ok || deadLetter == "" {
		deadLetter = client.DefaultDeadLetter(ch, host, al)
	}
	if p.flags["--print-service"] {
		if p.flags["--once"] || p.flags["--dry-run"] {
			return die("--print-service describes a long-running forwarder; drop --once and --dry-run")
		}
		return printForwardService(ch, host, al, cfgPath, deadLetter, ints, timeout)
	}
	fd, err := client.ResolveFrontDoor(host)
	if err != nil {
		return die("%v", err)
	}
	token, _ := client.NewCredStore().Get(host, "token")
	if token == "" {
		return die("no relay token for %q — run: cbus auth set %s --token -", host, host)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logf := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "cbus forward: "+format+"\n", a...)
	}
	logf("%s@%s/%s -> webhook (%s relay, consumer %s, dead letters %s)", ch, host, al, map[bool]string{true: "local", false: "public"}[fd.Local], client.ForwardConsumer(al), deadLetter)
	err = client.Forward(ctx, client.ForwardOptions{
		Channel: ch, Host: host, Alias: al,
		Base: fd.Base, Token: token,
		Webhook: hook,
		PerMin:  ints["--per-min"], PerHour: ints["--per-hour"], MaxAttempts: ints["--max-attempts"],
		Timeout:    timeout,
		DeadLetter: deadLetter,
		DryRun:     p.flags["--dry-run"],
		Once:       p.flags["--once"] || p.flags["--dry-run"],
		OnceIdle:   3 * time.Second,
		Logf:       logf,
	})
	if err != nil && ctx.Err() == nil {
		return die("forward: %v", err)
	}
	return 0
}

// printForwardService writes a service definition to stdout and installs nothing.
func printForwardService(ch, host, al, cfgPath, deadLetter string, ints map[string]int, timeout time.Duration) int {
	exe, err := os.Executable()
	if err != nil {
		return die("%v", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	abs := func(p string) (string, error) { return filepath.Abs(p) }
	cfg, err := abs(cfgPath)
	if err != nil {
		return die("%v", err)
	}
	dl, err := abs(deadLetter)
	if err != nil {
		return die("%v", err)
	}
	argv := []string{exe, "forward", ch + "@" + host + "/" + al, "--webhook-config", cfg, "--dead-letter", dl,
		"--per-min", strconv.Itoa(ints["--per-min"]), "--per-hour", strconv.Itoa(ints["--per-hour"]),
		"--max-attempts", strconv.Itoa(ints["--max-attempts"]), "--timeout", timeout.String()}
	svc := forwardService{label: forwardServiceLabel(ch, host, al), argv: argv, env: forwardServiceEnv(host)}
	logDir := filepath.Dir(dl)
	if home, err := os.UserHomeDir(); err == nil {
		logDir = filepath.Join(home, "Library", "Logs") // launchd will not create a missing log folder
	}
	out, err := renderForwardService(runtime.GOOS, svc, logDir)
	if err != nil {
		return die("%v", err)
	}
	fmt.Print(out)
	return 0
}
