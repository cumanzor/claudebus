//go:build linux

package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func codexRolloutWriters(ctx context.Context, path string) ([]codexWriterFD, error) {
	es, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var found []codexWriterFD
	for _, e := range es {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, _, err := procParent(pid)
		if err != nil || commBase(comm) != "codex" {
			continue
		}
		files, err := codexProcessFiles(ctx, pid)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if sameExistingFile(f.Path, path) {
				found = append(found, f)
			}
		}
	}
	return found, nil
}

func codexProcessFiles(ctx context.Context, pid int) ([]codexWriterFD, error) {
	base := filepath.Join("/proc", strconv.Itoa(pid))
	es, err := os.ReadDir(filepath.Join(base, "fd"))
	if err != nil {
		return nil, err
	}
	var found []codexWriterFD
	for _, e := range es {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := os.Readlink(filepath.Join(base, "fd", e.Name()))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := os.ReadFile(filepath.Join(base, "fdinfo", e.Name()))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		access := ""
		for _, line := range strings.Split(string(info), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] != "flags:" {
				continue
			}
			flags, err := strconv.ParseUint(fields[1], 8, 64)
			if err != nil {
				return nil, fmt.Errorf("parse CLI descriptor access: %w", err)
			}
			switch flags & syscall.O_ACCMODE {
			case syscall.O_RDONLY:
				access = "r"
			case syscall.O_WRONLY:
				access = "w"
			case syscall.O_RDWR:
				access = "u"
			}
		}
		found = append(found, codexWriterFD{PID: pid, Path: path, Access: access})
	}
	return found, nil
}
