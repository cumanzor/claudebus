//go:build darwin || linux

package client

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
)

func prepareClaudeAdmission(req ConnectRequest, token string) (*ClaudeConnectionConfig, error) {
	if req.Claude == nil || !validClaudeCredentialToken(token) || req.Config != (CodexQueueConfig{}) {
		return nil, errors.New("Claude admission requires its exact caller binding and private capability, without Codex configuration")
	}
	b := *req.Claude
	if b.TranscriptSize <= 0 || b.TranscriptOffset <= 0 || b.TranscriptOffset > b.TranscriptSize {
		return nil, errors.New("Claude admission requires the captured transcript extent and complete-line checkpoint")
	}
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return nil, errors.New("Claude credential binding ID generation failed")
	}
	uuid[6], uuid[8] = uuid[6]&15|64, uuid[8]&63|128
	ref := fmt.Sprintf("%x-%x-%x-%x-%x.token", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
	cfg := &ClaudeConnectionConfig{Binding: b, CredentialRef: ref, ReceiptOffset: b.TranscriptOffset}
	c := &ConnectionState{Harness: daemonHarnessClaude, ThreadID: req.ThreadID, Claude: cfg}
	if err := validateConnectionBinding(c); err != nil {
		return nil, err
	}
	return cfg, nil // Native inspection precedes both alias claim and token storage.
}

func (d *busDaemon) persistClaudeCapability(cfg *ClaudeConnectionConfig, token string) error {
	ref, err := storeClaudeCredential(d.root, strings.TrimSuffix(cfg.CredentialRef, ".token"), token)
	if err != nil {
		return err
	}
	if ref != cfg.CredentialRef {
		return errors.New("Claude credential reference changed during storage")
	}
	return nil
}
