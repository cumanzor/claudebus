package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claudebus/internal/core"
)

const forwardSkippedReason = "skipped: queued before start"

// RelayQueued is the relay's own count of queued messages for one mailbox.
func RelayQueued(e RemoteEndpoint, channel, alias string) (int, error) {
	peers, err := RemoteList(e)
	if err != nil {
		return 0, err
	}
	return peers[channel+"/"+alias].Queued, nil
}

// skip settles one backlog message without a POST. The record goes to the dead
// letters first, so a wrong skip can be replayed and a failed write stays on the relay.
func (f *forwarder) skip(fr relayFrame) error {
	var m core.Message
	if json.Unmarshal(fr.Message, &m) == nil && m.Kind == "presence" {
		f.Logf("skip presence %s", fr.SpoolID)
		return nil
	}
	f.Logf("skip queued %s from=%s", fr.SpoolID, m.From)
	return f.deadLetter(fr, forwardSkippedReason)
}

// the lock covers one append or one rewrite, never a POST, so a running forwarder is not held up
func withDeadLetterLock(path string, fn func() error) error {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := tryLockExclusive(lock)
		if err == nil {
			break
		}
		if !errors.Is(err, errLockContended) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s is locked", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer unlockFile(lock)
	return fn()
}

// removeDeadLetterLine re-reads the file, so a record appended during a slow POST survives.
func removeDeadLetterLine(path string, line []byte) error {
	return withDeadLetterLock(path, func() error {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := bytes.SplitAfter(raw, []byte("\n"))
		kept := make([][]byte, 0, len(lines))
		removed := false
		for _, l := range lines {
			if !removed && bytes.Equal(bytes.TrimRight(l, "\n"), line) {
				removed = true
				continue
			}
			kept = append(kept, l)
		}
		if !removed {
			return nil
		}
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".deadletter-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err = tmp.Write(bytes.Join(kept, nil)); err == nil {
			err = tmp.Sync()
		}
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(tmp.Name(), fi.Mode().Perm())
		}
		if err != nil {
			return err
		}
		return os.Rename(tmp.Name(), path)
	})
}

// ReplayResult counts one replay. Failed records stay in the file.
type ReplayResult struct {
	Delivered, Failed, LeftSkipped int
}

// ReplayDeadLetters POSTs each record once. A delivered record leaves the file. A
// failed or unreadable one stays, so a later replay can try again.
func ReplayDeadLetters(ctx context.Context, o ForwardOptions, path string, ids []string) (ReplayResult, error) {
	var res ReplayResult
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.client == nil {
		o.client = newForwardClient(o.Timeout)
	}
	if o.sleep == nil {
		o.sleep = relayWait
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && len(ids) == 0 {
		o.Logf("no dead letters at %s", path)
		return res, nil
	}
	if err != nil {
		return res, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	f := &forwarder{ForwardOptions: o}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec struct {
			ID      string          `json:"id"`
			Reason  string          `json:"reason"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil || rec.ID == "" {
			o.Logf("keep an unreadable line in %s", path)
			if len(ids) == 0 {
				res.Failed++
			}
			continue
		}
		if len(ids) > 0 && !want[rec.ID] {
			continue
		}
		delete(want, rec.ID)
		// the operator chose not to deliver a skipped backlog, so it takes a name or a flag
		if rec.Reason == forwardSkippedReason && len(ids) == 0 && !o.IncludeSkipped {
			res.LeftSkipped++
			continue
		}
		var m core.Message
		if len(rec.Message) == 0 || json.Unmarshal(rec.Message, &m) != nil {
			o.Logf("keep %s: no message recorded", rec.ID)
			res.Failed++
			continue
		}
		body := forwardPayload(rec.ID, m, rec.Message)
		if o.DryRun {
			o.Logf("dry-run replay %s from=%s %d bytes (%s)", rec.ID, m.From, len(body), rec.Reason)
			continue
		}
		if !f.throttle(ctx) {
			return res, ctx.Err()
		}
		status, err := f.post(ctx, body)
		if err == nil && status/100 != 2 {
			err = fmt.Errorf("HTTP %d", status)
		}
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			o.Logf("keep %s: %v", rec.ID, err)
			res.Failed++
			continue
		}
		o.Logf("replayed %s from=%s HTTP %d", rec.ID, m.From, status)
		if err := removeDeadLetterLine(path, line); err != nil {
			return res, fmt.Errorf("replayed %s but could not remove it from %s: %w", rec.ID, path, err)
		}
		res.Delivered++
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for id := range want {
			missing = append(missing, id)
		}
		return res, fmt.Errorf("not in %s: %s", path, strings.Join(missing, ", "))
	}
	return res, nil
}
