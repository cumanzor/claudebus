// Package wire preserves the relay transport import path while the daemon and
// relay share one WebSocket implementation.
package wire

import shared "claudebus/internal/wire"

type Conn = shared.Conn

const (
	OpText  = shared.OpText
	OpClose = shared.OpClose
	OpPing  = shared.OpPing
	OpPong  = shared.OpPong
)

var Upgrade = shared.Upgrade
var Dial = shared.Dial
