package client

import "errors"

var errClaudeSessionChanged = errors.New("bound Claude session is no longer current in its native process; reconnect from the intended session")
