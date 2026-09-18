//go:build darwin

package client

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func codexRolloutWriters(ctx context.Context, path string) ([]codexWriterFD, error) {
	return codexLsof(ctx, "-nP", "-F0pfan", "--", path)
}

func codexProcessFiles(ctx context.Context, pid int) ([]codexWriterFD, error) {
	return codexLsof(ctx, "-nP", "-a", "-p", strconv.Itoa(pid), "-F0pfan")
}

func codexLsof(parent context.Context, args ...string) ([]codexWriterFD, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	cmd := boundedCmd(ctx, "/usr/sbin/lsof", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// lsof exits 1 for a successfully inspected file with no open descriptors.
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || len(out) != 0 || stderr.Len() != 0 || ctx.Err() != nil {
			return nil, fmt.Errorf("inspect CLI rollout writers: %w (%s)", err, strings.TrimSpace(stderr.String()))
		}
		return nil, nil
	}
	return parseCodexLsof(out), nil
}

func parseCodexLsof(out []byte) []codexWriterFD {
	var files []codexWriterFD
	var current codexWriterFD
	for _, raw := range bytes.Split(out, []byte{0}) {
		value := strings.TrimPrefix(string(raw), "\n")
		if len(value) < 1 {
			continue
		}
		switch value[0] {
		case 'p':
			current = codexWriterFD{}
			current.PID, _ = strconv.Atoi(value[1:])
		case 'f':
			current.Access, current.Path = "", ""
		case 'a':
			current.Access = value[1:]
		case 'n':
			current.Path = value[1:]
			files = append(files, current)
		}
	}
	return files
}
