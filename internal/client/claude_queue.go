package client

import "fmt"

// This configuration carries only a reference to the private capability token.
// ReceiptOffset is a complete-line checkpoint, persisted after positive receipt.
type ClaudeConnectionConfig struct {
	Binding       ClaudeConnectBinding `json:"binding"`
	CredentialRef string               `json:"credentialRef"`
	ReceiptOffset int64                `json:"receiptOffset"`
}

type claudeNotSubmittedError struct{ Err error }

func (e *claudeNotSubmittedError) Error() string {
	return fmt.Sprintf("Claude message not submitted: %v", e.Err)
}
func (e *claudeNotSubmittedError) Unwrap() error { return e.Err }

type claudeAwaitingReceiptError struct {
	State string
	Err   error
}

func (e *claudeAwaitingReceiptError) Error() string {
	if e.Err == nil {
		return "Claude message " + e.State + "; awaiting exact transcript receipt"
	}
	return fmt.Sprintf("Claude message %s; awaiting exact transcript receipt: %v", e.State, e.Err)
}
func (e *claudeAwaitingReceiptError) Unwrap() error { return e.Err }
